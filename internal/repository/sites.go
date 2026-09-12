package repository

// Site control-plane persistence owns Site metadata, authorization, and
// tenant-scoped audit events. Deployment lifecycle and public artifact
// publication live in sibling modules so control-plane changes stay local.

import (
	"context"
	"errors"
	"sort"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrSiteQuotaExceeded     = errors.New("site artifact quota exceeded")
	ErrSiteDisabled          = errors.New("site is disabled")
	ErrSiteDeploymentActive  = errors.New("active site deployment cannot be deleted")
	ErrInvalidSiteTransition = errors.New("invalid site deployment transition")
	ErrInvalidSiteSettings   = errors.New("invalid site settings")
	ErrSiteArtifactTooLarge  = errors.New("site artifact is too large")
	ErrNoSiteDeploymentJob   = errors.New("no site deployment build job available")
	ErrSiteBuildNotAvailable = errors.New("site deployment build is not available")
)

// Sites use the same explicit management actor boundary as Storage and
// Functions. API keys are project-bound and never become Console accounts.
type SiteActor = DatabaseActor

const (
	SiteConsoleActor = DatabaseConsoleActor
	SiteAPIKeyActor  = DatabaseAPIKeyActor
)

type SiteInput struct {
	Name               string
	Framework          string
	Enabled            bool
	Status             string
	ArtifactQuotaBytes int64
}

type SitePatch struct {
	Name               *string
	Framework          *string
	Enabled            *bool
	Status             *string
	ArtifactQuotaBytes *int64
}

type SiteDeploymentInput struct {
	Source             string
	SourceName         *string
	GitRepository      *string
	GitRef             *string
	SizeBytes          int64
	ArchiveSizeBytes   int64
	ChecksumSHA256     string
	ArtifactPath       string
	SourcePath         *string
	BuildRuntime       string
	BuildCommand       string
	OutputDirectory    string
	ReservedBytes      int64
	CreatedByAccountID *uuid.UUID
	Activate           bool
}

// SiteBuildJob is the worker-only view of a source deployment. Private
// storage paths never cross an HTTP or SDK boundary.
type SiteBuildJob struct {
	Site         domain.Site
	Deployment   domain.SiteDeployment
	SourcePath   string
	ArtifactPath string
}

// SitePublicArtifact is an internal lookup result. ArtifactPath must never be
// serialized or accepted from a client; it is returned only to the static
// file-serving handler after the active deployment is resolved in PostgreSQL.
type SitePublicArtifact struct {
	Site         domain.Site
	Deployment   domain.SiteDeployment
	ArtifactPath string
}

// SiteStoragePaths are private filesystem locators returned only after a
// metadata transaction commits. Source and public artifact stores are
// separate namespaces and must both be cleaned up by the caller.
type SiteStoragePaths struct {
	ArtifactPath string
	SourcePath   string
}

const siteProjection = `id,project_id,name,framework,enabled,status,artifact_quota_bytes,artifact_used_bytes,artifact_reserved_bytes,active_deployment_id,created_at,updated_at`
const siteDeploymentProjection = `id,site_id,project_id,version,source,source_name,size_bytes,archive_size_bytes,checksum_sha256,status,build_runtime,build_command,output_directory,reserved_bytes,build_status,activate_requested,error_message,created_by_account_id,queued_at,build_started_at,built_at,activated_at,finished_at,created_at,updated_at,git_repository,git_ref`
const siteBuildLogProjection = `id,deployment_id,site_id,project_id,sequence,level,message,created_at`

type siteScanner interface{ Scan(...any) error }

func scanSite(row siteScanner) (domain.Site, error) {
	var item domain.Site
	var active *uuid.UUID
	err := row.Scan(&item.ID, &item.ProjectID, &item.Name, &item.Framework, &item.Enabled, &item.Status, &item.ArtifactQuotaBytes, &item.ArtifactUsedBytes, &item.ArtifactReservedBytes, &active, &item.CreatedAt, &item.UpdatedAt)
	if err == nil && active != nil {
		value := active.String()
		item.ActiveDeploymentID = &value
	}
	return item, err
}

func scanSiteDeploymentPublic(row siteScanner) (domain.SiteDeployment, error) {
	var item domain.SiteDeployment
	var createdBy *uuid.UUID
	err := row.Scan(&item.ID, &item.SiteID, &item.ProjectID, &item.Version, &item.Source, &item.SourceName, &item.SizeBytes, &item.ArchiveSizeBytes, &item.ChecksumSHA256, &item.Status, &item.BuildRuntime, &item.BuildCommand, &item.OutputDirectory, &item.ReservedBytes, &item.BuildStatus, &item.ActivateRequested, &item.ErrorMessage, &createdBy, &item.QueuedAt, &item.BuildStartedAt, &item.BuiltAt, &item.ActivatedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt, &item.GitRepository, &item.GitRef)
	if err == nil && createdBy != nil {
		value := createdBy.String()
		item.CreatedByAccountID = &value
	}
	return item, err
}

