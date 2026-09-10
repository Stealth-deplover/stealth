package repository

import (
	"context"
	"errors"
	"slices"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) ProjectRegistrationEnabled(ctx context.Context, projectID uuid.UUID) (bool, error) {
	var enabled bool
	err := r.pool.QueryRow(ctx, `SELECT registration_enabled FROM project_auth_settings WHERE project_id=$1`, projectID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return enabled, err
}

func (r *Repository) ProjectAuthSettings(ctx context.Context, projectID, accountID uuid.UUID) (domain.ProjectAuthSettings, bool, error) {
	role, err := r.projectRole(ctx, projectID, accountID)
	if err != nil {
		return domain.ProjectAuthSettings{}, false, err
	}
	var item domain.ProjectAuthSettings
	err = r.pool.QueryRow(ctx, `
		SELECT project_id,registration_enabled,cors_origins,created_at,updated_at
		FROM project_auth_settings WHERE project_id=$1`, projectID).
		Scan(&item.ProjectID, &item.RegistrationEnabled, &item.CORSOrigins, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectAuthSettings{}, false, ErrNotFound
	}
	if item.CORSOrigins == nil {
		item.CORSOrigins = []string{}
	}
	return item, role == "owner" || role == "admin", err
}

func (r *Repository) UpdateProjectAuthSettings(ctx context.Context, projectID, accountID uuid.UUID, registrationEnabled *bool, corsOrigins *[]string) (domain.ProjectAuthSettings, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ProjectAuthSettings{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.ProjectAuthSettings{}, err
	}
	var item domain.ProjectAuthSettings
	err = tx.QueryRow(ctx, `
		SELECT project_id,registration_enabled,cors_origins,created_at,updated_at
		FROM project_auth_settings WHERE project_id=$1 FOR UPDATE`, projectID).
		Scan(&item.ProjectID, &item.RegistrationEnabled, &item.CORSOrigins, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectAuthSettings{}, ErrNotFound
	}
	if err != nil {
		return domain.ProjectAuthSettings{}, err
	}
	if item.CORSOrigins == nil {
		item.CORSOrigins = []string{}
	}
	previousRegistration := item.RegistrationEnabled
	previousOrigins := append([]string{}, item.CORSOrigins...)
	nextRegistration := item.RegistrationEnabled
	if registrationEnabled != nil {
		nextRegistration = *registrationEnabled
	}
	nextOrigins := append([]string{}, item.CORSOrigins...)
	if corsOrigins != nil {
		nextOrigins = append([]string{}, (*corsOrigins)...)
	}
	if nextRegistration != item.RegistrationEnabled || !slices.Equal(nextOrigins, item.CORSOrigins) {
		err = tx.QueryRow(ctx, `
			UPDATE project_auth_settings
			SET registration_enabled=$2,cors_origins=$3,updated_at=now()
			WHERE project_id=$1
			RETURNING project_id,registration_enabled,cors_origins,created_at,updated_at`, projectID, nextRegistration, nextOrigins).
			Scan(&item.ProjectID, &item.RegistrationEnabled, &item.CORSOrigins, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			return domain.ProjectAuthSettings{}, err
		}
		orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
		if err != nil {
			return domain.ProjectAuthSettings{}, err
		}
		metadata := map[string]any{
			"project_id":           projectID.String(),
			"registration_enabled": map[string]bool{"from": previousRegistration, "to": nextRegistration},
			"cors_origins":         map[string][]string{"from": previousOrigins, "to": append([]string{}, nextOrigins...)},
		}
		if err := writeAuditMetadata(ctx, tx, orgID, accountID, "project_auth.settings_update", "project", projectID, metadata); err != nil {
			return domain.ProjectAuthSettings{}, err
		}
		if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_auth.settings_update", "project", projectID, metadata); err != nil {
			return domain.ProjectAuthSettings{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProjectAuthSettings{}, err
	}
	return item, nil
}

// UpdateProjectUserStatus is idempotent: repeating the current status returns
// the same DTO without producing a misleading status-change audit event.
