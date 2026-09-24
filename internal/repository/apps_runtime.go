package repository

// App runtime persistence is the private durable coordination seam between
// PostgreSQL desired state and the trusted Moby reconciler. Docker IDs and
// image paths in these projections never enter the normal App API response.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	AppRuntimeDriftInterval = 30 * time.Second
	AppRuntimeMaxErrorBytes = 512
)

var (
	ErrNoAppRuntimeJob      = errors.New("no App runtime job available")
	ErrNoAppRuntimeCleanup  = errors.New("no App runtime cleanup job available")
	ErrAppRuntimeLeaseLost  = errors.New("App runtime lease is no longer owned by this worker")
	ErrAppRuntimeStale      = errors.New("App desired state changed during runtime reconciliation")
	ErrInvalidAppRuntimeJob = errors.New("invalid App runtime job")
)

// AppRuntimeJob is a trusted worker projection. ImagePath is private and is
// read only from the immutable selected AppDeployment.
type AppRuntimeJob struct {
	App          domain.App
	Deployment   domain.AppDeployment
	ImagePath    string
	WorkerID     string
	LeaseToken   uuid.UUID
	FailureCount int
}

// AppRuntimeContainer is the private identity confirmed by Docker inspect.
type AppRuntimeContainer struct {
	ID          string
	Name        string
	ImageID     string
	ImageDigest string
	RuntimeTag  string
}

// AppRuntimeCleanupJob survives deletion of its App and project rows. The
// runtime worker still verifies ownership labels before acting on its target.
type AppRuntimeCleanupJob struct {
	ID                  uuid.UUID
	ProjectID           *uuid.UUID
	AppID               uuid.UUID
	ContainerID         *string
	ContainerName       string
	StopGracePeriodSecs int
	WorkerID            string
	LeaseToken          uuid.UUID
	AttemptCount        int
}

func AppRuntimeContainerName(appID uuid.UUID) string {
	return "stealth-app-" + strings.ReplaceAll(appID.String(), "-", "")
}

