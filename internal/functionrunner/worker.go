package functionrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/functionstore"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

const (
	defaultWorkerPoll     = 500 * time.Millisecond
	defaultLeaseAge       = 20 * time.Minute
	defaultBuildTimeout   = 15 * time.Minute
	defaultStagingRoot    = "/var/lib/stealth/runner-staging"
	defaultSourceDirMode  = 0o700
	maxFailureMessageSize = 4000
)

// RuntimeExecutor is intentionally narrower than DockerExecutor. Tests can
// provide a deterministic fake while production uses Docker with the
// restrictions in docker.go.
type RuntimeExecutor interface {
	Execute(context.Context, repository.FunctionExecutionJob, string, []repository.FunctionRuntimeVariable) (ExecutionResult, error)
}

// BuildExecutor is implemented by the production Docker executor. Keeping it
// separate from RuntimeExecutor lets tests and alternate runners continue to
// execute already-built artifacts without needing a container builder.
type BuildExecutor interface {
	Build(context.Context, repository.FunctionBuildJob, string, []repository.FunctionRuntimeVariable, io.Writer) error
}

type Worker struct {
	BuildStore     repository.FunctionBuildStore
	ExecutionStore repository.FunctionExecutionStore
	Store          *functionstore.Store
	Cipher         *functionsecret.Cipher
	Executor       RuntimeExecutor
	Builder        BuildExecutor
	WorkerID       string
	StagingRoot    string
	ArchiveLimit   ArchiveLimits
	PollInterval   time.Duration
	LeaseAge       time.Duration
	BuildTimeout   time.Duration
	Logger         *slog.Logger
	Metrics        *observability.WorkerMetrics
}

func NewWorker(persistence repository.FunctionWorkerStore, store *functionstore.Store, cipher *functionsecret.Cipher, executor RuntimeExecutor, workerID, stagingRoot string, logger *slog.Logger) (*Worker, error) {
	if persistence == nil || store == nil || cipher == nil || executor == nil || !validWorkerID(workerID) {
		return nil, fmt.Errorf("invalid function worker dependencies")
	}
	if strings.TrimSpace(stagingRoot) == "" {
		stagingRoot = defaultStagingRoot
	}
	stagingRoot, err := filepath.Abs(stagingRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve function worker staging root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stagingRoot, "jobs"), defaultSourceDirMode); err != nil {
		return nil, fmt.Errorf("create function worker staging root: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	builder, _ := executor.(BuildExecutor)
	return &Worker{BuildStore: persistence, ExecutionStore: persistence, Store: store, Cipher: cipher, Executor: executor, Builder: builder, WorkerID: workerID, StagingRoot: filepath.Clean(stagingRoot), ArchiveLimit: ArchiveLimits{}, PollInterval: defaultWorkerPoll, LeaseAge: defaultLeaseAge, BuildTimeout: defaultBuildTimeout, Logger: logger, Metrics: observability.NewWorkerMetrics()}, nil
}

// Run polls until ctx is cancelled. RequeueStaleFunctionExecutions is called
// before each poll so a crashed worker does not leave accepted work blocked.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.BuildStore == nil || w.ExecutionStore == nil {
		return errors.New("function worker is not configured")
	}
	poll := w.PollInterval
	if poll <= 0 {
		poll = defaultWorkerPoll
	}
	leaseAge := w.LeaseAge
	if leaseAge <= 0 {
		leaseAge = defaultLeaseAge
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if metrics := w.Metrics; metrics != nil {
			metrics.Polls.Inc()
		}
		if requeued, err := w.BuildStore.RequeueStaleFunctionDeployments(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) {
			if metrics := w.Metrics; metrics != nil {
				metrics.Errors.WithLabelValues("requeue_build").Inc()
			}
			w.Logger.Error("requeue stale function builds failed", "error", err)
		} else if requeued > 0 {
			if metrics := w.Metrics; metrics != nil {
				metrics.BuildRequeued.Add(float64(requeued))
			}
		}
		if requeued, err := w.ExecutionStore.RequeueStaleFunctionExecutions(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) {
			if metrics := w.Metrics; metrics != nil {
				metrics.Errors.WithLabelValues("requeue").Inc()
			}
			w.Logger.Error("requeue stale function executions failed", "error", err)
		} else if requeued > 0 {
			if metrics := w.Metrics; metrics != nil {
				metrics.Requeued.Add(float64(requeued))
			}
		}
		built, buildErr := w.RunBuildOnce(ctx)
		if buildErr != nil {
			if errors.Is(buildErr, context.Canceled) || errors.Is(buildErr, context.DeadlineExceeded) {
				return nil
			}
			w.Logger.Error("function build failed", "error", buildErr)
		}
		if built {
			continue
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			w.Logger.Error("function execution failed", "error", err)
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

func (w *Worker) MetricsHandler() http.Handler {
	if w == nil || w.Metrics == nil {
		return http.NotFoundHandler()
	}
	return w.Metrics.Handler()
}

func validWorkerID(value string) bool {
	if value == "" || len(value) > 128 {
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

func redactFailure(message string, secrets []string) string {
	message = Redact(message, secrets)
	message, _ = executionErrorText(message)
	if strings.TrimSpace(message) == "" {
		return "function execution failed"
	}
	return message
}

func mustUUID(value string) uuid.UUID {
	parsed, _ := uuid.Parse(value)
	return parsed
}
