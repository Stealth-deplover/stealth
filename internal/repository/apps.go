package repository

// App control-plane persistence owns durable desired state and authorization.
// Builds, deployments, leases, runtime observation, and logs belong to later
// capabilities and are deliberately absent from this module.

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/platformhostname"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidAppSettings = errors.New("invalid app settings")

type AppActor = DatabaseActor

const (
	AppConsoleActor = DatabaseConsoleActor
	AppAPIKeyActor  = DatabaseAPIKeyActor
)

type AppInput struct {
	Name     string
	Enabled  bool
	Workload workloadspec.Spec
}

type AppPatch struct {
	Name     *string
	Enabled  *bool
	Workload *workloadspec.Spec
}

const appProjection = `app.id::text,app.project_id::text,app.name,app.enabled,app.workload_spec,app.workload_spec_sha256,app.desired_generation,app.observed_generation,app.runtime_status,app.runtime_error,app.created_at,app.updated_at,app.platform_label,settings.workload_base_domain`

type appScanner interface{ Scan(...any) error }

func scanApp(row appScanner) (domain.App, error) {
	var item domain.App
	var rawSpec []byte
	var platformLabel string
	var workloadBaseDomain *string
	if err := row.Scan(
		&item.ID,
		&item.ProjectID,
		&item.Name,
		&item.Enabled,
		&rawSpec,
		&item.WorkloadSpecSHA256,
		&item.DesiredGeneration,
		&item.ObservedGeneration,
		&item.RuntimeStatus,
		&item.RuntimeError,
		&item.CreatedAt,
		&item.UpdatedAt,
		&platformLabel,
		&workloadBaseDomain,
	); err != nil {
		return domain.App{}, err
	}
	spec, err := workloadspec.Decode(rawSpec)
	if err != nil {
		return domain.App{}, fmt.Errorf("stored App WorkloadSpec is invalid: %w", err)
	}
	digest, err := workloadspec.Digest(spec)
	if err != nil {
		return domain.App{}, err
	}
	if digest != item.WorkloadSpecSHA256 {
		return domain.App{}, fmt.Errorf("stored App WorkloadSpec digest does not match its canonical content")
	}
	item.Workload = spec
	if workloadBaseDomain != nil {
		hostname, hostnameErr := platformhostname.Hostname(platformLabel, *workloadBaseDomain)
		if hostnameErr != nil {
			return domain.App{}, hostnameErr
		}
		item.PlatformHostname = &hostname
	}
	return item, nil
}