// ScheduleAppRuntimeStartupSweep makes last-observed runtime state advisory
// after every worker restart, including when generations already matched.
func (r *Repository) ScheduleAppRuntimeStartupSweep(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return ErrInvalidAppRuntimeJob
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO app_runtime_state (app_id,project_id,container_name)
		SELECT id,project_id,'stealth-app-'||replace(id::text,'-','') FROM project_apps
		ON CONFLICT (app_id) DO UPDATE SET next_inspection_at=now(),updated_at=now()`)
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
		INSERT INTO app_runtime_state (app_id,project_id,container_name)
		SELECT id,project_id,'stealth-app-'||replace(id::text,'-','') FROM project_apps
		ON CONFLICT (app_id) DO NOTHING`); err != nil {
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
		    container_name=COALESCE(container_name,'stealth-app-'||replace(app_id::text,'-','')),
		    updated_at=now()
		WHERE app_id=$1`, appID, workerID, token, leaseAge.Seconds()); err != nil {
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
	job := AppRuntimeJob{App: app, WorkerID: workerID, LeaseToken: token, FailureCount: failureCount}
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
		)`, uuid.MustParse(job.App.ProjectID), uuid.MustParse(job.App.ID), job.App.Enabled,
		job.App.DesiredGeneration, optionalUUID(job.App.DesiredDeploymentID), job.App.WorkloadSpecSHA256,
		job.WorkerID, job.LeaseToken).Scan(&current)
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
	if container != nil && (!validRuntimeContainerID(container.ID) || container.Name != AppRuntimeContainerName(uuid.MustParse(job.App.ID)) || !validRuntimeImageID(container.ImageID) || !validRuntimeDigest(container.ImageDigest) || len(container.RuntimeTag) > 255) {
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
	if err := tx.QueryRow(ctx, `SELECT worker_id,lease_token FROM app_runtime_state WHERE app_id=$1 AND lease_expires_at>now() FOR UPDATE`, appID).Scan(&owner, &currentToken); errors.Is(err, pgx.ErrNoRows) {
		return ErrAppRuntimeLeaseLost
	} else if err != nil {
		return err
	}
	if owner != job.WorkerID || currentToken != job.LeaseToken {
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
	containerName := AppRuntimeContainerName(appID)
	if container != nil {
		containerID, imageID, imageDigest, runtimeTag = container.ID, container.ImageID, container.ImageDigest, container.RuntimeTag
	}
	stateResult, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET container_id=$4,container_name=$5,image_id=$6,image_digest=$7,runtime_tag=$8,
		    applied_deployment_id=$9,applied_workload_spec_sha256=$10,applied_generation=$11,
		    stop_grace_period_seconds=$12,failure_count=0,next_retry_at=NULL,last_failure_at=NULL,
		    last_inspected_at=now(),next_inspection_at=now()+($13::double precision*interval '1 second'),
		    last_transition_at=CASE WHEN $14 THEN now() ELSE last_transition_at END,
		    last_started_at=CASE WHEN $14 AND $15='running' THEN now() ELSE last_started_at END,
		    last_stopped_at=CASE WHEN $14 AND $15<>'running' THEN now() ELSE last_stopped_at END,
		    worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`,
		appID, job.WorkerID, job.LeaseToken, containerID, containerName, imageID, imageDigest, runtimeTag,
		optionalUUID(job.App.DesiredDeploymentID), job.App.WorkloadSpecSHA256, job.App.DesiredGeneration,
		job.App.Workload.StopGracePeriodSeconds, AppRuntimeDriftInterval.Seconds(),
		current.RuntimeStatus != status || current.ObservedGeneration != job.App.DesiredGeneration,
		status)
	if err != nil {
		return err
	}
	if stateResult.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	if current.RuntimeStatus != status || current.RuntimeError != nil || current.ObservedGeneration != job.App.DesiredGeneration {
		if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
			"runtime_status": status, "desired_generation": job.App.DesiredGeneration,
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

// AppRuntimeContainerExists verifies that an orphan's labels still refer to
// the same live App and project before the worker decides whether cleanup is
// appropriate.
func (r *Repository) AppRuntimeContainerExists(ctx context.Context, projectID, appID uuid.UUID) (bool, error) {
	if r == nil || r.pool == nil || projectID == uuid.Nil || appID == uuid.Nil {
		return false, ErrInvalidAppRuntimeJob
	}
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_apps WHERE project_id=$1 AND id=$2)`, projectID, appID).Scan(&exists)
	return exists, err
}

