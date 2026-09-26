package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
)

// ScheduleAppRuntimeStartupSweep makes last-observed runtime state advisory
// after every worker restart, including when generations already matched.
func (r *Repository) ScheduleAppRuntimeStartupSweep(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return ErrInvalidAppRuntimeJob
	}
	if _, err := r.pool.Exec(ctx, `
		WITH missing AS (
		  SELECT id,project_id,gen_random_uuid() AS route_identity FROM project_apps
		)
		INSERT INTO app_runtime_state (app_id,project_id,route_identity,container_name)
		SELECT id,project_id,route_identity,
		       'st-'||replace(id::text,'-','')||'-'||substring(replace(route_identity::text,'-','') FROM 1 FOR 24)
		FROM missing
		ON CONFLICT (app_id) DO UPDATE SET
		  next_inspection_at=now(),
		  next_health_check_at=CASE
		    WHEN app_runtime_state.health_status='pending' AND app_runtime_state.health_checked_at IS NULL
		      THEN app_runtime_state.next_health_check_at
		    ELSE now()
		  END,
		  updated_at=now()`); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE app_runtime_state
		SET container_name='st-'||replace(app_id::text,'-','')||'-'||substring(replace(route_identity::text,'-','') FROM 1 FOR 24)
		WHERE container_name IS NULL`)
	return err
}

// RequeueStaleAppRuntimeLeases releases expired ownership tokens. A worker
// that wakes later cannot finalize because every write is fenced by the token.
func (r *Repository) RequeueStaleAppRuntimeLeases(ctx context.Context) (int64, error) {
	if r == nil || r.pool == nil {
		return 0, ErrInvalidAppRuntimeJob
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE app_runtime_state
		SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,
		    next_inspection_at=LEAST(next_inspection_at,now()),updated_at=now()
		WHERE lease_token IS NOT NULL AND lease_expires_at<=now()`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// ClaimNextAppRuntime uses a short transaction to lease one App, then returns
