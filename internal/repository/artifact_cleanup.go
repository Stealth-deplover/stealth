package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNoArtifactCleanup        = errors.New("no artifact cleanup job available")
	ErrArtifactCleanupNotOwned  = errors.New("artifact cleanup job is not owned by this worker")
	ErrInvalidArtifactCleanup   = errors.New("invalid artifact cleanup job")
	ErrArtifactCleanupStore     = errors.New("invalid artifact cleanup store")
	ErrArtifactCleanupOperation = errors.New("invalid artifact cleanup operation")
	ErrArtifactPublishConflict  = errors.New("artifact publish cleanup reservation already exists")
	ErrArtifactPublishLost      = errors.New("artifact publish cleanup reservation is no longer available")
)

// ArtifactPublishReservationAge is deliberately conservative. A reservation
// is not eligible for the cleanup worker while an upload/build may still be
// publishing its bytes. The owning metadata transaction removes it before
// exposing the artifact.
const ArtifactPublishReservationAge = 24 * time.Hour

type ArtifactCleanupStoreKind string

const (
	ArtifactCleanupStorage      ArtifactCleanupStoreKind = "storage"
	ArtifactCleanupFunctions    ArtifactCleanupStoreKind = "functions"
	ArtifactCleanupSiteArchives ArtifactCleanupStoreKind = "site_archives"
	ArtifactCleanupSites        ArtifactCleanupStoreKind = "sites"
)

type ArtifactCleanupOperation string

const (
	ArtifactCleanupRelative ArtifactCleanupOperation = "relative"
	ArtifactCleanupProject  ArtifactCleanupOperation = "project"
)

type ArtifactCleanupInput struct {
	ProjectID    uuid.UUID
	StoreKind    ArtifactCleanupStoreKind
	Operation    ArtifactCleanupOperation
	RelativePath string
}

// ArtifactCleanupJob is the leased, non-secret projection consumed by the
// trusted cleanup worker. The path is never returned through an HTTP API.
type ArtifactCleanupJob struct {
	ID           uuid.UUID
	ProjectID    uuid.UUID
	StoreKind    ArtifactCleanupStoreKind
	Operation    ArtifactCleanupOperation
	RelativePath string
	Attempts     int
}

type ArtifactCleanupPersistence interface {
	ClaimNextArtifactCleanup(context.Context, string, time.Duration) (ArtifactCleanupJob, error)
	RequeueStaleArtifactCleanup(context.Context, time.Duration) (int64, error)
	CompleteArtifactCleanup(context.Context, uuid.UUID, string) error
	RetryArtifactCleanup(context.Context, uuid.UUID, string, time.Time, string) error
	FailArtifactCleanup(context.Context, uuid.UUID, string, string) error
}

var _ ArtifactCleanupPersistence = (*Repository)(nil)

func ValidateArtifactCleanupInput(input ArtifactCleanupInput) error {
	if input.ProjectID == uuid.Nil || input.ProjectID.Version() != uuid.Version(7) {
		return fmt.Errorf("%w: project id", ErrInvalidArtifactCleanup)
	}
	if !validArtifactCleanupStore(input.StoreKind) {
		return ErrArtifactCleanupStore
	}
	switch input.Operation {
	case ArtifactCleanupRelative:
		if !validArtifactCleanupRelativePath(input.RelativePath) {
			return fmt.Errorf("%w: relative path", ErrInvalidArtifactCleanup)
		}
	case ArtifactCleanupProject:
		if input.RelativePath != input.ProjectID.String() {
			return fmt.Errorf("%w: project path", ErrInvalidArtifactCleanup)
		}
	default:
		return ErrArtifactCleanupOperation
	}
	return nil
}

func validArtifactCleanupStore(kind ArtifactCleanupStoreKind) bool {
	switch kind {
	case ArtifactCleanupStorage, ArtifactCleanupFunctions, ArtifactCleanupSiteArchives, ArtifactCleanupSites:
		return true
	default:
		return false
	}
}

func validArtifactCleanupRelativePath(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 3 || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	for _, part := range parts {
		id, err := uuid.Parse(part)
		if err != nil || id == uuid.Nil || id.Version() != uuid.Version(7) {
			return false
		}
	}
	return true
}

