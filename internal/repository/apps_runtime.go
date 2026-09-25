package repository

// App runtime persistence is the private durable coordination seam between
// PostgreSQL desired state and the trusted Moby reconciler. Docker IDs and
// image paths in these projections never enter the normal App API response.

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	ErrNoAppRuntimeJob           = errors.New("no App runtime job available")
	ErrNoAppHealthCheckJob       = errors.New("no App health check available")
	ErrNoAppRuntimeCleanup       = errors.New("no App runtime cleanup job available")
	ErrAppRuntimeLeaseLost       = errors.New("App runtime lease is no longer owned by this worker")
	ErrAppRuntimeStale           = errors.New("App desired state changed during runtime reconciliation")
	ErrInvalidAppRuntimeJob      = errors.New("invalid App runtime job")
	ErrAppRuntimeLogSourcesLimit = errors.New("App runtime log source limit exceeded")
)

const maxAppRuntimeLogSources = 2048

// AppRuntimeJob is a trusted worker projection. ImagePath is private and is
// read only from the immutable selected AppDeployment.
type AppRuntimeJob struct {
	App           domain.App
	Deployment    domain.AppDeployment
	RouteIdentity uuid.UUID
	ContainerName string
	ImagePath     string
	WorkerID      string
	LeaseToken    uuid.UUID
	FailureCount  int
}

// AppRuntimeContainer is the private identity confirmed by Docker inspect.
type AppRuntimeContainer struct {
	ID          string
	Name        string
	ImageID     string
	ImageDigest string
	RuntimeTag  string
	Address     string
}

