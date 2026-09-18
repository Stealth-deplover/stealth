package monitoring

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

const (
	defaultPollInterval = 1 * time.Second
	defaultLeaseAge     = 2 * time.Minute
)

type Persistence interface {
	RequeueStaleAdminMonitors(context.Context, time.Duration) (int64, error)
	ClaimNextAdminMonitor(context.Context, string, time.Duration) (repository.AdminMonitorJob, error)
	CompleteAdminMonitorCheck(context.Context, uuid.UUID, string, repository.AdminMonitorCheckInput) error
}

type Worker struct {
	Store        Persistence
	Cipher       *functionsecret.Cipher
	WorkerID     string
	PollInterval time.Duration
	LeaseAge     time.Duration
	Logger       *slog.Logger
}

func NewWorker(store Persistence, cipher *functionsecret.Cipher, workerID string, logger *slog.Logger) (*Worker, error) {
	if store == nil || cipher == nil || strings.TrimSpace(workerID) == "" {
		return nil, errors.New("invalid monitoring worker dependencies")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{Store: store, Cipher: cipher, WorkerID: workerID, PollInterval: defaultPollInterval, LeaseAge: defaultLeaseAge, Logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Cipher == nil || strings.TrimSpace(w.WorkerID) == "" {
		return errors.New("monitoring worker is not configured")
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
		if _, err := w.Store.RequeueStaleAdminMonitors(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			if w.Logger != nil {
				w.Logger.Error("admin monitor lease recovery failed", "error", safeWorkerError(err))
			}
		}
		processed, err := w.RunOnce(ctx, leaseAge)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			if w.Logger != nil {
				w.Logger.Error("admin monitor check failed", "error", safeWorkerError(err))
			}
		}
		// A claimed job whose result could not be persisted is still leased. Do
		// not immediately claim/process again: wait for the lease recovery path
		// and avoid a hot loop while the database is unavailable.
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

func (w *Worker) RunOnce(ctx context.Context, leaseAge time.Duration) (bool, error) {
	job, err := w.Store.ClaimNextAdminMonitor(ctx, w.WorkerID, leaseAge)
	if errors.Is(err, repository.ErrNoAdminMonitor) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result := Check(ctx, job, w.Cipher)
	if err := w.Store.CompleteAdminMonitorCheck(ctx, job.ID, w.WorkerID, result); err != nil {
		return true, err
	}
	return true, nil
}

func safeWorkerError(err error) string {
	if err == nil {
		return "monitor worker failed"
	}
	value := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", " "), "\r", " "))
	if len(value) > 240 {
		value = value[:240]
	}
	if value == "" {
		return "monitor worker failed"
	}
	return value
}