func queueArtifactCleanupTx(ctx context.Context, tx pgx.Tx, input ArtifactCleanupInput) error {
	if err := ValidateArtifactCleanupInput(input); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("create artifact cleanup identifier: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO artifact_cleanup_jobs (id,project_id,store_kind,operation,relative_path)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (store_kind,operation,relative_path) DO NOTHING`,
		id, input.ProjectID, input.StoreKind, input.Operation, input.RelativePath)
	return err
}

// ReserveArtifactPublishCleanup records durable cleanup intent before a
// physical artifact is published. If the process dies before metadata can
// consume the reservation, RequeueStaleArtifactCleanup eventually promotes it
// to the normal idempotent cleanup queue.
func (r *Repository) ReserveArtifactPublishCleanup(ctx context.Context, input ArtifactCleanupInput) error {
	if input.Operation != ArtifactCleanupRelative {
		return fmt.Errorf("%w: publish reservations only support relative artifacts", ErrInvalidArtifactCleanup)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := reserveArtifactPublishCleanupTx(ctx, tx, input); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func reserveArtifactPublishCleanupTx(ctx context.Context, tx pgx.Tx, input ArtifactCleanupInput) error {
	if err := ValidateArtifactCleanupInput(input); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("create artifact publish reservation identifier: %w", err)
	}
	var reservedID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO artifact_cleanup_jobs (id,project_id,store_kind,operation,relative_path,status)
		VALUES ($1,$2,$3,$4,$5,'reserved')
		ON CONFLICT (store_kind,operation,relative_path) DO UPDATE
		SET updated_at=now()
		WHERE artifact_cleanup_jobs.status='reserved'
		RETURNING id`, id, input.ProjectID, input.StoreKind, input.Operation, input.RelativePath).Scan(&reservedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArtifactPublishConflict
	}
	if err != nil {
		return err
	}
	return nil
}

// finalizeArtifactPublishCleanupTx consumes the reservation in the same
// metadata transaction that publishes the corresponding row. If recovery has
// already promoted or claimed the reservation, the metadata transaction is
// rejected rather than exposing a row whose physical bytes may be removed.
func finalizeArtifactPublishCleanupTx(ctx context.Context, tx pgx.Tx, input ArtifactCleanupInput) error {
	if err := ValidateArtifactCleanupInput(input); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		DELETE FROM artifact_cleanup_jobs
		WHERE project_id=$1 AND store_kind=$2 AND operation=$3 AND relative_path=$4
		  AND status='reserved' AND leased_at IS NULL`, input.ProjectID, input.StoreKind, input.Operation, input.RelativePath)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrArtifactPublishLost
	}
	return nil
}

func validatePublishCleanup(input *ArtifactCleanupInput, projectID uuid.UUID, kind ArtifactCleanupStoreKind, relativePath string) error {
	if input == nil {
		return nil
	}
	if input.ProjectID != projectID || input.StoreKind != kind || input.Operation != ArtifactCleanupRelative || input.RelativePath != relativePath {
		return fmt.Errorf("%w: publish reservation does not match metadata", ErrInvalidArtifactCleanup)
	}
	return nil
}

// ClaimNextArtifactCleanup leases one due job. SKIP LOCKED allows multiple
// workers while the lease makes a crashed process recoverable.
func (r *Repository) ClaimNextArtifactCleanup(ctx context.Context, workerID string, leaseAge time.Duration) (ArtifactCleanupJob, error) {
	if !validFunctionWorkerID(workerID) || leaseAge <= 0 {
		return ArtifactCleanupJob{}, ErrInvalidArtifactCleanup
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ArtifactCleanupJob{}, err
	}
	defer tx.Rollback(ctx)
	var job ArtifactCleanupJob
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,store_kind,operation,relative_path,attempts
		FROM artifact_cleanup_jobs
		WHERE status='pending' AND available_at<=now()
		  AND (leased_at IS NULL OR leased_at < now() - ($1::double precision * interval '1 second'))
		ORDER BY available_at,id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, leaseAge.Seconds()).Scan(
		&job.ID, &job.ProjectID, &job.StoreKind, &job.Operation, &job.RelativePath, &job.Attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ArtifactCleanupJob{}, ErrNoArtifactCleanup
	}
	if err != nil {
		return ArtifactCleanupJob{}, err
	}
	job.Attempts++
	if _, err := tx.Exec(ctx, `
		UPDATE artifact_cleanup_jobs
		SET leased_at=now(),worker_id=$2,attempts=$3,last_error=NULL,updated_at=now()
		WHERE id=$1 AND status='pending'`, job.ID, workerID, job.Attempts); err != nil {
		return ArtifactCleanupJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ArtifactCleanupJob{}, err
	}
	return job, nil
}

