package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
)

// ResetAppHealthBeforeRuntimeRestart withdraws route eligibility before the
// worker starts an exited managed container. The runtime lease and current
// desired state fence the reset so a stale worker cannot invalidate or
// authorize another generation.
func (r *Repository) ResetAppHealthBeforeRuntimeRestart(ctx context.Context, job AppRuntimeJob, identity uuid.UUID) error {
	return r.rotateAppRuntimeIdentity(ctx, job, identity, false, true)
}

// RotateAppRuntimeIdentityBeforeCreate gives each newly created runtime a
// distinct DNS target after the previous managed container has been removed.
func (r *Repository) RotateAppRuntimeIdentityBeforeCreate(ctx context.Context, job AppRuntimeJob, identity uuid.UUID) error {
	return r.rotateAppRuntimeIdentity(ctx, job, identity, true, false)
}

func (r *Repository) rotateAppRuntimeIdentity(ctx context.Context, job AppRuntimeJob, identity uuid.UUID, clearContainer, restart bool) error {
	if r == nil || r.pool == nil || validateRuntimeJob(job) != nil || !job.App.Enabled || job.App.DesiredDeploymentID == nil || identity == uuid.Nil || identity == job.RouteIdentity {
		return ErrInvalidAppRuntimeJob
	}
	appID := uuid.MustParse(job.App.ID)
	containerName := AppRuntimeContainerNameForIncarnation(appID, identity)
	if containerName == "" {
		return ErrInvalidAppRuntimeJob
	}
	delay := 0
	if restart && job.App.Workload.HealthCheck.InitialDelaySeconds != nil {
		delay = *job.App.Workload.HealthCheck.InitialDelaySeconds
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	projectID := uuid.MustParse(job.App.ProjectID)
	current, err := appByID(ctx, tx, projectID, appID, true)
	if errors.Is(err, ErrNotFound) {
		return ErrAppRuntimeLeaseLost
	}
	if err != nil {
		return err
	}
	var owner string
	var token uuid.UUID
	var healthStatus string
	var healthCheckedAt *time.Time
	var containerAddress *string
	var routeIdentity uuid.UUID
	var currentContainerName string
	if err := tx.QueryRow(ctx, `
		SELECT worker_id,lease_token,health_status,health_checked_at,host(container_address),route_identity,container_name
		FROM app_runtime_state WHERE app_id=$1 AND lease_expires_at>now() FOR UPDATE`, appID).Scan(
		&owner, &token, &healthStatus, &healthCheckedAt, &containerAddress, &routeIdentity, &currentContainerName,
	); errors.Is(err, pgx.ErrNoRows) {
		return ErrAppRuntimeLeaseLost
	} else if err != nil {
		return err
	}
	if owner != job.WorkerID || token != job.LeaseToken || routeIdentity != job.RouteIdentity || currentContainerName != job.ContainerName {
		return ErrAppRuntimeLeaseLost
	}
	if !runtimeDesiredStateMatches(current, job.App) {
		if _, err := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,next_inspection_at=now(),updated_at=now() WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`, appID, job.WorkerID, job.LeaseToken); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrAppRuntimeStale
	}
	containerReset := ""
	if clearContainer {
		containerReset = `container_id=NULL,image_id=NULL,image_digest=NULL,runtime_tag=NULL,
			applied_deployment_id=NULL,applied_workload_spec_sha256=NULL,applied_generation=NULL,`
	}
	result, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET route_identity=$4,container_name=$5,
		    health_status='pending',health_generation=NULL,health_deployment_id=NULL,health_container_id=NULL,health_route_identity=NULL,
		    health_failure_count=0,health_checked_at=NULL,
		    next_health_check_at=CASE WHEN $6 THEN now()+($7::double precision*interval '1 second') ELSE NULL END,
		    `+containerReset+`container_address=NULL,last_inspected_at=NULL,next_inspection_at=now(),updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3 AND lease_expires_at>now()
		  AND route_identity=$8 AND container_name=$9`,
		appID, job.WorkerID, job.LeaseToken, identity, containerName, restart, delay, job.RouteIdentity, job.ContainerName)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	if healthStatus != "pending" || healthCheckedAt != nil || containerAddress != nil || routeIdentity != identity {
		if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
			"runtime_status": current.RuntimeStatus, "health_status": "pending",
			"route_status":       appRouteStatus(current, "pending"),
			"desired_generation": current.DesiredGeneration, "observed_generation": current.ObservedGeneration,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// CompleteAppRuntime records convergence only after the caller has verified
// actual Docker state. Expected desired fields and the lease token fence stale
// work from newer App edits or reclaimed leases.
func (r *Repository) CompleteAppRuntime(ctx context.Context, job AppRuntimeJob, status string, container *AppRuntimeContainer) error {
	if r == nil || r.pool == nil || validateRuntimeJob(job) != nil {
		return ErrInvalidAppRuntimeJob
	}
	if status != "running" && status != "stopped" && status != "not_deployed" {
		return ErrInvalidAppRuntimeJob
	}
	if (status == "running") != (container != nil) ||
		(status == "running" && (!job.App.Enabled || job.App.DesiredDeploymentID == nil || job.Deployment.ID == "")) ||
		(status == "stopped" && job.App.Enabled) ||
		(status == "not_deployed" && (!job.App.Enabled || job.App.DesiredDeploymentID != nil)) {
		return ErrInvalidAppRuntimeJob
	}
	if container != nil && (!validRuntimeContainerID(container.ID) || container.Name != job.ContainerName || !validRuntimeImageID(container.ImageID) || !validRuntimeDigest(container.ImageDigest) || len(container.RuntimeTag) > 255 || !validPrivateRuntimeAddress(container.Address)) {
		return ErrInvalidAppRuntimeJob
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	appID := uuid.MustParse(job.App.ID)
	projectID := uuid.MustParse(job.App.ProjectID)
	current, err := appByID(ctx, tx, projectID, appID, true)
	if errors.Is(err, ErrNotFound) {
		return ErrAppRuntimeLeaseLost
	}
	if err != nil {
		return err
	}
	var owner string
	var currentToken uuid.UUID
	var oldContainerID, oldAddress, oldAppliedSpec *string
	var oldAppliedGeneration *int64
	var oldAppliedDeploymentID *uuid.UUID
	var oldHealthStatus string
	var currentRouteIdentity uuid.UUID
	var currentContainerName string
	if err := tx.QueryRow(ctx, `SELECT worker_id,lease_token,container_id,host(container_address),applied_generation,applied_deployment_id,applied_workload_spec_sha256,health_status,route_identity,container_name FROM app_runtime_state WHERE app_id=$1 AND lease_expires_at>now() FOR UPDATE`, appID).Scan(
		&owner, &currentToken, &oldContainerID, &oldAddress, &oldAppliedGeneration, &oldAppliedDeploymentID, &oldAppliedSpec, &oldHealthStatus, &currentRouteIdentity, &currentContainerName,
	); errors.Is(err, pgx.ErrNoRows) {
		return ErrAppRuntimeLeaseLost
	} else if err != nil {
		return err
	}
	if owner != job.WorkerID || currentToken != job.LeaseToken {
		return ErrAppRuntimeLeaseLost
	}
	if !runtimeDesiredStateMatches(current, job.App) || currentRouteIdentity != job.RouteIdentity || currentContainerName != job.ContainerName {
		if _, err := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,next_inspection_at=now(),updated_at=now() WHERE app_id=$1 AND lease_token=$2`, appID, job.LeaseToken); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrAppRuntimeStale
	}
	identityChanged := status != "running" || oldContainerID == nil || container == nil || *oldContainerID != container.ID ||
		currentRouteIdentity != job.RouteIdentity ||
		oldAppliedGeneration == nil || *oldAppliedGeneration != job.App.DesiredGeneration ||
		oldAppliedDeploymentID == nil || job.App.DesiredDeploymentID == nil || *oldAppliedDeploymentID != uuid.MustParse(*job.App.DesiredDeploymentID) ||
		oldAppliedSpec == nil || *oldAppliedSpec != job.App.WorkloadSpecSHA256 ||
		oldAddress == nil || container == nil || *oldAddress != container.Address
	result, err := tx.Exec(ctx, `
		UPDATE project_apps
		SET observed_generation=desired_generation,runtime_status=$7,runtime_error=NULL,updated_at=now()
		WHERE project_id=$1 AND id=$2 AND desired_generation=$3
		  AND desired_deployment_id IS NOT DISTINCT FROM $4::uuid
		  AND workload_spec_sha256=$5 AND enabled=$6`,
		projectID, appID, job.App.DesiredGeneration, optionalUUID(job.App.DesiredDeploymentID), job.App.WorkloadSpecSHA256, job.App.Enabled, status)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		if _, releaseErr := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,next_inspection_at=now(),updated_at=now() WHERE app_id=$1 AND lease_token=$2`, appID, job.LeaseToken); releaseErr != nil {
			return releaseErr
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrAppRuntimeStale
	}
	var containerID, imageID, imageDigest, runtimeTag any
	containerName := job.ContainerName
	if container != nil {
		containerID, imageID, imageDigest, runtimeTag = container.ID, container.ImageID, container.ImageDigest, container.RuntimeTag
	}
	var containerAddress any
	var healthDelaySeconds any
	if container != nil {
		containerAddress = container.Address
	}
	if identityChanged && status == "running" {
		delay := 0
		if job.App.Workload.HealthCheck.InitialDelaySeconds != nil {
			delay = *job.App.Workload.HealthCheck.InitialDelaySeconds
		}
		healthDelaySeconds = delay
	}
	stateResult, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET container_id=$4,container_name=$5,image_id=$6,image_digest=$7,runtime_tag=$8,
		    applied_deployment_id=$9,applied_workload_spec_sha256=$10,applied_generation=$11,
		    container_address=$17::inet,
		    health_status=CASE WHEN $14 THEN 'pending' ELSE health_status END,
		    health_generation=CASE WHEN $14 AND $15='running' THEN $11 ELSE CASE WHEN $14 THEN NULL ELSE health_generation END END,
		    health_deployment_id=CASE WHEN $14 AND $15='running' THEN $9 ELSE CASE WHEN $14 THEN NULL ELSE health_deployment_id END END,
		    health_container_id=CASE WHEN $14 AND $15='running' THEN $4 ELSE CASE WHEN $14 THEN NULL ELSE health_container_id END END,
		    health_route_identity=CASE WHEN $14 AND $15='running' THEN route_identity ELSE CASE WHEN $14 THEN NULL ELSE health_route_identity END END,
		    health_failure_count=CASE WHEN $14 THEN 0 ELSE health_failure_count END,
		    health_checked_at=CASE WHEN $14 THEN NULL ELSE health_checked_at END,
		    next_health_check_at=CASE WHEN $14 AND $15='running' THEN now()+($16::double precision*interval '1 second') WHEN $14 THEN NULL ELSE next_health_check_at END,
		    stop_grace_period_seconds=$12,failure_count=0,next_retry_at=NULL,last_failure_at=NULL,
		    last_inspected_at=now(),next_inspection_at=now()+($13::double precision*interval '1 second'),
		    last_transition_at=CASE WHEN $14 THEN now() ELSE last_transition_at END,
		    last_started_at=CASE WHEN $14 AND $15='running' THEN now() ELSE last_started_at END,
		    last_stopped_at=CASE WHEN $14 AND $15<>'running' THEN now() ELSE last_stopped_at END,
		    worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3 AND route_identity=$18 AND container_name=$19`,
		appID, job.WorkerID, job.LeaseToken, containerID, containerName, imageID, imageDigest, runtimeTag,
		optionalUUID(job.App.DesiredDeploymentID), job.App.WorkloadSpecSHA256, job.App.DesiredGeneration,
		job.App.Workload.StopGracePeriodSeconds, AppRuntimeDriftInterval.Seconds(),
		identityChanged, status, healthDelaySeconds, containerAddress, job.RouteIdentity, job.ContainerName)
	if err != nil {
		return err
	}
	if stateResult.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	if status == "running" && container != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_runtime_log_sources (app_id,project_id,container_id,first_seen_at,last_seen_at)
			VALUES ($1,$2,$3,now(),now())
			ON CONFLICT (app_id,container_id) DO UPDATE
			SET last_seen_at=GREATEST(app_runtime_log_sources.last_seen_at,EXCLUDED.last_seen_at),updated_at=now()`,
			appID, projectID, container.ID); err != nil {
			return err
		}
	}
	if current.RuntimeStatus != status || current.RuntimeError != nil || current.ObservedGeneration != job.App.DesiredGeneration {
		if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
			"runtime_status": status, "desired_generation": job.App.DesiredGeneration,
			"observed_generation": job.App.DesiredGeneration,
		}); err != nil {
			return err
		}
	}
	if identityChanged && (oldHealthStatus != "pending" || oldAddress == nil || container == nil || (oldAddress != nil && container != nil && *oldAddress != container.Address)) {
		if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
			"runtime_status": status, "health_status": "pending", "desired_generation": job.App.DesiredGeneration,
			"observed_generation": job.App.DesiredGeneration,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// FailAppRuntime records a retryable safe summary and deliberately leaves
// observed_generation untouched.
func (r *Repository) FailAppRuntime(ctx context.Context, job AppRuntimeJob, status, message string, retryAt time.Time) error {
	if r == nil || r.pool == nil || validateRuntimeJob(job) != nil || (status != "failed" && status != "degraded") || retryAt.IsZero() {
		return ErrInvalidAppRuntimeJob
	}
	message = safeAppRuntimeError(message)
	if message == "" {
		return ErrInvalidAppRuntimeJob
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	appID := uuid.MustParse(job.App.ID)
	projectID := uuid.MustParse(job.App.ProjectID)
	current, err := appByID(ctx, tx, projectID, appID, true)
	if errors.Is(err, ErrNotFound) {
		return ErrAppRuntimeLeaseLost
	}
	if err != nil {
		return err
	}
	var owner string
	var token uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT worker_id,lease_token FROM app_runtime_state WHERE app_id=$1 AND lease_expires_at>now() FOR UPDATE`, appID).Scan(&owner, &token); errors.Is(err, pgx.ErrNoRows) {
		return ErrAppRuntimeLeaseLost
	} else if err != nil {
		return err
	}
	if owner != job.WorkerID || token != job.LeaseToken {
		return ErrAppRuntimeLeaseLost
	}
	if !runtimeDesiredStateMatches(current, job.App) {
		if _, err := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,next_inspection_at=now(),updated_at=now() WHERE app_id=$1 AND lease_token=$2`, appID, job.LeaseToken); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrAppRuntimeStale
	}
	if _, err := tx.Exec(ctx, `
		UPDATE project_apps SET runtime_status=$5,runtime_error=$6,updated_at=now()
		WHERE project_id=$1 AND id=$2 AND desired_generation=$3
		  AND desired_deployment_id IS NOT DISTINCT FROM $4::uuid`,
		projectID, appID, job.App.DesiredGeneration, optionalUUID(job.App.DesiredDeploymentID), status, message); err != nil {
		return err
	}
	stateResult, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET failure_count=failure_count+1,next_retry_at=$4,last_failure_at=now(),last_inspected_at=now(),
		    next_inspection_at=$4,worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`, appID, job.WorkerID, job.LeaseToken, retryAt.UTC())
	if err != nil {
		return err
	}
	if stateResult.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	if current.RuntimeStatus != status || current.RuntimeError == nil || *current.RuntimeError != message {
		if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
			"runtime_status": status, "desired_generation": job.App.DesiredGeneration,
			"observed_generation": current.ObservedGeneration,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AppRuntimeContainerExists protects the exact persisted container ID, even
// while its name is being rotated, and containers under an active runtime
// lease. Different App containers remain eligible for orphan cleanup.
func (r *Repository) AppRuntimeContainerExists(ctx context.Context, projectID, appID uuid.UUID, containerID string) (bool, error) {
	if r == nil || r.pool == nil || projectID == uuid.Nil || appID == uuid.Nil || !validRuntimeContainerID(containerID) {
		return false, ErrInvalidAppRuntimeJob
	}
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM project_apps app
		  JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		  WHERE app.project_id=$1 AND app.id=$2
		    AND (runtime.container_id=$3 OR (runtime.lease_token IS NOT NULL AND runtime.lease_expires_at>now()))
		)`, projectID, appID, containerID).Scan(&exists)
	return exists, err
}