func scanSiteDeploymentWithPath(row siteScanner) (domain.SiteDeployment, string, string, error) {
	var item domain.SiteDeployment
	var createdBy *uuid.UUID
	var sourcePath *string
	var artifactPath string
	err := row.Scan(&item.ID, &item.SiteID, &item.ProjectID, &item.Version, &item.Source, &item.SourceName, &item.SizeBytes, &item.ArchiveSizeBytes, &item.ChecksumSHA256, &item.Status, &item.BuildRuntime, &item.BuildCommand, &item.OutputDirectory, &item.ReservedBytes, &item.BuildStatus, &item.ActivateRequested, &item.ErrorMessage, &createdBy, &item.QueuedAt, &item.BuildStartedAt, &item.BuiltAt, &item.ActivatedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt, &item.GitRepository, &item.GitRef, &sourcePath, &artifactPath)
	if err == nil && createdBy != nil {
		value := createdBy.String()
		item.CreatedByAccountID = &value
	}
	if sourcePath == nil {
		return item, "", artifactPath, err
	}
	return item, *sourcePath, artifactPath, err
}

func scanSiteBuildLog(row siteScanner) (domain.SiteBuildLog, error) {
	var item domain.SiteBuildLog
	err := row.Scan(&item.ID, &item.DeploymentID, &item.SiteID, &item.ProjectID, &item.Sequence, &item.Level, &item.Message, &item.CreatedAt)
	return item, err
}