// RequeueStaleArtifactCleanup returns jobs held by a worker that disappeared.
func (r *Repository) RequeueStaleArtifactCleanup(ctx context.Context, leaseAge time.Duration) (int64, error) {
	if leaseAge <= 0 {
		return 0, ErrInvalidArtifactCleanup
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE artifact_cleanup_jobs
		SET leased_at=NULL,worker_id=NULL,available_at=LEAST(available_at,now()),updated_at=now()
		WHERE status='pending' AND leased_at IS NOT NULL
		  AND leased_at < now() - ($1::double precision * interval '1 second')`, leaseAge.Seconds())
	if err != nil {
		return 0, err
	}
	reservedAge := ArtifactPublishReservationAge
	if leaseAge > reservedAge {
		reservedAge = leaseAge
	}
	reserved, err := r.pool.Exec(ctx, `
		UPDATE artifact_cleanup_jobs
		SET status='pending',available_at=LEAST(available_at,now()),updated_at=now()
		WHERE status='reserved' AND updated_at < now() - ($1::double precision * interval '1 second')`, reservedAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected() + reserved.RowsAffected(), nil
}

func (r *Repository) CompleteArtifactCleanup(ctx context.Context, jobID uuid.UUID, workerID string) error {
	if jobID == uuid.Nil || !validFunctionWorkerID(workerID) {
		return ErrInvalidArtifactCleanup
	}
	result, err := r.pool.Exec(ctx, `
		DELETE FROM artifact_cleanup_jobs
		WHERE id=$1 AND status='pending' AND worker_id=$2 AND leased_at IS NOT NULL`, jobID, workerID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrArtifactCleanupNotOwned
	}
	return nil
}

func (r *Repository) RetryArtifactCleanup(ctx context.Context, jobID uuid.UUID, workerID string, retryAt time.Time, lastError string) error {
	if jobID == uuid.Nil || !validFunctionWorkerID(workerID) || retryAt.IsZero() {
		return ErrInvalidArtifactCleanup
	}
	lastError = truncateArtifactCleanupError(lastError)
	result, err := r.pool.Exec(ctx, `
		UPDATE artifact_cleanup_jobs
		SET leased_at=NULL,worker_id=NULL,available_at=$3,last_error=$4,updated_at=now()
		WHERE id=$1 AND status='pending' AND worker_id=$2 AND leased_at IS NOT NULL`, jobID, workerID, retryAt.UTC(), lastError)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrArtifactCleanupNotOwned
	}
	return nil
}

func (r *Repository) FailArtifactCleanup(ctx context.Context, jobID uuid.UUID, workerID string, lastError string) error {
	if jobID == uuid.Nil || !validFunctionWorkerID(workerID) {
		return ErrInvalidArtifactCleanup
	}
	lastError = truncateArtifactCleanupError(lastError)
	result, err := r.pool.Exec(ctx, `
		UPDATE artifact_cleanup_jobs
		SET status='failed',leased_at=NULL,worker_id=NULL,last_error=$3,failed_at=now(),updated_at=now()
		WHERE id=$1 AND status='pending' AND worker_id=$2 AND leased_at IS NOT NULL`, jobID, workerID, lastError)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrArtifactCleanupNotOwned
	}
	return nil
}

func truncateArtifactCleanupError(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\x00' {
			return ' '
		}
		return r
	}, value)
	if len(value) > 4000 {
		return value[:4000]
	}
	return value
}
