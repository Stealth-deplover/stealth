package repository

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
)

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