// the current desired App and selected immutable deployment. Docker work is
// always performed after this transaction commits.
func (r *Repository) ClaimNextAppRuntime(ctx context.Context, workerID string, leaseAge time.Duration) (AppRuntimeJob, error) {
	if r == nil || r.pool == nil || !validFunctionWorkerID(workerID) || leaseAge < 15*time.Second || leaseAge > 10*time.Minute {
		return AppRuntimeJob{}, ErrInvalidAppRuntimeJob
	}
	token, err := uuid.NewV7()
	if err != nil {
		return AppRuntimeJob{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AppRuntimeJob{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		WITH missing AS (
		  SELECT id,project_id,gen_random_uuid() AS route_identity FROM project_apps
		)
		INSERT INTO app_runtime_state (app_id,project_id,route_identity,container_name)
		SELECT id,project_id,route_identity,
		       'st-'||replace(id::text,'-','')||'-'||substring(replace(route_identity::text,'-','') FROM 1 FOR 24)
		FROM missing
		ON CONFLICT (app_id) DO NOTHING`); err != nil {
		return AppRuntimeJob{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET container_name='st-'||replace(app_id::text,'-','')||'-'||substring(replace(route_identity::text,'-','') FROM 1 FOR 24)
		WHERE container_name IS NULL`); err != nil {
		return AppRuntimeJob{}, err
	}
	var projectID, appID uuid.UUID
	var failureCount int
	err = tx.QueryRow(ctx, `
		SELECT app.project_id,app.id,runtime.failure_count
		FROM project_apps app
		JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		WHERE (runtime.lease_token IS NULL OR runtime.lease_expires_at<=now())
		  AND (runtime.next_retry_at IS NULL OR runtime.next_retry_at<=now())
		  AND (
		    app.desired_generation<>app.observed_generation OR
		    (app.enabled AND app.desired_deployment_id IS NOT NULL AND app.runtime_status<>'running') OR
		    (NOT app.enabled AND app.runtime_status<>'stopped') OR
		    (app.enabled AND app.desired_deployment_id IS NULL AND app.runtime_status<>'not_deployed') OR
		    runtime.last_inspected_at IS NULL OR runtime.next_inspection_at<=now()
		  )
		ORDER BY
		  CASE WHEN app.desired_generation<>app.observed_generation THEN 0
		       WHEN runtime.last_inspected_at IS NULL THEN 1 ELSE 2 END,
		  runtime.next_inspection_at,app.updated_at,app.id
		FOR UPDATE OF app,runtime SKIP LOCKED
		LIMIT 1`).Scan(&projectID, &appID, &failureCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppRuntimeJob{}, ErrNoAppRuntimeJob
	}
	if err != nil {
		return AppRuntimeJob{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET worker_id=$2,lease_token=$3,lease_expires_at=now()+($4::double precision*interval '1 second'),
		    updated_at=now()
		WHERE app_id=$1`, appID, workerID, token, leaseAge.Seconds()); err != nil {
		return AppRuntimeJob{}, err
	}
	var routeIdentity uuid.UUID
	var containerName string
	if err := tx.QueryRow(ctx, `SELECT route_identity,container_name FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&routeIdentity, &containerName); err != nil {
		return AppRuntimeJob{}, err
	}
	app, err := appByID(ctx, tx, projectID, appID, false)
	if err != nil {
		return AppRuntimeJob{}, err
	}
	shouldMarkPending := app.DesiredGeneration != app.ObservedGeneration &&
		((app.Enabled && app.DesiredDeploymentID != nil) || !app.Enabled)
	if shouldMarkPending && (app.RuntimeStatus != "pending" || app.RuntimeError != nil) {
		if _, err := tx.Exec(ctx, `UPDATE project_apps SET runtime_status='pending',runtime_error=NULL,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, appID); err != nil {
			return AppRuntimeJob{}, err
		}
		if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
			"runtime_status": "pending", "desired_generation": app.DesiredGeneration,
		}); err != nil {
			return AppRuntimeJob{}, err
		}
		app.RuntimeStatus = "pending"
		app.RuntimeError = nil
	}
	job := AppRuntimeJob{
		App: app, RouteIdentity: routeIdentity, ContainerName: containerName,
		WorkerID: workerID, LeaseToken: token, FailureCount: failureCount,
	}
	if app.DesiredDeploymentID != nil {
		deploymentID, parseErr := uuid.Parse(*app.DesiredDeploymentID)
		if parseErr != nil {
			return AppRuntimeJob{}, ErrInvalidAppRuntimeJob
		}
		deployment, _, imagePath, _, _, _, _, deploymentErr := appDeploymentByID(ctx, tx, projectID, appID, deploymentID, false, true)
		if deploymentErr != nil {
			return AppRuntimeJob{}, deploymentErr
		}
		job.Deployment = deployment
		if imagePath != nil {
			job.ImagePath = *imagePath
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AppRuntimeJob{}, err
	}
	return job, nil
}

// RenewAppRuntimeLease extends only the exact claim token that began the
// current reconcile operation.
func (r *Repository) RenewAppRuntimeLease(ctx context.Context, appID uuid.UUID, workerID string, token uuid.UUID, leaseAge time.Duration) error {
	if r == nil || r.pool == nil || appID == uuid.Nil || token == uuid.Nil || !validFunctionWorkerID(workerID) || leaseAge < 15*time.Second || leaseAge > 10*time.Minute {
		return ErrInvalidAppRuntimeJob
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE app_runtime_state SET lease_expires_at=now()+($4::double precision*interval '1 second'),updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3 AND lease_expires_at>now()`, appID, workerID, token, leaseAge.Seconds())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	return nil
}

// IsAppRuntimeJobCurrent checks fencing and desired-state identity before
// irreversible container actions such as replacing or starting a container.
func (r *Repository) IsAppRuntimeJobCurrent(ctx context.Context, job AppRuntimeJob) (bool, error) {
	if r == nil || r.pool == nil || validateRuntimeJob(job) != nil {
		return false, ErrInvalidAppRuntimeJob
	}
	var current bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM project_apps app
		  JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		  WHERE app.project_id=$1 AND app.id=$2 AND app.enabled=$3
		    AND app.desired_generation=$4
		    AND app.desired_deployment_id IS NOT DISTINCT FROM $5::uuid
		    AND app.workload_spec_sha256=$6
		    AND runtime.worker_id=$7 AND runtime.lease_token=$8 AND runtime.lease_expires_at>now()
		    AND runtime.route_identity=$9 AND runtime.container_name=$10
		)`, uuid.MustParse(job.App.ProjectID), uuid.MustParse(job.App.ID), job.App.Enabled,
		job.App.DesiredGeneration, optionalUUID(job.App.DesiredDeploymentID), job.App.WorkloadSpecSHA256,
		job.WorkerID, job.LeaseToken, job.RouteIdentity, job.ContainerName).Scan(&current)
	return current, err
}

// ReleaseAppRuntimeJob makes a stale or cancelled claim immediately eligible
// for a fresh desired-state read without recording a runtime failure.
func (r *Repository) ReleaseAppRuntimeJob(ctx context.Context, job AppRuntimeJob) error {
	if r == nil || r.pool == nil || validateRuntimeJob(job) != nil {
		return ErrInvalidAppRuntimeJob
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE app_runtime_state
		SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,next_inspection_at=now(),updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`, uuid.MustParse(job.App.ID), job.WorkerID, job.LeaseToken)
	return err
}
