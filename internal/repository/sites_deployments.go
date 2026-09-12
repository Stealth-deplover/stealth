package repository

// Site deployment persistence owns source metadata, activation, build leases,
// quota transitions, and worker build logs. It is intentionally independent
// from public artifact lookup and Site control-plane CRUD.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) siteDeploymentByID(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, siteID, deploymentID uuid.UUID, lock bool, includePath bool) (domain.SiteDeployment, string, error) {
	if includePath {
		item, _, artifactPath, err := r.siteDeploymentByIDWithPaths(ctx, query, projectID, siteID, deploymentID, lock)
		return item, artifactPath, err
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	projection := siteDeploymentProjection
	row := query.QueryRow(ctx, `SELECT `+projection+` FROM site_deployments WHERE project_id=$1 AND site_id=$2 AND id=$3`+suffix, projectID, siteID, deploymentID)
	item, err := scanSiteDeploymentPublic(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SiteDeployment{}, "", ErrNotFound
	}
	if err != nil {
		return domain.SiteDeployment{}, "", err
	}
	return item, "", nil
}

func (r *Repository) siteDeploymentByIDWithPaths(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, siteID, deploymentID uuid.UUID, lock bool) (domain.SiteDeployment, string, string, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, sourcePath, artifactPath, err := scanSiteDeploymentWithPath(query.QueryRow(ctx, `SELECT `+siteDeploymentProjection+`,source_path,artifact_path FROM site_deployments WHERE project_id=$1 AND site_id=$2 AND id=$3`+suffix, projectID, siteID, deploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SiteDeployment{}, "", "", ErrNotFound
	}
	if err != nil {
		return domain.SiteDeployment{}, "", "", err
	}
	return item, sourcePath, artifactPath, nil
}

func (r *Repository) ListSiteDeployments(ctx context.Context, projectID, siteID uuid.UUID, actor SiteActor, limit int, cursor *uuid.UUID) ([]domain.SiteDeployment, string, bool, error) {
	canManage, err := r.requireSiteRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if _, err := r.siteByID(ctx, r.pool, projectID, siteID, false); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+siteDeploymentProjection+` FROM site_deployments WHERE project_id=$1 AND site_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, siteID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.SiteDeployment, 0, limit)
	for rows.Next() {
		item, scanErr := scanSiteDeploymentPublic(rows)
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

// AppendSiteBuildLog appends one lifecycle message. A non-positive sequence
// asks the repository to allocate the next sequence while locking the
// deployment, which keeps concurrent workers and retries ordered.
func (r *Repository) AppendSiteBuildLog(ctx context.Context, projectID, siteID, deploymentID, id uuid.UUID, sequence int64, level, message string) (domain.SiteBuildLog, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteBuildLog{}, err
	}
	defer tx.Rollback(ctx)
	if sequence <= 0 {
		if _, _, err := r.siteDeploymentByID(ctx, tx, projectID, siteID, deploymentID, true, false); err != nil {
			return domain.SiteBuildLog{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM site_build_logs WHERE project_id=$1 AND site_id=$2 AND deployment_id=$3`, projectID, siteID, deploymentID).Scan(&sequence); err != nil {
			return domain.SiteBuildLog{}, err
		}
	} else if _, _, err := r.siteDeploymentByID(ctx, tx, projectID, siteID, deploymentID, false, false); err != nil {
		return domain.SiteBuildLog{}, err
	}
	level = strings.ToLower(strings.TrimSpace(level))
	message = normalizeSiteBuildLogMessage(message)
	item, err := scanSiteBuildLog(tx.QueryRow(ctx, `INSERT INTO site_build_logs (id,deployment_id,site_id,project_id,sequence,level,message) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+siteBuildLogProjection, id, deploymentID, siteID, projectID, sequence, level, message))
	if err != nil {
		return domain.SiteBuildLog{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteBuildLog{}, err
	}
	return item, nil
}

// ListSiteBuildLogs returns only logs for the requested tenant/site/deployment
// tuple. The sequence cursor is stable while a worker appends new messages.
func (r *Repository) ListSiteBuildLogs(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, actor SiteActor, limit int, after int64) ([]domain.SiteBuildLog, error) {
	if _, err := r.requireSiteRead(ctx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := r.siteByID(ctx, r.pool, projectID, siteID, false); err != nil {
		return nil, err
	}
	if _, _, err := r.siteDeploymentByID(ctx, r.pool, projectID, siteID, deploymentID, false, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+siteBuildLogProjection+` FROM site_build_logs WHERE project_id=$1 AND site_id=$2 AND deployment_id=$3 AND sequence>$4 ORDER BY sequence LIMIT $5`, projectID, siteID, deploymentID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.SiteBuildLog, 0, limit)
	for rows.Next() {
		item, scanErr := scanSiteBuildLog(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetSiteDeployment(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, actor SiteActor) (domain.SiteDeployment, error) {
	if _, err := r.requireSiteRead(ctx, projectID, actor); err != nil {
		return domain.SiteDeployment{}, err
	}
	if _, err := r.siteByID(ctx, r.pool, projectID, siteID, false); err != nil {
		return domain.SiteDeployment{}, err
	}
	item, _, err := r.siteDeploymentByID(ctx, r.pool, projectID, siteID, deploymentID, false, false)
	return item, err
}

func (r *Repository) CreateSiteDeployment(ctx context.Context, id, projectID, siteID uuid.UUID, actor SiteActor, input SiteDeploymentInput) (domain.SiteDeployment, error) {
	if input.SizeBytes < 0 || input.ArchiveSizeBytes <= 0 || !validSiteArtifactPath(input.ArtifactPath) || !validSiteSHA256(input.ChecksumSHA256) {
		return domain.SiteDeployment{}, ErrInvalidSiteSettings
	}
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	if input.Source != "github" && input.Source != "gitlab" {
		if input.GitRepository != nil || input.GitRef != nil {
			return domain.SiteDeployment{}, ErrInvalidSiteSettings
		}
	} else if input.GitRepository == nil || input.GitRef == nil || strings.TrimSpace(*input.GitRepository) == "" || strings.TrimSpace(*input.GitRef) == "" || len([]byte(strings.TrimSpace(*input.GitRepository))) > 512 || len([]byte(strings.TrimSpace(*input.GitRef))) > 256 {
		return domain.SiteDeployment{}, ErrInvalidSiteSettings
	}
	if input.BuildRuntime == "" {
		input.BuildRuntime = "node-22"
	}
	if input.OutputDirectory == "" {
		input.OutputDirectory = "."
	}
	building := strings.TrimSpace(input.BuildCommand) != ""
	if !validSiteBuildRuntime(input.BuildRuntime) || !validSiteOutputDirectory(input.OutputDirectory) || len(input.BuildCommand) > 4000 {
		return domain.SiteDeployment{}, ErrInvalidSiteSettings
	}
	if building {
		if input.SourcePath == nil || !validSiteArtifactPath(strings.TrimSpace(*input.SourcePath)) || input.SizeBytes != 0 || input.ReservedBytes <= 0 {
			return domain.SiteDeployment{}, ErrInvalidSiteSettings
		}
	} else if input.SourcePath != nil || input.ReservedBytes != 0 {
		return domain.SiteDeployment{}, ErrInvalidSiteSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.SiteDeployment{}, err
	}
	site, err := r.siteByID(ctx, tx, projectID, siteID, true)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	available := site.ArtifactQuotaBytes - site.ArtifactUsedBytes - site.ArtifactReservedBytes
	reserved := int64(0)
	if building {
		if input.ReservedBytes < available {
			reserved = input.ReservedBytes
		} else {
			reserved = available
		}
		if reserved <= 0 {
			return domain.SiteDeployment{}, ErrSiteQuotaExceeded
		}
	} else if input.SizeBytes > available {
		return domain.SiteDeployment{}, ErrSiteQuotaExceeded
	}
	var version int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM site_deployments WHERE project_id=$1 AND site_id=$2`, projectID, siteID).Scan(&version); err != nil {
		return domain.SiteDeployment{}, err
	}
	status, buildStatus := "ready", "succeeded"
	activateRequested := false
	if building {
		status, buildStatus, activateRequested = "queued", "queued", input.Activate
	}
	var sourcePath any
	if input.SourcePath != nil {
		sourcePath = strings.TrimSpace(*input.SourcePath)
	}
	var gitRepository, gitRef any
	if input.GitRepository != nil {
		gitRepository = strings.TrimSpace(*input.GitRepository)
	}
	if input.GitRef != nil {
		gitRef = strings.TrimSpace(*input.GitRef)
	}
	item, err := scanSiteDeploymentPublic(tx.QueryRow(ctx, `INSERT INTO site_deployments (id,site_id,project_id,version,source,source_name,size_bytes,archive_size_bytes,checksum_sha256,artifact_path,source_path,build_runtime,build_command,output_directory,reserved_bytes,status,build_status,activate_requested,created_by_account_id,git_repository,git_ref) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21) RETURNING `+siteDeploymentProjection, id, siteID, projectID, version, input.Source, input.SourceName, input.SizeBytes, input.ArchiveSizeBytes, input.ChecksumSHA256, input.ArtifactPath, sourcePath, input.BuildRuntime, input.BuildCommand, input.OutputDirectory, reserved, status, buildStatus, activateRequested, input.CreatedByAccountID, gitRepository, gitRef))
	if err != nil {
		return domain.SiteDeployment{}, mapError(err)
	}
	if building {
		if _, err := tx.Exec(ctx, `UPDATE project_sites SET artifact_reserved_bytes=artifact_reserved_bytes+$3,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, siteID, reserved); err != nil {
			return domain.SiteDeployment{}, err
		}
	} else if _, err := tx.Exec(ctx, `UPDATE project_sites SET artifact_used_bytes=artifact_used_bytes+$3,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, siteID, input.SizeBytes); err != nil {
		return domain.SiteDeployment{}, err
	}
	if input.Activate && !building {
		if site.Status != "active" {
			return domain.SiteDeployment{}, ErrSiteDisabled
		}
		item, err = activateSiteDeploymentTx(ctx, tx, projectID, siteID, id, item)
		if err != nil {
			return domain.SiteDeployment{}, err
		}
	}
	auditData := map[string]any{"site_id": siteID.String(), "version": item.Version, "size_bytes": input.SizeBytes, "archive_size_bytes": input.ArchiveSizeBytes, "checksum_sha256": input.ChecksumSHA256, "activated": input.Activate}
	if input.Source == "github" || input.Source == "gitlab" {
		auditData["git_repository"] = strings.TrimSpace(*input.GitRepository)
		auditData["git_ref"] = strings.TrimSpace(*input.GitRef)
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site_deployment.create", "site_deployment", id, auditData); err != nil {
		return domain.SiteDeployment{}, err
	}
	if input.Activate && !building {
		if err := r.auditSite(ctx, tx, projectID, actor, "site_deployment.activate", "site_deployment", id, map[string]any{"site_id": siteID.String(), "version": item.Version}); err != nil {
			return domain.SiteDeployment{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteDeployment{}, err
	}
	return item, nil
}

func activateSiteDeploymentTx(ctx context.Context, tx pgx.Tx, projectID, siteID, deploymentID uuid.UUID, item domain.SiteDeployment) (domain.SiteDeployment, error) {
	var current *uuid.UUID
	var status string
	if err := tx.QueryRow(ctx, `SELECT active_deployment_id,status FROM project_sites WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, siteID).Scan(&current, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.SiteDeployment{}, ErrNotFound
		}
		return domain.SiteDeployment{}, err
	}
	if status != "active" {
		return domain.SiteDeployment{}, ErrSiteDisabled
	}
	locked, _, err := (&Repository{pool: nil}).siteDeploymentByID(ctx, tx, projectID, siteID, deploymentID, true, true)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	if locked.Status == "active" && current != nil && *current == deploymentID {
		return locked, nil
	}
	if locked.Status != "ready" || locked.BuildStatus != "succeeded" {
		return domain.SiteDeployment{}, ErrInvalidSiteTransition
	}
	if current != nil && *current != deploymentID {
		if _, err := tx.Exec(ctx, `UPDATE site_deployments SET status='superseded',finished_at=now(),updated_at=now() WHERE project_id=$1 AND site_id=$2 AND id=$3 AND status='active'`, projectID, siteID, *current); err != nil {
			return domain.SiteDeployment{}, err
		}
	}
	updated, err := scanSiteDeploymentPublic(tx.QueryRow(ctx, `UPDATE site_deployments SET status='active',activated_at=COALESCE(activated_at,now()),updated_at=now() WHERE project_id=$1 AND site_id=$2 AND id=$3 RETURNING `+siteDeploymentProjection, projectID, siteID, deploymentID))
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_sites SET active_deployment_id=$3,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, siteID, deploymentID); err != nil {
		return domain.SiteDeployment{}, err
	}
	_ = item
	return updated, nil
}

func (r *Repository) ActivateSiteDeployment(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, actor SiteActor) (domain.SiteDeployment, domain.Site, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteDeployment{}, domain.Site{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.SiteDeployment{}, domain.Site{}, err
	}
	item, _, err := r.siteDeploymentByID(ctx, tx, projectID, siteID, deploymentID, false, true)
	if err != nil {
		return domain.SiteDeployment{}, domain.Site{}, err
	}
	item, err = activateSiteDeploymentTx(ctx, tx, projectID, siteID, deploymentID, item)
	if err != nil {
		return domain.SiteDeployment{}, domain.Site{}, err
	}
	site, err := r.siteByID(ctx, tx, projectID, siteID, false)
	if err != nil {
		return domain.SiteDeployment{}, domain.Site{}, err
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site_deployment.activate", "site_deployment", deploymentID, map[string]any{"site_id": siteID.String(), "version": item.Version}); err != nil {
		return domain.SiteDeployment{}, domain.Site{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteDeployment{}, domain.Site{}, err
	}
	return item, site, nil
}

func siteDeploymentStoragePaths(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, siteID, deploymentID uuid.UUID) (string, string, error) {
	var sourcePath *string
	var artifactPath string
	err := query.QueryRow(ctx, `SELECT source_path,artifact_path FROM site_deployments WHERE project_id=$1 AND site_id=$2 AND id=$3`, projectID, siteID, deploymentID).Scan(&sourcePath, &artifactPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if sourcePath == nil {
		return "", artifactPath, nil
	}
	return *sourcePath, artifactPath, nil
}

// ClaimNextSiteDeployment leases one source deployment for the trusted Sites
// builder. The row remains queued while the immutable public directory is
// being produced; the active pointer is changed only after a successful
// build.
func (r *Repository) ClaimNextSiteDeployment(ctx context.Context, workerID string) (SiteBuildJob, error) {
	if !validFunctionWorkerID(workerID) {
		return SiteBuildJob{}, ErrInvalidSiteSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return SiteBuildJob{}, err
	}
	defer tx.Rollback(ctx)
	var deploymentID, projectID, siteID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT d.id,d.project_id,d.site_id
		FROM site_deployments d
		JOIN project_sites s ON s.id=d.site_id AND s.project_id=d.project_id
		WHERE d.source_path IS NOT NULL
		  AND d.build_command <> ''
		  AND d.status='queued'
		  AND d.build_status IN ('queued','deferred')
		ORDER BY d.queued_at,d.id
		LIMIT 1
		FOR UPDATE OF d SKIP LOCKED`).Scan(&deploymentID, &projectID, &siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteBuildJob{}, ErrNoSiteDeploymentJob
	}
	if err != nil {
		return SiteBuildJob{}, err
	}
	site, err := r.siteByID(ctx, tx, projectID, siteID, true)
	if err != nil {
		return SiteBuildJob{}, err
	}
	deployment, sourcePath, artifactPath, err := r.siteDeploymentByIDWithPaths(ctx, tx, projectID, siteID, deploymentID, true)
	if err != nil {
		return SiteBuildJob{}, err
	}
	if deployment.BuildStatus != "queued" && deployment.BuildStatus != "deferred" || strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(deployment.BuildCommand) == "" {
		return SiteBuildJob{}, ErrNoSiteDeploymentJob
	}
	updated, err := scanSiteDeploymentPublic(tx.QueryRow(ctx, `UPDATE site_deployments SET build_status='running',build_started_at=now(),build_worker_id=$4,updated_at=now() WHERE project_id=$1 AND site_id=$2 AND id=$3 AND build_status IN ('queued','deferred') AND status='queued' RETURNING `+siteDeploymentProjection, projectID, siteID, deploymentID, workerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteBuildJob{}, ErrNoSiteDeploymentJob
	}
	if err != nil {
		return SiteBuildJob{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "site_deployment.updated", "site_deployment", deploymentID, map[string]any{"site_id": siteID.String(), "status": updated.Status, "build_status": updated.BuildStatus}); err != nil {
		return SiteBuildJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteBuildJob{}, err
	}
	return SiteBuildJob{Site: site, Deployment: updated, SourcePath: sourcePath, ArtifactPath: artifactPath}, nil
}

// RequeueStaleSiteDeployments releases leases held by a crashed Sites worker.
func (r *Repository) RequeueStaleSiteDeployments(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, ErrInvalidSiteSettings
	}
	result, err := r.pool.Exec(ctx, `UPDATE site_deployments SET build_status='deferred',build_started_at=NULL,build_worker_id=NULL,updated_at=now() WHERE build_status='running' AND build_started_at IS NOT NULL AND build_started_at < now() - ($1::double precision * interval '1 second')`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// CompleteSiteDeploymentBuild commits the worker-produced directory and
// atomically moves its reservation into expanded-byte quota. If activation
// was requested at upload time, the new deployment becomes active in the
// same transaction as the build result.
func (r *Repository) CompleteSiteDeploymentBuild(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, workerID, buildChecksumSHA256 string, buildSizeBytes int64) (domain.SiteDeployment, error) {
	if !validFunctionWorkerID(workerID) || buildSizeBytes < 0 || !validSiteSHA256(buildChecksumSHA256) {
		return domain.SiteDeployment{}, ErrInvalidSiteSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	defer tx.Rollback(ctx)
	site, err := r.siteByID(ctx, tx, projectID, siteID, true)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	item, _, _, err := r.siteDeploymentByIDWithPaths(ctx, tx, projectID, siteID, deploymentID, true)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	var currentWorker *string
	if err := tx.QueryRow(ctx, `SELECT build_worker_id FROM site_deployments WHERE project_id=$1 AND site_id=$2 AND id=$3 FOR UPDATE`, projectID, siteID, deploymentID).Scan(&currentWorker); err != nil {
		return domain.SiteDeployment{}, err
	}
	if item.BuildStatus != "running" || currentWorker == nil || *currentWorker != workerID {
		return domain.SiteDeployment{}, ErrSiteBuildNotAvailable
	}
	remainingAfterReservation := site.ArtifactQuotaBytes - site.ArtifactUsedBytes - (site.ArtifactReservedBytes - item.ReservedBytes)
	if item.ReservedBytes <= 0 || buildSizeBytes > item.ReservedBytes || buildSizeBytes > remainingAfterReservation {
		return domain.SiteDeployment{}, ErrSiteQuotaExceeded
	}
	active := item.ActivateRequested && site.Status == "active"
	if active && site.ActiveDeploymentID != nil && *site.ActiveDeploymentID != deploymentID.String() {
		if _, err := tx.Exec(ctx, `UPDATE site_deployments SET status='superseded',finished_at=now(),updated_at=now() WHERE project_id=$1 AND site_id=$2 AND id=$3 AND status='active'`, projectID, siteID, *site.ActiveDeploymentID); err != nil {
			return domain.SiteDeployment{}, err
		}
	}
	status := "ready"
	if active {
		status = "active"
	}
	if _, err := tx.Exec(ctx, `UPDATE project_sites SET artifact_used_bytes=artifact_used_bytes+$3,artifact_reserved_bytes=GREATEST(0,artifact_reserved_bytes-$4),active_deployment_id=CASE WHEN $5 THEN $6 ELSE active_deployment_id END,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, siteID, buildSizeBytes, item.ReservedBytes, active, deploymentID); err != nil {
		return domain.SiteDeployment{}, err
	}
	updated, err := scanSiteDeploymentPublic(tx.QueryRow(ctx, `UPDATE site_deployments SET size_bytes=$4,checksum_sha256=$5,status=$6,build_status='succeeded',build_worker_id=NULL,error_message=NULL,reserved_bytes=0,built_at=COALESCE(built_at,now()),activated_at=CASE WHEN $6='active' THEN COALESCE(activated_at,now()) ELSE activated_at END,activate_requested=false,updated_at=now() WHERE project_id=$1 AND site_id=$2 AND id=$3 RETURNING `+siteDeploymentProjection, projectID, siteID, deploymentID, buildSizeBytes, strings.ToLower(buildChecksumSHA256), status))
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "site_deployment.updated", "site_deployment", deploymentID, map[string]any{"site_id": siteID.String(), "status": updated.Status, "build_status": updated.BuildStatus}); err != nil {
		return domain.SiteDeployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteDeployment{}, err
	}
	return updated, nil
}

// FailSiteDeploymentBuild records a bounded failure and releases the quota
// reservation so a replacement deployment can be uploaded.
func (r *Repository) FailSiteDeploymentBuild(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, workerID, errorMessage string) (domain.SiteDeployment, error) {
	if !validFunctionWorkerID(workerID) {
		return domain.SiteDeployment{}, ErrInvalidSiteSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	defer tx.Rollback(ctx)
	_, err = r.siteByID(ctx, tx, projectID, siteID, true)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	item, _, _, err := r.siteDeploymentByIDWithPaths(ctx, tx, projectID, siteID, deploymentID, true)
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	var currentWorker *string
	if err := tx.QueryRow(ctx, `SELECT build_worker_id FROM site_deployments WHERE project_id=$1 AND site_id=$2 AND id=$3 FOR UPDATE`, projectID, siteID, deploymentID).Scan(&currentWorker); err != nil {
		return domain.SiteDeployment{}, err
	}
	if item.BuildStatus != "running" || currentWorker == nil || *currentWorker != workerID {
		return domain.SiteDeployment{}, ErrSiteBuildNotAvailable
	}
	failure := normalizeSiteBuildError(errorMessage)
	if _, err := tx.Exec(ctx, `UPDATE project_sites SET artifact_reserved_bytes=GREATEST(0,artifact_reserved_bytes-$3),updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, siteID, item.ReservedBytes); err != nil {
		return domain.SiteDeployment{}, err
	}
	updated, err := scanSiteDeploymentPublic(tx.QueryRow(ctx, `UPDATE site_deployments SET status='failed',build_status='failed',build_worker_id=NULL,error_message=$4,reserved_bytes=0,finished_at=now(),updated_at=now() WHERE project_id=$1 AND site_id=$2 AND id=$3 RETURNING `+siteDeploymentProjection, projectID, siteID, deploymentID, failure))
	if err != nil {
		return domain.SiteDeployment{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "site_deployment.updated", "site_deployment", deploymentID, map[string]any{"site_id": siteID.String(), "status": updated.Status, "build_status": updated.BuildStatus, "has_error": true}); err != nil {
		return domain.SiteDeployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteDeployment{}, err
	}
	return updated, nil
}

func normalizeSiteBuildError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "site build failed"
	}
	if len(value) > 4000 {
		value = value[:4000]
	}
	return value
}

func normalizeSiteBuildLogMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 16000 {
		value = value[:16000]
	}
	return value
}