func (r *Repository) requireAppRead(ctx context.Context, projectID uuid.UUID, actor AppActor) (bool, error) {
	switch actor.Kind {
	case AppConsoleActor:
		role, err := r.projectRole(ctx, projectID, actor.AccountID)
		if err != nil {
			return false, err
		}
		return role == "owner" || role == "admin", nil
	case AppAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "apps.read") {
			return false, ErrForbidden
		}
		var active bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM project_api_keys
			WHERE id=$1 AND project_id=$2
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at>now())
			  AND 'apps.read' = ANY(scopes)
		)`, actor.APIKeyID, projectID).Scan(&active); err != nil {
			return false, err
		}
		if !active {
			return false, ErrNotFound
		}
		return apikey.HasScope(actor.APIKeyScopes, "apps.write"), nil
	default:
		return false, ErrForbidden
	}
}

func (r *Repository) requireAppWriteTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor AppActor) error {
	switch actor.Kind {
	case AppConsoleActor:
		return requireProjectRoleTx(ctx, tx, projectID, actor.AccountID, "owner", "admin")
	case AppAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "apps.write") {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, projectID, actor.APIKeyID, "apps.write")
	default:
		return ErrForbidden
	}
}

func appByID(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, appID uuid.UUID, lock bool) (domain.App, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF app"
	}
	item, err := scanApp(query.QueryRow(ctx, `
		SELECT `+appProjection+`
		FROM project_apps app
		LEFT JOIN instance_domain_settings settings ON settings.id=TRUE
		WHERE app.project_id=$1 AND app.id=$2`+suffix, projectID, appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.App{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) ListApps(ctx context.Context, projectID uuid.UUID, actor AppActor, limit int, cursor *uuid.UUID) ([]domain.App, string, bool, error) {
	canManage, err := r.requireAppRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+appProjection+`
		FROM project_apps app
		LEFT JOIN instance_domain_settings settings ON settings.id=TRUE
		WHERE app.project_id=$1 AND ($3::uuid IS NULL OR app.id>$3)
		ORDER BY app.id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.App, 0, limit)
	for rows.Next() {
		item, scanErr := scanApp(rows)
		if scanErr != nil {
			return nil, "", false, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, canManage, nil
}

func (r *Repository) GetApp(ctx context.Context, projectID, appID uuid.UUID, actor AppActor) (domain.App, error) {
	if _, err := r.requireAppRead(ctx, projectID, actor); err != nil {
		return domain.App{}, err
	}
	return appByID(ctx, r.pool, projectID, appID, false)
}

func (r *Repository) CreateApp(ctx context.Context, id, projectID uuid.UUID, actor AppActor, input AppInput) (domain.App, error) {
	name, err := validate.Slug(input.Name, "name")
	if err != nil {
		return domain.App{}, ErrInvalidAppSettings
	}
	spec, err := workloadspec.Normalize(input.Workload)
	if err != nil {
		return domain.App{}, err
	}
	canonical, err := workloadspec.MarshalCanonical(spec)
	if err != nil {
		return domain.App{}, err
	}
	digest, err := workloadspec.Digest(spec)
	if err != nil {
		return domain.App{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.App{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireAppWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.App{}, err
	}
	organizationID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.App{}, err
	}
	if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "apps"); err != nil {
		return domain.App{}, err
	}

	allocated := false
	for _, platformLabel := range platformhostname.AppCandidates(name, id) {
		claim, claimErr := tx.Exec(ctx, `
			INSERT INTO platform_hostname_claims (label,resource_type,resource_id,project_id)
			VALUES ($1,'app',$2,$3)
			ON CONFLICT (label) DO NOTHING`, platformLabel, id, projectID)
		if claimErr != nil {
			return domain.App{}, mapError(claimErr)
		}
		if claim.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_apps (id,project_id,name,platform_label,enabled,workload_spec,workload_spec_sha256)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7)`, id, projectID, name, platformLabel, input.Enabled, canonical, digest); err != nil {
			return domain.App{}, mapError(err)
		}
		allocated = true
		break
	}
	if !allocated {
		return domain.App{}, ErrConflict
	}
	item, err := appByID(ctx, tx, projectID, id, false)
	if err != nil {
		return domain.App{}, err
	}
	if err := r.auditApp(ctx, tx, projectID, actor, "app.create", id, appAuditMetadata(item)); err != nil {
		return domain.App{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.App{}, err
	}
	return item, nil
}

func (r *Repository) UpdateApp(ctx context.Context, projectID, appID uuid.UUID, actor AppActor, patch AppPatch) (domain.App, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.App{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireAppWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.App{}, err
	}
	existing, err := appByID(ctx, tx, projectID, appID, true)
	if err != nil {
		return domain.App{}, err
	}
	name := existing.Name
	if patch.Name != nil {
		name, err = validate.Slug(*patch.Name, "name")
		if err != nil {
			return domain.App{}, ErrInvalidAppSettings
		}
	}
	enabled := existing.Enabled
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	spec := existing.Workload
	if patch.Workload != nil {
		spec, err = workloadspec.Normalize(*patch.Workload)
		if err != nil {
			return domain.App{}, err
		}
	}
	runtimeChanged := enabled != existing.Enabled || !workloadspec.Equal(spec, existing.Workload)
	metadataChanged := name != existing.Name || runtimeChanged
	if !metadataChanged {
		if err := tx.Commit(ctx); err != nil {
			return domain.App{}, err
		}
		return existing, nil
	}

	desiredGeneration := existing.DesiredGeneration
	digest := existing.WorkloadSpecSHA256
	var canonical []byte
	if runtimeChanged {
		if desiredGeneration == math.MaxInt64 {
			return domain.App{}, ErrInvalidAppSettings
		}
		desiredGeneration++
		digest, err = workloadspec.Digest(spec)
		if err != nil {
			return domain.App{}, err
		}
		canonical, err = workloadspec.MarshalCanonical(spec)
		if err != nil {
			return domain.App{}, err
		}
	} else {
		canonical, err = workloadspec.MarshalCanonical(existing.Workload)
		if err != nil {
			return domain.App{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE project_apps
		SET name=$3,enabled=$4,workload_spec=$5::jsonb,workload_spec_sha256=$6,
		    desired_generation=$7,updated_at=now()
		WHERE project_id=$1 AND id=$2`, projectID, appID, name, enabled, canonical, digest, desiredGeneration); err != nil {
		return domain.App{}, mapError(err)
	}
	item, err := appByID(ctx, tx, projectID, appID, false)
	if err != nil {
		return domain.App{}, err
	}
	if err := r.auditApp(ctx, tx, projectID, actor, "app.update", appID, appAuditMetadata(item)); err != nil {
		return domain.App{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.App{}, err
	}
	return item, nil
}

func (r *Repository) DeleteApp(ctx context.Context, projectID, appID uuid.UUID, actor AppActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireAppWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	item, err := appByID(ctx, tx, projectID, appID, true)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_service_layouts WHERE project_id=$1 AND resource_type='app' AND resource_id=$2`, projectID, appID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM platform_hostname_claims WHERE project_id=$1 AND resource_type='app' AND resource_id=$2`, projectID, appID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_apps WHERE project_id=$1 AND id=$2`, projectID, appID); err != nil {
		return err
	}
	if err := r.auditApp(ctx, tx, projectID, actor, "app.delete", appID, appAuditMetadata(item)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func appAuditMetadata(item domain.App) map[string]any {
	return map[string]any{
		"name":                 item.Name,
		"enabled":              item.Enabled,
		"desired_generation":   item.DesiredGeneration,
		"workload_spec_sha256": item.WorkloadSpecSHA256,
	}
}

func (r *Repository) auditApp(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor AppActor, action string, appID uuid.UUID, metadata map[string]any) error {
	organizationID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	metadata["project_id"] = projectID.String()
	if actor.Kind == AppAPIKeyActor {
		metadata["actor"] = "api_key"
		metadata["api_key_id"] = actor.APIKeyID.String()
	}
	actorID := uuid.Nil
	if actor.Kind == AppConsoleActor {
		actorID = actor.AccountID
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, actorID, action, "app", appID, metadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, action, "app", appID, metadata)
}
