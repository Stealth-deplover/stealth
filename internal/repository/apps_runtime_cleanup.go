package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
)

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
		SELECT $1,$2,$3,NULLIF($4::text,''),$5,$6
		WHERE NOT EXISTS (
		  SELECT 1 FROM app_runtime_cleanup_jobs
		  WHERE app_id=$3 AND container_id IS NOT DISTINCT FROM NULLIF($4::text,'')
		    AND container_name=$5 AND status='failed'
		)
		ON CONFLICT DO NOTHING`, id, projectID, appID, containerID, containerName, stopGrace)
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