// QueueAppRuntimeCleanup is used by the trusted worker for validated orphan
// containers discovered by the Docker label sweep.
func (r *Repository) QueueAppRuntimeCleanup(ctx context.Context, projectID *uuid.UUID, appID uuid.UUID, containerID, containerName string, stopGrace int) error {
	if r == nil || r.pool == nil || appID == uuid.Nil || !validCleanupTarget(appID, containerID, containerName) || stopGrace < 1 || stopGrace > 120 {
		return ErrInvalidAppRuntimeJob
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := queueAppRuntimeCleanupTx(ctx, tx, projectID, appID, containerID, containerName, stopGrace); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClaimNextAppRuntimeCleanup leases one durable cleanup row without holding a
// database lock while Docker is inspected or stopped.
func (r *Repository) ClaimNextAppRuntimeCleanup(ctx context.Context, workerID string, leaseAge time.Duration) (AppRuntimeCleanupJob, error) {
	if r == nil || r.pool == nil || !validFunctionWorkerID(workerID) || leaseAge < 15*time.Second || leaseAge > 10*time.Minute {
		return AppRuntimeCleanupJob{}, ErrInvalidAppRuntimeJob
	}
	token, err := uuid.NewV7()
	if err != nil {
		return AppRuntimeCleanupJob{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AppRuntimeCleanupJob{}, err
	}
	defer tx.Rollback(ctx)
	var job AppRuntimeCleanupJob
	var containerID string
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,app_id,COALESCE(container_id,''),container_name,stop_grace_period_seconds,attempt_count
		FROM app_runtime_cleanup_jobs
		WHERE (status='pending' AND next_attempt_at<=now()) OR (status='leased' AND lease_expires_at<=now())
		ORDER BY next_attempt_at,created_at,id
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&job.ID, &job.ProjectID, &job.AppID, &containerID, &job.ContainerName, &job.StopGracePeriodSecs, &job.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppRuntimeCleanupJob{}, ErrNoAppRuntimeCleanup
	}
	if err != nil {
		return AppRuntimeCleanupJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE app_runtime_cleanup_jobs SET status='leased',worker_id=$2,lease_token=$3,lease_expires_at=now()+($4::double precision*interval '1 second'),updated_at=now() WHERE id=$1`, job.ID, workerID, token, leaseAge.Seconds()); err != nil {
		return AppRuntimeCleanupJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppRuntimeCleanupJob{}, err
	}
	job.WorkerID = workerID
	job.LeaseToken = token
	if containerID != "" {
		job.ContainerID = &containerID
	}
	return job, nil
}

func (r *Repository) RenewAppRuntimeCleanupLease(ctx context.Context, job AppRuntimeCleanupJob, leaseAge time.Duration) error {
	if validateCleanupJob(job) != nil || leaseAge < 15*time.Second || leaseAge > 10*time.Minute {
		return ErrInvalidAppRuntimeJob
	}
	result, err := r.pool.Exec(ctx, `UPDATE app_runtime_cleanup_jobs SET lease_expires_at=now()+($4::double precision*interval '1 second'),updated_at=now() WHERE id=$1 AND worker_id=$2 AND lease_token=$3 AND status='leased' AND lease_expires_at>now()`, job.ID, job.WorkerID, job.LeaseToken, leaseAge.Seconds())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	return nil
}

func (r *Repository) CompleteAppRuntimeCleanup(ctx context.Context, job AppRuntimeCleanupJob) error {
	if validateCleanupJob(job) != nil {
		return ErrInvalidAppRuntimeJob
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE app_runtime_cleanup_jobs
		SET status='completed',completed_at=now(),worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,
		    last_error=NULL,updated_at=now()
		WHERE id=$1 AND worker_id=$2 AND lease_token=$3 AND status='leased'`, job.ID, job.WorkerID, job.LeaseToken)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	return nil
}

func (r *Repository) FailAppRuntimeCleanup(ctx context.Context, job AppRuntimeCleanupJob, message string, retryAt time.Time, terminal bool) error {
	if validateCleanupJob(job) != nil || retryAt.IsZero() {
		return ErrInvalidAppRuntimeJob
	}
	message = safeAppRuntimeError(message)
	if message == "" {
		return ErrInvalidAppRuntimeJob
	}
	status := "pending"
	if terminal {
		status = "failed"
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE app_runtime_cleanup_jobs
		SET status=$4,attempt_count=attempt_count+1,next_attempt_at=CASE WHEN $5 THEN now() ELSE $6 END,
		    last_error=$7,completed_at=NULL,worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE id=$1 AND worker_id=$2 AND lease_token=$3 AND status='leased'`, job.ID, job.WorkerID, job.LeaseToken, status, terminal, retryAt.UTC(), message)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	return nil
}

func queueAppRuntimeCleanupTx(ctx context.Context, tx pgx.Tx, projectID *uuid.UUID, appID uuid.UUID, containerID, containerName string, stopGrace int) error {
	if tx == nil || appID == uuid.Nil || !validCleanupTarget(appID, containerID, containerName) || stopGrace < 1 || stopGrace > 120 {
		return ErrInvalidAppRuntimeJob
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var containerIDValue any
	if containerID != "" {
		containerIDValue = containerID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app_runtime_cleanup_jobs (id,project_id,app_id,container_id,container_name,stop_grace_period_seconds)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, id, projectID, appID, containerIDValue, containerName, stopGrace)
	return err
}

// resetAppRuntimeRetryTx wakes an App immediately after desired state changes.
// An old failure backoff must not delay convergence of a new generation.
func resetAppRuntimeRetryTx(ctx context.Context, tx pgx.Tx, appID uuid.UUID) error {
	if tx == nil || appID == uuid.Nil {
		return ErrInvalidAppRuntimeJob
	}
	_, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET failure_count=0,next_retry_at=NULL,next_inspection_at=now(),updated_at=now()
		WHERE app_id=$1`, appID)
	return err
}

func queueAppRuntimeCleanupForAppTx(ctx context.Context, tx pgx.Tx, projectID, appID uuid.UUID, stopGrace int) error {
	containerName := AppRuntimeContainerName(appID)
	var containerID, storedName string
	err := tx.QueryRow(ctx, `SELECT COALESCE(container_id,''),COALESCE(container_name,'') FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&containerID, &storedName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if storedName != "" && validRuntimeContainerName(appID, storedName) {
		containerName = storedName
	}
	return queueAppRuntimeCleanupTx(ctx, tx, &projectID, appID, containerID, containerName, stopGrace)
}

func validateRuntimeJob(job AppRuntimeJob) error {
	if job.LeaseToken == uuid.Nil || !validFunctionWorkerID(job.WorkerID) {
		return ErrInvalidAppRuntimeJob
	}
	appID, err := uuid.Parse(job.App.ID)
	if err != nil || appID == uuid.Nil {
		return ErrInvalidAppRuntimeJob
	}
	projectID, err := uuid.Parse(job.App.ProjectID)
	if err != nil || projectID == uuid.Nil || job.App.DesiredGeneration < 1 || len(job.App.WorkloadSpecSHA256) != 64 {
		return ErrInvalidAppRuntimeJob
	}
	if job.App.DesiredDeploymentID != nil {
		deploymentID, parseErr := uuid.Parse(*job.App.DesiredDeploymentID)
		if parseErr != nil || deploymentID == uuid.Nil {
			return ErrInvalidAppRuntimeJob
		}
	}
	return nil
}

func validateCleanupJob(job AppRuntimeCleanupJob) error {
	containerID := ""
	if job.ContainerID != nil {
		containerID = *job.ContainerID
	}
	if job.ID == uuid.Nil || job.AppID == uuid.Nil || job.LeaseToken == uuid.Nil || !validFunctionWorkerID(job.WorkerID) || !validCleanupTarget(job.AppID, containerID, job.ContainerName) {
		return ErrInvalidAppRuntimeJob
	}
	return nil
}

func runtimeDesiredStateMatches(current, expected domain.App) bool {
	return current.ID == expected.ID && current.ProjectID == expected.ProjectID && current.Enabled == expected.Enabled &&
		current.DesiredGeneration == expected.DesiredGeneration && current.WorkloadSpecSHA256 == expected.WorkloadSpecSHA256 &&
		optionalStringEqual(current.DesiredDeploymentID, expected.DesiredDeploymentID)
}

func optionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func optionalUUID(value *string) any {
	if value == nil {
		return nil
	}
	parsed, err := uuid.Parse(*value)
	if err != nil {
		return nil
	}
	return parsed
}

func validRuntimeContainerName(appID uuid.UUID, name string) bool {
	return name == AppRuntimeContainerName(appID)
}

func validCleanupTarget(appID uuid.UUID, containerID, name string) bool {
	if containerID == "" {
		return validRuntimeContainerName(appID, strings.TrimPrefix(name, "/"))
	}
	if !validRuntimeContainerID(containerID) || len(name) < 1 || len(name) > 256 {
		return false
	}
	if strings.HasPrefix(name, "/") {
		name = name[1:]
	}
	if name == "" || strings.ContainsRune(name, '/') {
		return false
	}
	for _, character := range name {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && !strings.ContainsRune("_.-", character) {
			return false
		}
	}
	return true
}

func validRuntimeContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validRuntimeImageID(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && validRuntimeDigest(value)
}

func validRuntimeDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[7:] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func safeAppRuntimeError(value string) string {
	value = strings.TrimSpace(value)
	allowed := map[string]struct{}{
		"runtime unavailable": {}, "image artifact unavailable": {}, "image verification failed": {},
		"image import failed": {}, "runtime network conflict": {}, "container ownership conflict": {},
		"container create failed": {}, "container start failed": {}, "container inspection failed": {},
		"container exited unexpectedly": {}, "unsupported runtime platform": {},
		"runtime image declares unsupported volumes": {}, "runtime cleanup unavailable": {},
	}
	if _, ok := allowed[value]; !ok || len(value) > AppRuntimeMaxErrorBytes {
		return ""
	}
	return value
}

func appRuntimeDebugIdentity(job AppRuntimeJob) string {
	return fmt.Sprintf("project_id=%s app_id=%s generation=%d", job.App.ProjectID, job.App.ID, job.App.DesiredGeneration)
}
