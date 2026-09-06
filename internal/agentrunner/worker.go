// Package agentrunner owns the trusted side of the Agent run queue. The API
// only persists prompts; this package is the sole place that may claim a run
// and hand it to a provider adapter.
package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/repository"
)

const (
	defaultPollInterval   = 500 * time.Millisecond
	defaultLeaseAge       = 20 * time.Minute
	defaultExecutionLimit = 15 * time.Minute
	maxProviderID         = 64
	maxPublicError        = 1000
)

var (
	ErrInvalidAdapter = errors.New("invalid Agent provider adapter")
	ErrInvalidWorker  = errors.New("invalid Agent worker")
)

// PublicError is the only adapter error whose message is persisted into the
// user-visible AgentRun. Provider adapters should use a deliberately bounded,
// secret-free message here; arbitrary errors are replaced with a generic
// failure below.
type PublicError struct {
	Message string
}

func (e *PublicError) Error() string {
	if e == nil {
		return "agent provider execution failed"
	}
	return e.Message
}

// Job is the bounded data a provider adapter receives. Credentials and raw
// repository contents are intentionally absent; an adapter must obtain any
// provider credential from its trusted process-local configuration.
type Job struct {
	Run   domain.AgentRun
	Agent domain.Agent
}

// Adapter is implemented by a trusted provider integration. It must return a
// terminal repository result; it must never mutate the database directly.
type Adapter interface {
	Execute(context.Context, Job) (repository.AgentRunResult, error)
}

// AdapterFunc makes deterministic provider adapters easy to inject in tests.
type AdapterFunc func(context.Context, Job) (repository.AgentRunResult, error)

func (f AdapterFunc) Execute(ctx context.Context, job Job) (repository.AgentRunResult, error) {
	return f(ctx, job)
}

// Registry maps fixed provider identifiers to trusted adapters. It is safe to
// populate during startup and resolve from the worker loop.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

func (r *Registry) Register(provider string, adapter Adapter) error {
	provider, err := normalizeProvider(provider)
	if err != nil || adapter == nil {
		return ErrInvalidAdapter
	}
	if r == nil {
		return ErrInvalidAdapter
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapters == nil {
		r.adapters = make(map[string]Adapter)
	}
	r.adapters[provider] = adapter
	return nil
}

func (r *Registry) Resolve(provider string) Adapter {
	provider, err := normalizeProvider(provider)
	if err != nil || r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.adapters[provider]
}

// Providers returns normalized provider identifiers in deterministic order.
func (r *Registry) Providers() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]string, 0, len(r.adapters))
	for provider := range r.adapters {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

type Worker struct {
	Repository       *repository.Repository
	WorkerID         string
	Adapters         *Registry
	PollInterval     time.Duration
	LeaseAge         time.Duration
	ExecutionTimeout time.Duration
	Logger           *slog.Logger
	Metrics          *observability.WorkerMetrics
}

func New(repo *repository.Repository, workerID string, adapters *Registry, logger *slog.Logger) (*Worker, error) {
	if repo == nil || adapters == nil {
		return nil, ErrInvalidWorker
	}
	if !validWorkerID(workerID) {
		return nil, ErrInvalidWorker
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Repository:       repo,
		WorkerID:         workerID,
		Adapters:         adapters,
		PollInterval:     defaultPollInterval,
		LeaseAge:         defaultLeaseAge,
		ExecutionTimeout: defaultExecutionLimit,
		Logger:           logger,
		Metrics:          observability.NewWorkerMetrics(),
	}, nil
}

// Run keeps queue repair and provider execution outside the public API. With
// an empty registry it remains a harmless queue-only loop and never claims a
// run, which is the safe default for installations without provider workers.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Repository == nil || w.Adapters == nil {
		return ErrInvalidWorker
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
		if metrics := w.Metrics; metrics != nil {
			metrics.AgentPolls.Inc()
		}
		if requeued, err := w.Repository.RequeueStaleAgentRuns(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) {
			w.observeError("requeue")
			w.Logger.Error("requeue stale Agent runs failed", "error", err)
		} else if requeued > 0 {
			if metrics := w.Metrics; metrics != nil {
				metrics.AgentRequeued.Add(float64(requeued))
			}
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			w.observeError("run")
			w.Logger.Error("Agent run failed", "error", err)
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

// RunOnce claims and processes one run for a registered provider. Unknown
// providers remain queued because they are excluded from the atomic claim.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.Repository == nil || w.Adapters == nil {
		return false, ErrInvalidWorker
	}
	providers := w.Adapters.Providers()
	if len(providers) == 0 {
		return false, nil
	}
	job, err := w.Repository.ClaimNextAgentRunForProviders(ctx, w.WorkerID, providers)
	if errors.Is(err, repository.ErrNoAgentRunJob) {
		return false, nil
	}
	if err != nil {
		w.observeError("claim")
		return false, err
	}
	if metrics := w.Metrics; metrics != nil {
		metrics.AgentJobsClaimed.Inc()
		metrics.AgentInFlight.Inc()
		defer metrics.AgentInFlight.Dec()
	}
	started := time.Now()
	defer func() {
		if metrics := w.Metrics; metrics != nil {
			// The terminal result is recorded after the adapter below; this
			// duration is still useful for all claimed jobs.
			metrics.AgentJobDuration.WithLabelValues("finished").Observe(time.Since(started).Seconds())
		}
	}()
	projectID, agentID, runID, err := parseJobIDs(job)
	if err != nil {
		w.observeError("job_identity")
		return true, err
	}
	if err := w.appendLog(ctx, job, "info", "trusted Agent worker claimed run"); err != nil && !errors.Is(err, context.Canceled) {
		w.observeError("log")
		w.Logger.Error("append Agent run claim log failed", "error", err)
	}
	adapter := w.Adapters.Resolve(job.Agent.Provider)
	if adapter == nil {
		return w.finishFailure(ctx, job, errors.New("provider adapter became unavailable"))
	}
	executionTimeout := w.ExecutionTimeout
	if executionTimeout <= 0 {
		executionTimeout = defaultExecutionLimit
	}
	executionContext, cancel := context.WithTimeout(ctx, executionTimeout)
	result, executeErr := adapter.Execute(executionContext, Job{Run: job.Run, Agent: job.Agent})
	cancel()
	if executeErr != nil {
		if ctx.Err() != nil {
			// Keep the lease for stale-run repair when the worker is shutting
			// down; do not turn an interrupted provider call into a fake failure.
			return true, ctx.Err()
		}
		return w.finishFailure(ctx, job, executeErr)
	}
	finished, err := w.Repository.TransitionAgentRun(ctx, projectID, agentID, runID, w.WorkerID, result)
	if err != nil {
		w.observeError("transition")
		return true, err
	}
	if metrics := w.Metrics; metrics != nil {
		metrics.AgentJobsCompleted.WithLabelValues(finished.Status).Inc()
	}
	if logErr := w.appendLog(ctx, job, "info", "trusted Agent worker persisted run result"); logErr != nil && !errors.Is(logErr, context.Canceled) {
		w.observeError("log")
		w.Logger.Error("append Agent run result log failed", "error", logErr)
	}
	return true, nil
}