// AppHealthCheckJob is fenced by the same per-App runtime lease used by
// reconciliation. Its container identity is an internal worker value only.
type AppHealthCheckJob struct {
	App                domain.App
	ContainerID        string
	Address            string
	RouteIdentity      uuid.UUID
	ContainerName      string
	HealthStatus       string
	HealthFailureCount int
	WorkerID           string
	LeaseToken         uuid.UUID
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

// ClaimNextAppHealthCheck leases one due probe while keeping probe work outside
// the database transaction. The selected row must still describe the current
// observed generation and exact managed container.
func (r *Repository) ClaimNextAppHealthCheck(ctx context.Context, workerID string, leaseAge time.Duration) (AppHealthCheckJob, error) {
	if r == nil || r.pool == nil || !validFunctionWorkerID(workerID) || leaseAge < 15*time.Second || leaseAge > 10*time.Minute {
		return AppHealthCheckJob{}, ErrInvalidAppRuntimeJob
	}
	token, err := uuid.NewV7()
	if err != nil {
		return AppHealthCheckJob{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AppHealthCheckJob{}, err
	}
	defer tx.Rollback(ctx)
	var job AppHealthCheckJob
	var projectID, appID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT app.project_id,app.id,runtime.container_id,host(runtime.container_address),runtime.health_status,runtime.health_failure_count,
		       runtime.route_identity,runtime.container_name
		FROM project_apps app
		JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		WHERE app.enabled=TRUE AND app.desired_deployment_id IS NOT NULL
		  AND app.desired_generation=app.observed_generation AND app.runtime_status='running'
		  AND runtime.applied_generation=app.desired_generation
		  AND runtime.applied_deployment_id=app.desired_deployment_id
		  AND runtime.applied_workload_spec_sha256=app.workload_spec_sha256
		  AND runtime.container_id IS NOT NULL AND runtime.container_address IS NOT NULL
		  AND runtime.health_generation=app.desired_generation
		  AND runtime.health_deployment_id=app.desired_deployment_id
		  AND runtime.health_container_id=runtime.container_id
		  AND runtime.health_route_identity=runtime.route_identity
		  AND runtime.next_health_check_at IS NOT NULL AND runtime.next_health_check_at<=now()
		  AND (runtime.lease_token IS NULL OR runtime.lease_expires_at<=now())
		ORDER BY runtime.next_health_check_at,app.updated_at,app.id
		FOR UPDATE OF app,runtime SKIP LOCKED LIMIT 1`).Scan(
		&projectID, &appID, &job.ContainerID, &job.Address, &job.HealthStatus, &job.HealthFailureCount,
		&job.RouteIdentity, &job.ContainerName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppHealthCheckJob{}, ErrNoAppHealthCheckJob
	}
	if err != nil {
		return AppHealthCheckJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=$2,lease_token=$3,lease_expires_at=now()+($4::double precision*interval '1 second'),updated_at=now() WHERE app_id=$1`, appID, workerID, token, leaseAge.Seconds()); err != nil {
		return AppHealthCheckJob{}, err
	}
	job.App, err = appByID(ctx, tx, projectID, appID, false)
	if err != nil {
		return AppHealthCheckJob{}, err
	}
	job.WorkerID = workerID
	job.LeaseToken = token
	if !validHealthCheckJob(job) {
		return AppHealthCheckJob{}, ErrInvalidAppRuntimeJob
	}
	if err := tx.Commit(ctx); err != nil {
		return AppHealthCheckJob{}, err
	}
	return job, nil
}

// IsAppHealthCheckCurrent rechecks a due probe's desired identity and lease
// before and after network I/O. CompleteAppHealthCheck repeats this fence in
// its write transaction.
func (r *Repository) IsAppHealthCheckCurrent(ctx context.Context, job AppHealthCheckJob) (bool, error) {
	if r == nil || r.pool == nil || !validHealthCheckJob(job) {
		return false, ErrInvalidAppRuntimeJob
	}
	var current bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM project_apps app
		  JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		  WHERE app.project_id=$1 AND app.id=$2 AND app.enabled=TRUE AND app.runtime_status='running'
		    AND app.desired_generation=$3 AND app.observed_generation=$3
		    AND app.desired_deployment_id=$4 AND app.workload_spec_sha256=$5
		    AND runtime.applied_generation=$3 AND runtime.applied_deployment_id=$4
		    AND runtime.applied_workload_spec_sha256=$5
		    AND runtime.container_id=$6 AND host(runtime.container_address)=$7
		    AND runtime.health_generation=$3 AND runtime.health_deployment_id=$4 AND runtime.health_container_id=$6
		    AND runtime.route_identity=$10 AND runtime.container_name=$11 AND runtime.health_route_identity=$10
		    AND runtime.worker_id=$8 AND runtime.lease_token=$9 AND runtime.lease_expires_at>now()
		)`, uuid.MustParse(job.App.ProjectID), uuid.MustParse(job.App.ID), job.App.DesiredGeneration,
		uuid.MustParse(*job.App.DesiredDeploymentID), job.App.WorkloadSpecSHA256, job.ContainerID, job.Address,
		job.WorkerID, job.LeaseToken, job.RouteIdentity, job.ContainerName).Scan(&current)
	return current, err
}

// CompleteAppHealthCheck publishes only an outcome for the exact current
// lease, desired generation, deployment, container ID, and inspected address.
func (r *Repository) CompleteAppHealthCheck(ctx context.Context, job AppHealthCheckJob, succeeded bool) error {
	if r == nil || r.pool == nil || !validHealthCheckJob(job) {
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
	if !runtimeDesiredStateMatches(current, job.App) {
		_, releaseErr := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`, appID, job.WorkerID, job.LeaseToken)
		if releaseErr != nil {
			return releaseErr
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrAppRuntimeStale
	}
	var owner string
	var token uuid.UUID
	var containerID, healthStatus string
	var healthContainerID *string
	var containerName string
	var address *string
	var healthGeneration *int64
	var healthDeploymentID *uuid.UUID
	var routeIdentity uuid.UUID
	var healthRouteIdentity *uuid.UUID
	var failures int
	if err := tx.QueryRow(ctx, `
		SELECT worker_id,lease_token,container_id,host(container_address),health_status,health_generation,health_deployment_id,health_container_id,health_failure_count,
		       route_identity,container_name,health_route_identity
		FROM app_runtime_state WHERE app_id=$1 AND lease_expires_at>now() FOR UPDATE`, appID).Scan(
		&owner, &token, &containerID, &address, &healthStatus, &healthGeneration, &healthDeploymentID, &healthContainerID, &failures,
		&routeIdentity, &containerName, &healthRouteIdentity,
	); errors.Is(err, pgx.ErrNoRows) {
		return ErrAppRuntimeLeaseLost
	} else if err != nil {
		return err
	}
	if owner != job.WorkerID || token != job.LeaseToken {
		return ErrAppRuntimeLeaseLost
	}
	if containerID != job.ContainerID || address == nil || *address != job.Address || healthContainerID == nil || *healthContainerID != job.ContainerID ||
		routeIdentity != job.RouteIdentity || containerName != job.ContainerName || healthRouteIdentity == nil || *healthRouteIdentity != job.RouteIdentity ||
		healthGeneration == nil || *healthGeneration != job.App.DesiredGeneration || job.App.DesiredDeploymentID == nil || healthDeploymentID == nil || *healthDeploymentID != uuid.MustParse(*job.App.DesiredDeploymentID) ||
		current.RuntimeStatus != "running" || current.ObservedGeneration != current.DesiredGeneration {
		_, releaseErr := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,next_inspection_at=now(),updated_at=now() WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`, appID, job.WorkerID, job.LeaseToken)
		if releaseErr != nil {
			return releaseErr
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrAppRuntimeStale
	}
	newStatus, newFailures := healthStateAfterProbe(healthStatus, failures, succeeded, job.App.Workload.HealthCheck.FailureThreshold)
	interval := job.App.Workload.HealthCheck.IntervalSeconds
	result, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET health_status=$4,health_route_identity=route_identity,health_failure_count=$5,health_checked_at=now(),
		    next_health_check_at=now()+($6::double precision*interval '1 second'),
		    worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3 AND lease_expires_at>now()
		  AND container_id=$7 AND host(container_address)=$8 AND health_generation=$9
		  AND health_deployment_id=$10 AND health_container_id=$7
		  AND route_identity=$11 AND container_name=$12 AND health_route_identity=$11`,
		appID, job.WorkerID, job.LeaseToken, newStatus, newFailures, interval,
		job.ContainerID, job.Address, job.App.DesiredGeneration, uuid.MustParse(*job.App.DesiredDeploymentID), job.RouteIdentity, job.ContainerName)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	if newStatus != healthStatus {
		if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
			"runtime_status": current.RuntimeStatus, "health_status": newStatus,
			"route_status":       appRouteStatus(current, newStatus),
			"desired_generation": current.DesiredGeneration, "observed_generation": current.ObservedGeneration,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// InvalidateAppHealthIdentity removes routing eligibility as soon as a due
// inspection finds that the expected container or its owned bridge address
// changed. Runtime reconciliation is made immediately due for recovery.
func (r *Repository) InvalidateAppHealthIdentity(ctx context.Context, job AppHealthCheckJob) error {
	if r == nil || r.pool == nil || !validHealthCheckJob(job) {
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
	if !runtimeDesiredStateMatches(current, job.App) {
		_, releaseErr := tx.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`, appID, job.WorkerID, job.LeaseToken)
		if releaseErr != nil {
			return releaseErr
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrAppRuntimeStale
	}
	result, err := tx.Exec(ctx, `
		UPDATE app_runtime_state
		SET health_status='pending',health_generation=NULL,health_deployment_id=NULL,health_container_id=NULL,health_route_identity=NULL,
		    health_failure_count=0,health_checked_at=NULL,next_health_check_at=NULL,
		    container_address=NULL,next_inspection_at=now(),last_inspected_at=NULL,
		    worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3 AND lease_expires_at>now()
		  AND container_id=$4 AND host(container_address)=$5 AND applied_generation=$6
		  AND applied_deployment_id=$7 AND applied_workload_spec_sha256=$8
		  AND route_identity=$9 AND container_name=$10 AND health_route_identity=$9`,
		appID, job.WorkerID, job.LeaseToken, job.ContainerID, job.Address,
		job.App.DesiredGeneration, uuid.MustParse(*job.App.DesiredDeploymentID), job.App.WorkloadSpecSHA256, job.RouteIdentity, job.ContainerName)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAppRuntimeLeaseLost
	}
	if _, err := tx.Exec(ctx, `UPDATE project_apps SET runtime_status='degraded',runtime_error='runtime unavailable',updated_at=now() WHERE project_id=$1 AND id=$2 AND desired_generation=$3`, projectID, appID, job.App.DesiredGeneration); err != nil {
		return err
	}
	if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.runtime.updated", "app", appID, map[string]any{
		"runtime_status": "degraded", "health_status": "pending", "route_status": appRouteStatus(current, "pending"),
		"desired_generation": current.DesiredGeneration, "observed_generation": current.ObservedGeneration,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReleaseAppHealthCheck makes a cancelled probe immediately eligible again
// while requiring the same lease token.
func (r *Repository) ReleaseAppHealthCheck(ctx context.Context, job AppHealthCheckJob) error {
	if r == nil || r.pool == nil || !validHealthCheckJob(job) {
		return ErrInvalidAppRuntimeJob
	}
	_, err := r.pool.Exec(ctx, `UPDATE app_runtime_state SET worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE app_id=$1 AND worker_id=$2 AND lease_token=$3`, uuid.MustParse(job.App.ID), job.WorkerID, job.LeaseToken)
	return err
}

func healthStateAfterProbe(current string, failures int, succeeded bool, threshold int) (string, int) {
	if succeeded {
		return "healthy", 0
	}
	if failures < 0 {
		failures = 0
	}
	failures++
	if threshold < 1 {
		threshold = 1
	}
	if failures >= threshold {
		return "unhealthy", failures
	}
	if current == "healthy" || current == "unhealthy" {
		return current, failures
	}
	return "pending", failures
}

func appRouteStatus(app domain.App, healthStatus string) string {
	switch {
	case !app.Enabled || app.DesiredDeploymentID == nil || app.PlatformHostname == nil:
		return "not_available"
	case !app.RouteDeploymentReady || app.ObservedGeneration != app.DesiredGeneration || app.RuntimeStatus != "running":
		return "waiting_for_runtime"
	case healthStatus != "healthy":
		return "waiting_for_health"
	default:
		return "active"
	}
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

// ListAppRuntimeLogSources returns only container IDs previously verified by
// successful runtime convergence. Callers cannot select a container ID, and
// the project/App authorization check happens before the private mapping is
// read. The extra row detects overflow rather than silently hiding history.
func (r *Repository) ListAppRuntimeLogSources(ctx context.Context, projectID, appID uuid.UUID, actor AppActor) ([]string, error) {
	if r == nil || r.pool == nil || projectID == uuid.Nil || appID == uuid.Nil {
		return nil, ErrNotFound
	}
	if _, err := r.requireAppRead(ctx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := appByID(ctx, r.pool, projectID, appID, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT container_id
		FROM app_runtime_log_sources
		WHERE project_id=$1 AND app_id=$2
		ORDER BY first_seen_at,container_id
		LIMIT $3`, projectID, appID, maxAppRuntimeLogSources+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if !validRuntimeContainerID(id) {
			return nil, ErrInvalidAppRuntimeJob
		}
		ids = append(ids, id)
		if len(ids) > maxAppRuntimeLogSources {
			return nil, ErrAppRuntimeLogSourcesLimit
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
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
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,app_id,container_name,stop_grace_period_seconds,attempt_count
		FROM app_runtime_cleanup_jobs
		WHERE (status='pending' AND next_attempt_at<=now()) OR (status='leased' AND lease_expires_at<=now())
		ORDER BY next_attempt_at,created_at,id
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&job.ID, &job.ProjectID, &job.AppID, &job.ContainerName, &job.StopGracePeriodSecs, &job.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppRuntimeCleanupJob{}, ErrNoAppRuntimeCleanup
	}
	if err != nil {
		return AppRuntimeCleanupJob{}, err
	}
	var containerID string
	if err := tx.QueryRow(ctx, `
		UPDATE app_runtime_cleanup_jobs
		SET status='leased',worker_id=$2,lease_token=$3,lease_expires_at=now()+($4::double precision*interval '1 second'),updated_at=now()
		WHERE id=$1
		RETURNING COALESCE(container_id,'')`, job.ID, workerID, token, leaseAge.Seconds()).Scan(&containerID); err != nil {
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
	_, err = tx.Exec(ctx, `
		INSERT INTO app_runtime_cleanup_jobs (id,project_id,app_id,container_id,container_name,stop_grace_period_seconds)
		VALUES ($1,$2,$3,NULLIF($4::text,''),$5,$6) ON CONFLICT DO NOTHING`, id, projectID, appID, containerID, containerName, stopGrace)
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
		SET failure_count=0,next_retry_at=NULL,next_inspection_at=now(),
		    health_status='pending',health_generation=NULL,health_deployment_id=NULL,health_container_id=NULL,
		    health_route_identity=NULL,health_failure_count=0,health_checked_at=NULL,next_health_check_at=NULL,
		    container_address=NULL,updated_at=now()
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
	if job.RouteIdentity == uuid.Nil || job.ContainerName != AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) {
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

func validHealthCheckJob(job AppHealthCheckJob) bool {
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	deploymentID := uuid.Nil
	if job.App.DesiredDeploymentID != nil {
		deploymentID, _ = uuid.Parse(*job.App.DesiredDeploymentID)
	}
	return appErr == nil && projectErr == nil && deploymentID != uuid.Nil && appID != uuid.Nil && projectID != uuid.Nil &&
		job.RouteIdentity != uuid.Nil && job.ContainerName == AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) &&
		job.App.Enabled && job.App.DesiredGeneration >= 1 && job.App.ObservedGeneration == job.App.DesiredGeneration &&
		job.App.RuntimeStatus == "running" && job.LeaseToken != uuid.Nil && validFunctionWorkerID(job.WorkerID) &&
		validRuntimeContainerID(job.ContainerID) && validPrivateRuntimeAddress(job.Address) &&
		(job.HealthStatus == "pending" || job.HealthStatus == "healthy" || job.HealthStatus == "unhealthy") &&
		job.HealthFailureCount >= 0
}

func validPrivateRuntimeAddress(value string) bool {
	address := net.ParseIP(value)
	return address != nil && address.To4() != nil && address.IsPrivate() && !address.IsLoopback() &&
		!address.IsUnspecified() && !address.IsLinkLocalUnicast() && !address.IsMulticast()
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
	return ValidAppRuntimeContainerName(appID, name)
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
