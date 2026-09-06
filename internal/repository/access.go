package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (r *Repository) requireProjectAccess(ctx context.Context, projectID, accountID uuid.UUID) error {
	_, err := r.projectRole(ctx, projectID, accountID)
	return err
}

func (r *Repository) projectRole(ctx context.Context, projectID, accountID uuid.UUID) (string, error) {
	var role string
	err := r.pool.QueryRow(ctx, `
		SELECT m.role
		FROM projects p
		JOIN organization_memberships m ON m.organization_id=p.organization_id
		WHERE p.id=$1 AND m.account_id=$2`, projectID, accountID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func requireProjectRoleTx(ctx context.Context, tx pgx.Tx, projectID, accountID uuid.UUID, allowed ...string) error {
	role, err := projectRoleTx(ctx, tx, projectID, accountID)
	if err != nil {
		return err
	}
	for _, candidate := range allowed {
		if role == candidate {
			return nil
		}
	}
	return ErrForbidden
}

func projectRoleTx(ctx context.Context, tx pgx.Tx, projectID, accountID uuid.UUID) (string, error) {
	var role string
	err := tx.QueryRow(ctx, `
		SELECT m.role
		FROM projects p
		JOIN organization_memberships m ON m.organization_id=p.organization_id
		WHERE p.id=$1 AND m.account_id=$2
		FOR SHARE`, projectID, accountID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func projectOrganizationIDValue(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, projectID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return orgID, err
}
func (r *Repository) requireMembership(ctx context.Context, org, account uuid.UUID) error {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND account_id=$2)`, org, account).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrForbidden
	}
	return nil
}
func requireRoleTx(ctx context.Context, tx pgx.Tx, org, account uuid.UUID, allowed ...string) error {
	var role string
	err := tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND account_id=$2 FOR SHARE`, org, account).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	for _, candidate := range allowed {
		if role == candidate {
			return nil
		}
	}
	return ErrForbidden
}
func writeAudit(ctx context.Context, tx pgx.Tx, org, actor uuid.UUID, action, targetType string, target uuid.UUID) error {
	return writeAuditMetadata(ctx, tx, org, actor, action, targetType, target, map[string]string{})
}

func writeAuditMetadata(ctx context.Context, tx pgx.Tx, org, actor uuid.UUID, action, targetType string, target uuid.UUID, value any) error {
	var orgID, actorID any
	if org != uuid.Nil {
		orgID = org
	}
	if actor != uuid.Nil {
		actorID = actor
	}
	metadata, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events (id,organization_id,actor_account_id,action,target_type,target_id,metadata) VALUES ($1,$2,$3,$4,$5,$6,$7)`, uuid.Must(uuid.NewV7()), orgID, actorID, action, targetType, target, metadata)
	return err
}
func mapError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}
func ParseUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid id")
	}
	return id, nil
}