func (w *Worker) finishFailure(ctx context.Context, job repository.AgentRunJob, cause error) (bool, error) {
	message := publicFailure(cause)
	result := repository.AgentRunResult{Status: "failed", ErrorMessage: &message}
	projectID, agentID, runID, err := parseJobIDs(job)
	if err != nil {
		return true, err
	}
	if err := w.appendLog(ctx, job, "error", message); err != nil && !errors.Is(err, context.Canceled) {
		w.observeError("log")
	}
	finished, err := w.Repository.TransitionAgentRun(ctx, projectID, agentID, runID, w.WorkerID, result)
	if err != nil {
		w.observeError("transition")
		return true, err
	}
	if metrics := w.Metrics; metrics != nil {
		metrics.AgentJobsCompleted.WithLabelValues(finished.Status).Inc()
	}
	return true, nil
}

func (w *Worker) appendLog(ctx context.Context, job repository.AgentRunJob, level, message string) error {
	projectID, agentID, runID, err := parseJobIDs(job)
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = w.Repository.AppendAgentRunLog(ctx, projectID, agentID, runID, w.WorkerID, id, 0, level, message)
	return err
}

func parseJobIDs(job repository.AgentRunJob) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	projectID, err := uuid.Parse(job.Run.ProjectID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("invalid Agent project id: %w", err)
	}
	agentID, err := uuid.Parse(job.Run.AgentID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("invalid Agent id: %w", err)
	}
	runID, err := uuid.Parse(job.Run.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("invalid Agent run id: %w", err)
	}
	return projectID, agentID, runID, nil
}

func (w *Worker) observeError(operation string) {
	if w != nil && w.Metrics != nil {
		w.Metrics.AgentErrors.WithLabelValues(operation).Inc()
	}
}

func normalizeProvider(provider string) (string, error) {
	if strings.ContainsAny(provider, "\x00\r\n\t") {
		return "", ErrInvalidAdapter
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || utf8.RuneCountInString(provider) > maxProviderID {
		return "", ErrInvalidAdapter
	}
	return provider, nil
}

func publicFailure(err error) string {
	message := "agent provider execution failed"
	var publicErr *PublicError
	if errors.As(err, &publicErr) && publicErr != nil {
		message = strings.TrimSpace(publicErr.Message)
	} else if errors.Is(err, context.DeadlineExceeded) {
		message = "agent provider execution timed out"
	} else if errors.Is(err, context.Canceled) {
		message = "agent provider execution was cancelled"
	}
	if message == "" {
		return "agent provider execution failed"
	}
	message = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\x00' {
			return ' '
		}
		return r
	}, message)
	if utf8.RuneCountInString(message) > maxPublicError {
		message = string([]rune(message)[:maxPublicError])
	}
	return message
}

func validWorkerID(workerID string) bool {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 128 || strings.ContainsAny(workerID, "\x00\r\n\t") {
		return false
	}
	for _, character := range workerID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