func (r *Repository) requireSiteRead(ctx context.Context, projectID uuid.UUID, actor SiteActor) (bool, error) {
	switch actor.Kind {
	case SiteConsoleActor:
		role, err := r.projectRole(ctx, projectID, actor.AccountID)
		if err != nil {
			return false, err
		}
		return role == "owner" || role == "admin", nil
	case SiteAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "sites.read") {
			return false, ErrForbidden
		}
		var active bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM project_api_keys
			WHERE id=$1 AND project_id=$2
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at>now())
			  AND 'sites.read' = ANY(scopes)
		)`, actor.APIKeyID, projectID).Scan(&active); err != nil {
			return false, err
		}
		if !active {
			return false, ErrNotFound
		}
		return apikey.HasScope(actor.APIKeyScopes, "sites.write"), nil
	default:
		return false, ErrForbidden
	}
}

func (r *Repository) requireSiteWriteTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor SiteActor) error {
	switch actor.Kind {
	case SiteConsoleActor:
		return requireProjectRoleTx(ctx, tx, projectID, actor.AccountID, "owner", "admin")
	case SiteAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "sites.write") {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, projectID, actor.APIKeyID, "sites.write")
	default:
		return ErrForbidden
	}
}

func (r *Repository) siteByID(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, siteID uuid.UUID, lock bool) (domain.Site, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, err := scanSite(query.QueryRow(ctx, `SELECT `+siteProjection+` FROM project_sites WHERE project_id=$1 AND id=$2`+suffix, projectID, siteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Site{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) ListSites(ctx context.Context, projectID uuid.UUID, actor SiteActor, limit int, cursor *uuid.UUID) ([]domain.Site, string, bool, error) {
	canManage, err := r.requireSiteRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+siteProjection+` FROM project_sites WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.Site, 0, limit)
	for rows.Next() {
		item, scanErr := scanSite(rows)
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

func (r *Repository) GetSite(ctx context.Context, projectID, siteID uuid.UUID, actor SiteActor) (domain.Site, error) {
	if _, err := r.requireSiteRead(ctx, projectID, actor); err != nil {
		return domain.Site{}, err
	}
	return r.siteByID(ctx, r.pool, projectID, siteID, false)
}

func (r *Repository) CreateSite(ctx context.Context, id, projectID uuid.UUID, actor SiteActor, input SiteInput) (domain.Site, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Site{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.Site{}, err
	}
	organizationID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.Site{}, err
	}
	if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "sites"); err != nil {
		return domain.Site{}, err
	}
	if input.Framework == "" {
		input.Framework = "static"
	}
	if input.Status == "" {
		if input.Enabled {
			input.Status = "active"
		} else {
			input.Status = "disabled"
		}
	}
	if input.ArtifactQuotaBytes <= 0 || input.Framework != "static" || (input.Status == "active") != input.Enabled {
		return domain.Site{}, ErrInvalidSiteSettings
	}
	item, err := scanSite(tx.QueryRow(ctx, `INSERT INTO project_sites (id,project_id,name,framework,enabled,status,artifact_quota_bytes) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+siteProjection, id, projectID, input.Name, input.Framework, input.Enabled, input.Status, input.ArtifactQuotaBytes))
	if err != nil {
		return domain.Site{}, mapError(err)
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site.create", "site", id, map[string]any{"name": input.Name, "framework": input.Framework}); err != nil {
		return domain.Site{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Site{}, err
	}
	return item, nil
}

func (r *Repository) UpdateSite(ctx context.Context, projectID, siteID uuid.UUID, actor SiteActor, patch SitePatch) (domain.Site, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Site{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.Site{}, err
	}
	existing, err := r.siteByID(ctx, tx, projectID, siteID, true)
	if err != nil {
		return domain.Site{}, err
	}
	name, framework, enabled, status, quota := existing.Name, existing.Framework, existing.Enabled, existing.Status, existing.ArtifactQuotaBytes
	if patch.Name != nil {
		name = *patch.Name
	}
	if patch.Framework != nil {
		framework = *patch.Framework
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if patch.Status != nil {
		status = *patch.Status
	}
	if patch.ArtifactQuotaBytes != nil {
		quota = *patch.ArtifactQuotaBytes
	}
	if patch.Status != nil && patch.Enabled == nil {
		enabled = status == "active"
	}
	if patch.Enabled != nil && patch.Status == nil {
		if enabled {
			status = "active"
		} else {
			status = "disabled"
		}
	}
	if framework != "static" || (status != "active" && status != "disabled") || (status == "active") != enabled || quota <= 0 || quota < existing.ArtifactUsedBytes+existing.ArtifactReservedBytes {
		return domain.Site{}, ErrInvalidSiteSettings
	}
	item, err := scanSite(tx.QueryRow(ctx, `UPDATE project_sites SET name=$3,framework=$4,enabled=$5,status=$6,artifact_quota_bytes=$7,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING `+siteProjection, projectID, siteID, name, framework, enabled, status, quota))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Site{}, ErrNotFound
	}
	if err != nil {
		return domain.Site{}, mapError(err)
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site.update", "site", siteID, map[string]any{"changed_fields": siteChangedFields(patch)}); err != nil {
		return domain.Site{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Site{}, err
	}
	return item, nil
}

func siteChangedFields(patch SitePatch) []string {
	fields := make([]string, 0, 5)
	if patch.Name != nil {
		fields = append(fields, "name")
	}
	if patch.Framework != nil {
		fields = append(fields, "framework")
	}
	if patch.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if patch.Status != nil {
		fields = append(fields, "status")
	}
	if patch.ArtifactQuotaBytes != nil {
		fields = append(fields, "artifact_quota_bytes")
	}
	sort.Strings(fields)
	return fields
}

func (r *Repository) DeleteSite(ctx context.Context, projectID, siteID uuid.UUID, actor SiteActor) ([]SiteStoragePaths, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := r.siteByID(ctx, tx, projectID, siteID, true); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT artifact_path,source_path FROM site_deployments WHERE project_id=$1 AND site_id=$2 FOR UPDATE`, projectID, siteID)
	if err != nil {
		return nil, err
	}
	paths := make([]SiteStoragePaths, 0)
	for rows.Next() {
		var pathsItem SiteStoragePaths
		var sourcePath *string
		if err := rows.Scan(&pathsItem.ArtifactPath, &sourcePath); err != nil {
			rows.Close()
			return nil, err
		}
		if sourcePath != nil {
			pathsItem.SourcePath = *sourcePath
		}
		paths = append(paths, pathsItem)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `DELETE FROM project_sites WHERE project_id=$1 AND id=$2`, projectID, siteID); err != nil {
		return nil, err
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site.delete", "site", siteID, map[string]any{"deployment_count": len(paths)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return paths, nil
}

func (r *Repository) auditSite(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor SiteActor, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["project_id"] = projectID.String()
	if actor.Kind == SiteAPIKeyActor {
		metadata["actor"] = "api_key"
		metadata["api_key_id"] = actor.APIKeyID.String()
	}
	actorID := uuid.Nil
	if actor.Kind == SiteConsoleActor {
		actorID = actor.AccountID
	}
	if err := writeAuditMetadata(ctx, tx, orgID, actorID, action, targetType, target, metadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, action, targetType, target, metadata)
}
