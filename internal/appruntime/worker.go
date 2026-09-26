// Package appruntime reconciles durable App desired state with the isolated
// Moby runtime. Docker remains an implementation detail owned by this worker.
package appruntime

import (
	"context"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/appsecret"
	"github.com/Stealth-deplover/stealth/internal/appstore"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultWorkerPoll       = time.Second
	defaultWorkerLease      = 2 * time.Minute
	defaultOrphanSweepEvery = time.Minute
	maxRuntimeRetry         = time.Minute
	maxCleanupAttempts      = 20
)

var (
	ErrImageArtifactUnavailable   = errors.New("App image artifact is unavailable")
	ErrUnsupportedRuntimePlatform = errors.New("App runtime platform is unsupported")
	ErrAppEnvironmentDecryption   = errors.New("App runtime environment could not be decrypted")
	appRuntimeEnvironmentKey      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,119}$`)
)

type Persistence interface {
	ScheduleAppRuntimeStartupSweep(context.Context) error
	RequeueStaleAppRuntimeLeases(context.Context) (int64, error)
	ClaimNextAppRuntime(context.Context, string, time.Duration) (repository.AppRuntimeJob, error)
	ListAppRuntimeEnvironment(context.Context, repository.AppRuntimeJob) ([]repository.AppRuntimeEnvironmentCiphertext, error)
	RenewAppRuntimeLease(context.Context, uuid.UUID, string, uuid.UUID, time.Duration) error
	IsAppRuntimeJobCurrent(context.Context, repository.AppRuntimeJob) (bool, error)
	ReleaseAppRuntimeJob(context.Context, repository.AppRuntimeJob) error
	CompleteAppRuntime(context.Context, repository.AppRuntimeJob, string, *repository.AppRuntimeContainer) error
	ResetAppHealthBeforeRuntimeRestart(context.Context, repository.AppRuntimeJob, uuid.UUID) error
	RotateAppRuntimeIdentityBeforeCreate(context.Context, repository.AppRuntimeJob, uuid.UUID) error
	FailAppRuntime(context.Context, repository.AppRuntimeJob, string, string, time.Time) error
	ClaimNextAppHealthCheck(context.Context, string, time.Duration) (repository.AppHealthCheckJob, error)
	IsAppHealthCheckCurrent(context.Context, repository.AppHealthCheckJob) (bool, error)
	CompleteAppHealthCheck(context.Context, repository.AppHealthCheckJob, bool) error
	InvalidateAppHealthIdentity(context.Context, repository.AppHealthCheckJob) error
	ReleaseAppHealthCheck(context.Context, repository.AppHealthCheckJob) error
	AppRuntimeContainerExists(context.Context, uuid.UUID, uuid.UUID, string) (bool, error)
	QueueAppRuntimeCleanup(context.Context, *uuid.UUID, uuid.UUID, string, string, int) error
	ClaimNextAppRuntimeCleanup(context.Context, string, time.Duration) (repository.AppRuntimeCleanupJob, error)
	RenewAppRuntimeCleanupLease(context.Context, repository.AppRuntimeCleanupJob, time.Duration) error
	CompleteAppRuntimeCleanup(context.Context, repository.AppRuntimeCleanupJob) error
	FailAppRuntimeCleanup(context.Context, repository.AppRuntimeCleanupJob, string, time.Time, bool) error
}

var _ Persistence = (*repository.Repository)(nil)

type Runtime interface {
	EnsureNetwork(context.Context) error
	EnsureRuntimeNetworkPeers(context.Context) error
	EnsureImage(context.Context, ociartifact.ImageInfo, io.ReadSeeker, string) (Image, error)
	InspectApp(context.Context, uuid.UUID) (Container, bool, error)
	ProbeApp(context.Context, repository.AppHealthCheckJob, Container) error
	CreateApp(context.Context, repository.AppRuntimeJob, Image, []RuntimeEnvironmentVariable) (Container, error)
	RenameApp(context.Context, repository.AppRuntimeJob, string, string) (Container, error)
	StartApp(context.Context, repository.AppRuntimeJob, string) (Container, error)
	RemoveApp(context.Context, repository.AppRuntimeJob, string) error
	RemoveCleanupTarget(context.Context, repository.AppRuntimeCleanupJob) error
	ListManagedAppContainers(context.Context) ([]Container, error)
}

type Worker struct {
	Store             Persistence
	Artifacts         *appstore.Store
	Runtime           Runtime
	AppSecretsCipher  *appsecret.Cipher
	WorkerID          string
	PollInterval      time.Duration
	LeaseAge          time.Duration
	MaxImageBytes     int64
	OrphanSweepEvery  time.Duration
	Logger            *slog.Logger
	Metrics           *observability.WorkerMetrics
	startSweepDone    bool
	networkRetryAfter time.Time
	lastOrphanSweep   time.Time
	startupSweepMutex sync.Mutex
}

func New(store Persistence, artifacts *appstore.Store, runtime Runtime, workerID string, pollInterval, leaseAge time.Duration, maxImageBytes int64, logger *slog.Logger) (*Worker, error) {
	if store == nil || artifacts == nil || artifacts.Images == nil || runtime == nil || !safeWorkerID(workerID) || maxImageBytes < 1 {
		return nil, errors.New("invalid App runtime worker dependencies")
	}
	if pollInterval <= 0 {
		pollInterval = defaultWorkerPoll
	}
	if pollInterval < 100*time.Millisecond || pollInterval > time.Minute {
		return nil, errors.New("App runtime poll interval is outside its supported range")
	}
	if leaseAge <= 0 {
		leaseAge = defaultWorkerLease
	}
	if leaseAge < 30*time.Second || leaseAge > 10*time.Minute {
		return nil, errors.New("App runtime lease age is outside its supported range")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Store: store, Artifacts: artifacts, Runtime: runtime, WorkerID: workerID,
		PollInterval: pollInterval, LeaseAge: leaseAge, MaxImageBytes: maxImageBytes,
		OrphanSweepEvery: defaultOrphanSweepEvery, Logger: logger,
		Metrics: observability.NewWorkerMetrics(),
	}, nil
}

// Run never returns a transient database or Moby error to workersupervisor.
// The queue remains durable and is retried on the next polling interval.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Runtime == nil || w.Artifacts == nil {
		return errors.New("App runtime worker is not configured")
	}
	interval := w.PollInterval
	if interval <= 0 {
		interval = defaultWorkerPoll
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		processed, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			w.Logger.Error("App runtime reconciliation iteration failed", "error", err)
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

// RunOnce performs bounded queue work and is exported for deterministic
// worker tests. It processes at most one cleanup or App job.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.Store == nil || w.Runtime == nil || w.Artifacts == nil {
		return false, errors.New("App runtime worker is not configured")
	}
	if w.Metrics != nil {
		w.Metrics.AppRuntimePolls.Inc()
	}
	if err := w.ensureStartupSweep(ctx); err != nil {
		w.Logger.Warn("App runtime startup recovery scheduling failed", "error", err)
	}
	if time.Now().After(w.networkRetryAfter) {
		if err := w.Runtime.EnsureNetwork(ctx); err != nil {
			if ctx.Err() == nil {
				w.Logger.Warn("App runtime network is unavailable", "error", err)
				w.networkRetryAfter = time.Now().Add(30 * time.Second)
			}
		} else {
			if peerErr := w.Runtime.EnsureRuntimeNetworkPeers(ctx); peerErr != nil {
				if ctx.Err() == nil {
					w.Logger.Warn("App routing network peers are unavailable", "error", safeRuntimeError(peerErr))
					w.networkRetryAfter = time.Now().Add(30 * time.Second)
				}
			} else {
				w.networkRetryAfter = time.Now().Add(30 * time.Second)
			}
		}
	}
	if requeued, err := w.Store.RequeueStaleAppRuntimeLeases(ctx); err != nil && ctx.Err() == nil {
		w.Logger.Warn("requeue stale App runtime leases failed", "error", err)
	} else if requeued > 0 && w.Metrics != nil {
		w.Metrics.AppRuntimeRequeued.Add(float64(requeued))
	}
	if err := w.sweepOrphansIfDue(ctx); err != nil && ctx.Err() == nil {
		w.Logger.Warn("App runtime orphan sweep failed", "error", err)
	}

	cleanup, err := w.Store.ClaimNextAppRuntimeCleanup(ctx, w.WorkerID, w.LeaseAge)
	if err == nil {
		return true, w.processCleanup(ctx, cleanup)
	}
	if !errors.Is(err, repository.ErrNoAppRuntimeCleanup) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, err
	}

	job, err := w.Store.ClaimNextAppRuntime(ctx, w.WorkerID, w.LeaseAge)
	if errors.Is(err, repository.ErrNoAppRuntimeJob) {
		healthJob, healthErr := w.Store.ClaimNextAppHealthCheck(ctx, w.WorkerID, w.LeaseAge)
		if errors.Is(healthErr, repository.ErrNoAppHealthCheckJob) {
			return false, nil
		}
		if healthErr != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, healthErr
		}
		return true, w.processHealthCheck(ctx, healthJob)
	}
	if err != nil {
		return false, err
	}
	if w.Metrics != nil {
		w.Metrics.AppRuntimeJobsClaimed.Inc()
	}
	return true, w.processApp(ctx, job)
}

func (w *Worker) withAppHeartbeat(parent context.Context, job repository.AppRuntimeJob, action func(context.Context) error) error {
	appID := uuid.MustParse(job.App.ID)
	return w.withHeartbeat(parent, func(ctx context.Context) error {
		return w.Store.RenewAppRuntimeLease(ctx, appID, job.WorkerID, job.LeaseToken, w.LeaseAge)
	}, action)
}

func (w *Worker) withCleanupHeartbeat(parent context.Context, job repository.AppRuntimeCleanupJob, action func(context.Context) error) error {
	return w.withHeartbeat(parent, func(ctx context.Context) error {
		return w.Store.RenewAppRuntimeCleanupLease(ctx, job, w.LeaseAge)
	}, action)
}

func (w *Worker) withHeartbeat(parent context.Context, renew func(context.Context) error, action func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	interval := w.LeaseAge / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	stop := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	var heartbeatErr error
	var heartbeatMu sync.Mutex
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(ctx, min(10*time.Second, interval))
				err := renew(renewCtx)
				renewCancel()
				if err != nil {
					heartbeatMu.Lock()
					heartbeatErr = err
					heartbeatMu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	err := action(ctx)
	once.Do(func() { close(stop) })
	<-finished
	heartbeatMu.Lock()
	defer heartbeatMu.Unlock()
	if heartbeatErr != nil {
		return heartbeatErr
	}
	return err
}

func safeWorkerID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'A' && character <= 'Z') && !(character >= 'a' && character <= 'z') &&
			!(character >= '0' && character <= '9') && !strings.ContainsRune("._-", character) {
			return false
		}
	}
	return true
}
