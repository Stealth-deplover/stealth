// Package realtimepublisher publishes durable PostgreSQL outbox rows to the
// ephemeral Redis fanout used by SSE. PostgreSQL remains the recovery source.
package realtimepublisher

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/realtime"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/retry"
)

const (
	defaultPollInterval = 500 * time.Millisecond
	defaultLeaseAge     = 20 * time.Minute
	defaultMaxAttempts  = 12
	defaultRetryBase    = time.Second
	defaultRetryMaximum = time.Minute
	pendingMetricPeriod = 15 * time.Second
)

type Worker struct {
	Repository   *repository.Repository
	Broker       *realtime.Broker
	WorkerID     string
	PollInterval time.Duration
	LeaseAge     time.Duration
	MaxAttempts  int
	Logger       *slog.Logger
	Metrics      *observability.WorkerMetrics
}

func New(repo *repository.Repository, broker *realtime.Broker, workerID string, logger *slog.Logger) (*Worker, error) {
	if repo == nil || broker == nil || strings.TrimSpace(workerID) == "" {
		return nil, errors.New("invalid realtime publisher dependencies")
	}
	if len(workerID) > 128 {
		return nil, errors.New("invalid realtime publisher worker id")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Repository:   repo,
		Broker:       broker,
		WorkerID:     workerID,
		PollInterval: defaultPollInterval,
		LeaseAge:     defaultLeaseAge,
		MaxAttempts:  defaultMaxAttempts,
		Logger:       logger,
		Metrics:      observability.NewWorkerMetrics(),
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Repository == nil || w.Broker == nil {
		return errors.New("realtime publisher is not configured")
	}
	pollInterval := w.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	leaseAge := w.LeaseAge
	if leaseAge <= 0 {
		leaseAge = defaultLeaseAge
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastPendingMetric := time.Time{}
	for {
		if time.Since(lastPendingMetric) >= pendingMetricPeriod {
			w.refreshPendingMetric(ctx)
			lastPendingMetric = time.Now()
		}
		if _, err := w.Repository.RequeueStaleRealtimeEvents(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) {
			w.logError("requeue stale realtime events failed", err)
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			w.logError("realtime publication failed", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) refreshPendingMetric(ctx context.Context) {
	if w.Metrics == nil {
		return
	}
	pending, err := w.Repository.PendingRealtimeEvents(ctx)
	if err != nil {
		w.logError("read realtime outbox depth failed", err)
		return
	}
	w.Metrics.OutboxPending.Set(float64(pending))
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	leaseAge := w.LeaseAge
	if leaseAge <= 0 {
		leaseAge = defaultLeaseAge
	}
	job, err := w.Repository.ClaimNextRealtimeEvent(ctx, w.WorkerID, leaseAge)
	if errors.Is(err, repository.ErrNoRealtimeEvent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if w.Metrics != nil {
		w.Metrics.OutboxPublishAttempts.Inc()
	}
	if !realtime.ShouldFanout(job.EventName) {
		if finishErr := w.Repository.FinishRealtimeEvent(ctx, job.EventID, w.WorkerID, true, nil, ""); finishErr != nil {
			return true, finishErr
		}
		if w.Metrics != nil {
			w.Metrics.OutboxSkipped.Inc()
		}
		return true, nil
	}
	started := time.Now()
	if !json.Valid(job.Payload) {
		if w.Metrics != nil {
			w.Metrics.OutboxFailed.Inc()
			w.Metrics.OutboxPublishDuration.Observe(time.Since(started).Seconds())
		}
		malformedErr := errors.New("realtime event payload is malformed")
		if finishErr := w.Repository.FinishRealtimeEvent(ctx, job.EventID, w.WorkerID, false, nil, malformedErr.Error()); finishErr != nil {
			return true, errors.Join(malformedErr, finishErr)
		}
		return true, malformedErr
	}
	err = w.Broker.Publish(ctx, job.ProjectID.String(), job.Payload)
	if err == nil {
		finishErr := w.Repository.FinishRealtimeEvent(ctx, job.EventID, w.WorkerID, true, nil, "")
		if finishErr != nil {
			return true, finishErr
		}
		if w.Metrics != nil {
			w.Metrics.OutboxPublished.Inc()
			w.Metrics.OutboxPublishDuration.Observe(time.Since(started).Seconds())
		}
		return true, nil
	}
	if w.Metrics != nil {
		w.Metrics.OutboxFailed.Inc()
		w.Metrics.OutboxPublishDuration.Observe(time.Since(started).Seconds())
	}
	if w.Logger != nil {
		correlationID := ""
		eventType := job.EventName
		if envelope, decodeErr := realtime.Unmarshal(job.Payload); decodeErr == nil {
			eventType = envelope.Type
			correlationID = envelope.CorrelationID
		}
		w.Logger.Warn("realtime event publication failed", "event_id", job.EventID, "event_type", eventType, "project_id", job.ProjectID, "correlation_id", correlationID, "attempt", job.AttemptCount, "error", err)
	}
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	var retryAt *time.Time
	if job.AttemptCount < maxAttempts {
		next := time.Now().Add(retry.Exponential(job.AttemptCount, defaultRetryBase, defaultRetryMaximum))
		retryAt = &next
	}
	finishErr := w.Repository.FinishRealtimeEvent(ctx, job.EventID, w.WorkerID, false, retryAt, err.Error())
	if finishErr != nil {
		return true, errors.Join(err, finishErr)
	}
	return true, err
}

func (w *Worker) logError(message string, err error) {
	if w.Logger == nil {
		return
	}
	w.Logger.Error(message, "error", err)
}
