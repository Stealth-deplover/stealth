// Package artifactcleanup retries physical artifact removal after the owning
// metadata transaction has committed. Jobs are durable in PostgreSQL and are
// processed only by the trusted worker process.
package artifactcleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

const (
	defaultPollInterval = 500 * time.Millisecond
	defaultLeaseAge     = 20 * time.Minute
	maxErrorLength      = 240
)

var ErrStoreUnavailable = errors.New("artifact cleanup store is unavailable")

// Cleaner is intentionally narrower than the concrete stores. The worker
// can only remove server-derived relative paths or a whole UUID project
// namespace; it cannot accept arbitrary filesystem commands.
type Cleaner interface {
	RemoveRelative(context.Context, string) error
	RemoveProject(context.Context, uuid.UUID) error
}

type Stores struct {
	Storage      Cleaner
	Functions    Cleaner
	SiteArchives Cleaner
	Sites        Cleaner
}

type Worker struct {
	Store        repository.ArtifactCleanupPersistence
	Cleaners     Stores
	WorkerID     string
	PollInterval time.Duration
	LeaseAge     time.Duration
	Logger       *slog.Logger
}

func New(store repository.ArtifactCleanupPersistence, cleaners Stores, workerID string, logger *slog.Logger) (*Worker, error) {
	if store == nil || strings.TrimSpace(workerID) == "" || len(workerID) > 128 || !validWorkerID(workerID) {
		return nil, errors.New("invalid artifact cleanup worker dependencies")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Store:        store,
		Cleaners:     cleaners,
		WorkerID:     workerID,
		PollInterval: defaultPollInterval,
		LeaseAge:     defaultLeaseAge,
		Logger:       logger,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Store == nil || !validWorkerID(w.WorkerID) {
		return errors.New("artifact cleanup worker is not configured")
	}
	poll := w.PollInterval
	if poll <= 0 {
		poll = defaultPollInterval
	}
	leaseAge := w.LeaseAge
	if leaseAge <= 0 {
		leaseAge = defaultLeaseAge
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if _, err := w.Store.RequeueStaleArtifactCleanup(ctx, leaseAge); err != nil && !isContextError(err) {
			w.logError("requeue stale artifact cleanup jobs failed", err)
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if isContextError(err) {
				return nil
			}
			w.logError("artifact cleanup job failed", err)
		}
		if processed && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce claims and processes at most one due job. A physical removal error
// is persisted with a bounded exponential backoff, so a transient failure is
// retryable without turning the worker into a busy loop.
func (w *Worker) RunOnce(ctx context.Context) (processed bool, runErr error) {
	if w == nil || w.Store == nil || !validWorkerID(w.WorkerID) {
		return false, errors.New("artifact cleanup worker is not configured")
	}
	job, err := w.Store.ClaimNextArtifactCleanup(ctx, w.WorkerID, w.leaseAge())
	if errors.Is(err, repository.ErrNoArtifactCleanup) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		if runErr != nil && !isContextError(runErr) {
			w.logJobError(job, runErr)
		}
	}()

	input := repository.ArtifactCleanupInput{
		ProjectID:    job.ProjectID,
		StoreKind:    job.StoreKind,
		Operation:    job.Operation,
		RelativePath: job.RelativePath,
	}
	if err := repository.ValidateArtifactCleanupInput(input); err != nil {
		finishErr := w.Store.FailArtifactCleanup(ctx, job.ID, w.WorkerID, "artifact cleanup job is invalid")
		if finishErr != nil {
			return true, finishErr
		}
		return true, nil
	}
	cleaner := w.cleaner(job.StoreKind)
	if cleaner == nil {
		return true, w.retry(ctx, job, ErrStoreUnavailable)
	}
	if err := w.remove(ctx, cleaner, job); err != nil {
		return true, w.retry(ctx, job, err)
	}
	if err := w.Store.CompleteArtifactCleanup(ctx, job.ID, w.WorkerID); err != nil {
		return true, err
	}
	return true, nil
}

func (w *Worker) retry(ctx context.Context, job repository.ArtifactCleanupJob, cause error) error {
	retryAt := time.Now().UTC().Add(retryDelay(job.Attempts))
	message := safeError(cause)
	if err := w.Store.RetryArtifactCleanup(ctx, job.ID, w.WorkerID, retryAt, message); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (w *Worker) remove(ctx context.Context, cleaner Cleaner, job repository.ArtifactCleanupJob) error {
	switch job.Operation {
	case repository.ArtifactCleanupRelative:
		return cleaner.RemoveRelative(ctx, job.RelativePath)
	case repository.ArtifactCleanupProject:
		return cleaner.RemoveProject(ctx, job.ProjectID)
	default:
		return fmt.Errorf("unsupported artifact cleanup operation %q", job.Operation)
	}
}

func (w *Worker) cleaner(kind repository.ArtifactCleanupStoreKind) Cleaner {
	switch kind {
	case repository.ArtifactCleanupStorage:
		return w.Cleaners.Storage
	case repository.ArtifactCleanupFunctions:
		return w.Cleaners.Functions
	case repository.ArtifactCleanupSiteArchives:
		return w.Cleaners.SiteArchives
	case repository.ArtifactCleanupSites:
		return w.Cleaners.Sites
	default:
		return nil
	}
}

func (w *Worker) leaseAge() time.Duration {
	if w.LeaseAge > 0 {
		return w.LeaseAge
	}
	return defaultLeaseAge
}

func retryDelay(attempts int) time.Duration {
	switch {
	case attempts <= 1:
		return time.Minute
	case attempts == 2:
		return 5 * time.Minute
	case attempts == 3:
		return 30 * time.Minute
	case attempts == 4:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func validWorkerID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func safeError(err error) string {
	if err == nil {
		return "artifact cleanup failed"
	}
	message := strings.TrimSpace(err.Error())
	message = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\x00' {
			return ' '
		}
		return r
	}, message)
	if len(message) > maxErrorLength {
		message = message[:maxErrorLength]
	}
	if message == "" {
		return "artifact cleanup failed"
	}
	return message
}

func (w *Worker) logError(message string, err error) {
	if w.Logger != nil {
		w.Logger.Error(message, "error", safeError(err))
	}
}

func (w *Worker) logJobError(job repository.ArtifactCleanupJob, err error) {
	if w.Logger != nil {
		w.Logger.Error("artifact cleanup job failed", "job_id", job.ID, "project_id", job.ProjectID, "store_kind", job.StoreKind, "attempt", job.Attempts, "error", safeError(err))
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
