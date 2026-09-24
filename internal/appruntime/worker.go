// Package appruntime reconciles durable App desired state with the isolated
// Moby runtime. Docker remains an implementation detail owned by this worker.
package appruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appstore"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
)

type Persistence interface {
	ScheduleAppRuntimeStartupSweep(context.Context) error
	RequeueStaleAppRuntimeLeases(context.Context) (int64, error)
	ClaimNextAppRuntime(context.Context, string, time.Duration) (repository.AppRuntimeJob, error)
	RenewAppRuntimeLease(context.Context, uuid.UUID, string, uuid.UUID, time.Duration) error
	IsAppRuntimeJobCurrent(context.Context, repository.AppRuntimeJob) (bool, error)
	ReleaseAppRuntimeJob(context.Context, repository.AppRuntimeJob) error
	CompleteAppRuntime(context.Context, repository.AppRuntimeJob, string, *repository.AppRuntimeContainer) error
	FailAppRuntime(context.Context, repository.AppRuntimeJob, string, string, time.Time) error
	AppRuntimeContainerExists(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	QueueAppRuntimeCleanup(context.Context, *uuid.UUID, uuid.UUID, string, string, int) error
	ClaimNextAppRuntimeCleanup(context.Context, string, time.Duration) (repository.AppRuntimeCleanupJob, error)
	RenewAppRuntimeCleanupLease(context.Context, repository.AppRuntimeCleanupJob, time.Duration) error
	CompleteAppRuntimeCleanup(context.Context, repository.AppRuntimeCleanupJob) error
	FailAppRuntimeCleanup(context.Context, repository.AppRuntimeCleanupJob, string, time.Time, bool) error
}

var _ Persistence = (*repository.Repository)(nil)

type Runtime interface {
	EnsureNetwork(context.Context) error
	EnsureImage(context.Context, ociartifact.ImageInfo, io.ReadSeeker, string) (Image, error)
	InspectApp(context.Context, uuid.UUID) (Container, bool, error)
	CreateApp(context.Context, repository.AppRuntimeJob, Image) (Container, error)
	StartApp(context.Context, repository.AppRuntimeJob, string) (Container, error)
	RemoveApp(context.Context, repository.AppRuntimeJob, string) error
	RemoveCleanupTarget(context.Context, repository.AppRuntimeCleanupJob) error
	ListManagedAppContainers(context.Context) ([]Container, error)
}

type Worker struct {
	Store             Persistence
	Artifacts         *appstore.Store
	Runtime           Runtime
	WorkerID          string
	PollInterval      time.Duration
	LeaseAge          time.Duration
	MaxImageBytes     int64
	OrphanSweepEvery  time.Duration
	Logger            *slog.Logger
	Metrics           *observability.WorkerMetrics
	startSweepDone    bool
	networkReady      bool
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
	if !w.networkReady && time.Now().After(w.networkRetryAfter) {
		if err := w.Runtime.EnsureNetwork(ctx); err != nil {
			if ctx.Err() == nil {
				w.Logger.Warn("App runtime network is unavailable", "error", err)
				w.networkRetryAfter = time.Now().Add(30 * time.Second)
			}
		} else {
			w.networkReady = true
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
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if w.Metrics != nil {
		w.Metrics.AppRuntimeJobsClaimed.Inc()
	}
	return true, w.processApp(ctx, job)
}

func (w *Worker) ensureStartupSweep(ctx context.Context) error {
	w.startupSweepMutex.Lock()
	defer w.startupSweepMutex.Unlock()
	if w.startSweepDone {
		return nil
	}
	if err := w.Store.ScheduleAppRuntimeStartupSweep(ctx); err != nil {
		return err
	}
	w.startSweepDone = true
	return nil
}

func (w *Worker) sweepOrphansIfDue(ctx context.Context) error {
	interval := w.OrphanSweepEvery
	if interval <= 0 {
		interval = defaultOrphanSweepEvery
	}
	if !w.lastOrphanSweep.IsZero() && time.Since(w.lastOrphanSweep) < interval {
		return nil
	}
	containers, err := w.Runtime.ListManagedAppContainers(ctx)
	if err != nil {
		return err
	}
	for _, container := range containers {
		if !managedAppContainer(container) {
			continue
		}
		appID, projectID, validIdentity := validManagedAppContainerIdentity(container)
		if !validIdentity {
			w.Logger.Warn("ignored managed App container with invalid ownership labels")
			continue
		}
		exists, err := w.Store.AppRuntimeContainerExists(ctx, projectID, appID)
		if err != nil {
			return err
		}
		if exists && container.Name == "/"+repository.AppRuntimeContainerName(appID) {
			continue
		}
		projectCopy := projectID
		if err := w.Store.QueueAppRuntimeCleanup(ctx, &projectCopy, appID, container.ID, container.Name, 15); err != nil {
			return err
		}
		if w.Metrics != nil {
			w.Metrics.AppRuntimeOrphansQueued.Inc()
		}
	}
	w.lastOrphanSweep = time.Now()
	return nil
}

func (w *Worker) processApp(parent context.Context, job repository.AppRuntimeJob) error {
	appID, _ := uuid.Parse(job.App.ID)
	ctx, span := otel.Tracer("stealth/internal/appruntime").Start(parent, "app.runtime.reconcile")
	span.SetAttributes(attribute.String("app.id", job.App.ID), attribute.String("project.id", job.App.ProjectID), attribute.Int64("app.generation", job.App.DesiredGeneration))
	defer span.End()
	started := time.Now()
	if w.Metrics != nil {
		w.Metrics.AppRuntimeInFlight.Inc()
		defer w.Metrics.AppRuntimeInFlight.Dec()
	}

	err := w.withAppHeartbeat(ctx, job, func(work context.Context) error { return w.reconcile(work, job) })
	if err == nil || errors.Is(err, repository.ErrAppRuntimeStale) || errors.Is(err, repository.ErrAppRuntimeLeaseLost) || errors.Is(err, context.Canceled) {
		if w.Metrics != nil {
			result := "converged"
			if errors.Is(err, repository.ErrAppRuntimeStale) || errors.Is(err, repository.ErrAppRuntimeLeaseLost) {
				result = "stale"
			}
			w.Metrics.AppRuntimeJobsCompleted.WithLabelValues(result).Inc()
			w.Metrics.AppRuntimeDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
		}
		return nil
	}
	if parent.Err() != nil {
		return nil
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "App runtime reconcile failed")
	if w.Metrics != nil {
		w.Metrics.AppRuntimeErrors.WithLabelValues(runtimeErrorClass(err)).Inc()
	}
	status := "degraded"
	if terminalRuntimeError(err) {
		status = "failed"
	}
	message := safeRuntimeError(err)
	if message == "" {
		message = "runtime unavailable"
	}
	retryAt := time.Now().Add(runtimeBackoff(job.FailureCount))
	if failErr := w.Store.FailAppRuntime(parent, job, status, message, retryAt); failErr != nil && !errors.Is(failErr, repository.ErrAppRuntimeStale) && !errors.Is(failErr, repository.ErrAppRuntimeLeaseLost) {
		return errors.Join(err, failErr)
	}
	w.Logger.Warn("App runtime did not converge", "app_id", appID, "generation", job.App.DesiredGeneration, "status", status, "reason", message)
	if w.Metrics != nil {
		w.Metrics.AppRuntimeJobsCompleted.WithLabelValues(status).Inc()
		w.Metrics.AppRuntimeDuration.WithLabelValues(status).Observe(time.Since(started).Seconds())
	}
	return nil
}

func (w *Worker) reconcile(ctx context.Context, job repository.AppRuntimeJob) error {
	appID, err := uuid.Parse(job.App.ID)
	if err != nil {
		return repository.ErrInvalidAppRuntimeJob
	}
	if !job.App.Enabled || job.App.DesiredDeploymentID == nil {
		container, found, err := w.Runtime.InspectApp(ctx, appID)
		if err != nil {
			return err
		}
		if found {
			if !managedForApp(container, appID, uuid.MustParse(job.App.ProjectID)) {
				return ErrRuntimeOwnershipConflict
			}
			if err := w.requireCurrent(ctx, job); err != nil {
				return err
			}
			if err := w.Runtime.RemoveApp(ctx, job, container.ID); err != nil {
				return err
			}
		}
		if err := w.requireCurrent(ctx, job); err != nil {
			return err
		}
		status := "stopped"
		if job.App.Enabled {
			status = "not_deployed"
		}
		return w.Store.CompleteAppRuntime(ctx, job, status, nil)
	}

	if job.Deployment.ID == "" || job.ImagePath == "" || job.Deployment.ImageDigest == nil || job.Deployment.ImageArchiveSHA256 == nil || job.Deployment.ImageSizeBytes == nil || job.Deployment.BuildStatus != "succeeded" || job.Deployment.Status != "ready" {
		return ErrImageArtifactUnavailable
	}
	imageInfo, archive, err := w.inspectPersistedImage(ctx, job)
	if err != nil {
		return err
	}
	defer archive.Close()
	if len(imageInfo.VolumePaths) > 0 {
		return errUnsupportedImageVolumes
	}
	if !supportedRuntimePlatform(job.Deployment.Platform, imageInfo) {
		return ErrUnsupportedRuntimePlatform
	}
	if err := w.Runtime.EnsureNetwork(ctx); err != nil {
		return err
	}
	runtimeTag, err := runtimeTagForJob(job)
	if err != nil {
		return err
	}
	image, err := w.Runtime.EnsureImage(ctx, imageInfo, archive, runtimeTag)
	if err != nil {
		return err
	}

	appID = uuid.MustParse(job.App.ID)
	projectID := uuid.MustParse(job.App.ProjectID)
	container, found, err := w.Runtime.InspectApp(ctx, appID)
	if err != nil {
		return err
	}
	if found && !managedForApp(container, appID, projectID) {
		return ErrRuntimeOwnershipConflict
	}
	if found && ContainerMatchesDesired(container, job, image, w.runtimeNetworkName()) {
		if container.State.Running {
			return w.completeRunning(ctx, job, container, imageInfo, runtimeTag)
		}
		// Docker restart policy is deliberately disabled. Stealth retries an
		// exited process through this durable, backoff-controlled reconcile.
		if err := w.requireCurrent(ctx, job); err != nil {
			return err
		}
		container, err = w.Runtime.StartApp(ctx, job, container.ID)
		if err != nil {
			return errors.Join(ErrContainerStart, err)
		}
		if !ContainerMatchesDesired(container, job, image, w.runtimeNetworkName()) || !container.State.Running {
			return ErrContainerStart
		}
		return w.completeRunning(ctx, job, container, imageInfo, runtimeTag)
	}

	if found {
		if err := w.requireCurrent(ctx, job); err != nil {
			return err
		}
		if err := w.Runtime.RemoveApp(ctx, job, container.ID); err != nil {
			return err
		}
	}
	if err := w.requireCurrent(ctx, job); err != nil {
		return err
	}
	container, err = w.Runtime.CreateApp(ctx, job, image)
	if err != nil {
		return err
	}
	if !managedForApp(container, appID, projectID) || !ContainerMatchesDesired(container, job, image, w.runtimeNetworkName()) {
		return ErrRuntimeOwnershipConflict
	}
	current, currentErr := w.Store.IsAppRuntimeJobCurrent(ctx, job)
	if currentErr != nil {
		return currentErr
	}
	if !current {
		_ = w.Runtime.RemoveApp(ctx, job, container.ID)
		_ = w.Store.ReleaseAppRuntimeJob(ctx, job)
		return repository.ErrAppRuntimeStale
	}
	container, err = w.Runtime.StartApp(ctx, job, container.ID)
	if err != nil {
		return errors.Join(ErrContainerStart, err)
	}
	if !container.State.Running || !ContainerMatchesDesired(container, job, image, w.runtimeNetworkName()) {
		return ErrContainerStart
	}
	return w.completeRunning(ctx, job, container, imageInfo, runtimeTag)
}

func (w *Worker) completeRunning(ctx context.Context, job repository.AppRuntimeJob, container Container, imageInfo ociartifact.ImageInfo, runtimeTag string) error {
	if err := w.requireCurrent(ctx, job); err != nil {
		return err
	}
	imageID := container.ImageID
	if imageID != imageInfo.ConfigDigest {
		return ErrImageVerification
	}
	state := &repository.AppRuntimeContainer{
		ID:          container.ID,
		Name:        repository.AppRuntimeContainerName(uuid.MustParse(job.App.ID)),
		ImageID:     imageID,
		ImageDigest: imageInfo.ManifestDigest,
		RuntimeTag:  runtimeTag,
	}
	return w.Store.CompleteAppRuntime(ctx, job, "running", state)
}

func (w *Worker) inspectPersistedImage(ctx context.Context, job repository.AppRuntimeJob) (ociartifact.ImageInfo, io.ReadSeekCloser, error) {
	archive, err := w.Artifacts.Images.OpenRelative(ctx, job.ImagePath)
	if err != nil {
		return ociartifact.ImageInfo{}, nil, ErrImageArtifactUnavailable
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = archive.Close()
		}
	}()
	archiveSize, err := archive.Seek(0, io.SeekEnd)
	if err != nil || archiveSize < 1 || archiveSize > w.MaxImageBytes || archiveSize != *job.Deployment.ImageSizeBytes {
		return ociartifact.ImageInfo{}, nil, ErrImageVerification
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return ociartifact.ImageInfo{}, nil, ErrImageVerification
	}
	hasher := sha256.New()
	limited := &io.LimitedReader{R: archive, N: archiveSize + 1}
	written, err := io.Copy(hasher, limited)
	if err != nil || written != archiveSize {
		return ociartifact.ImageInfo{}, nil, ErrImageVerification
	}
	if hex.EncodeToString(hasher.Sum(nil)) != strings.ToLower(*job.Deployment.ImageArchiveSHA256) {
		return ociartifact.ImageInfo{}, nil, ErrImageVerification
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return ociartifact.ImageInfo{}, nil, ErrImageVerification
	}
	info, err := ociartifact.Inspect(archive, *job.Deployment.ImageDigest, archiveSize)
	if err != nil {
		return ociartifact.ImageInfo{}, nil, errors.Join(ErrImageVerification, err)
	}
	if info.ManifestDigest != *job.Deployment.ImageDigest {
		return ociartifact.ImageInfo{}, nil, ErrImageVerification
	}
	closeOnError = false
	return info, archive, nil
}

func (w *Worker) completeRunningWithDigest(ctx context.Context, job repository.AppRuntimeJob, container Container, info ociartifact.ImageInfo, runtimeTag string) error {
	return w.completeRunning(ctx, job, container, info, runtimeTag)
}

func (w *Worker) requireCurrent(ctx context.Context, job repository.AppRuntimeJob) error {
	current, err := w.Store.IsAppRuntimeJobCurrent(ctx, job)
	if err != nil {
		return err
	}
	if !current {
		_ = w.Store.ReleaseAppRuntimeJob(ctx, job)
		return repository.ErrAppRuntimeStale
	}
	return nil
}

func (w *Worker) processCleanup(parent context.Context, job repository.AppRuntimeCleanupJob) error {
	started := time.Now()
	if w.Metrics != nil {
		w.Metrics.AppRuntimeCleanupInFlight.Inc()
		defer w.Metrics.AppRuntimeCleanupInFlight.Dec()
	}
	err := w.withCleanupHeartbeat(parent, job, func(ctx context.Context) error {
		return w.Runtime.RemoveCleanupTarget(ctx, job)
	})
	if err == nil {
		if err := w.Store.CompleteAppRuntimeCleanup(parent, job); err != nil {
			return err
		}
		if w.Metrics != nil {
			w.Metrics.AppRuntimeCleanupCompleted.WithLabelValues("completed").Inc()
			w.Metrics.AppRuntimeCleanupDuration.Observe(time.Since(started).Seconds())
		}
		return nil
	}
	if parent.Err() != nil || errors.Is(err, repository.ErrAppRuntimeLeaseLost) {
		return nil
	}
	terminal := errors.Is(err, ErrRuntimeOwnershipConflict) || job.AttemptCount+1 >= maxCleanupAttempts
	message := safeRuntimeError(err)
	if message == "" {
		message = "runtime cleanup unavailable"
	}
	if failErr := w.Store.FailAppRuntimeCleanup(parent, job, message, time.Now().Add(runtimeBackoff(job.AttemptCount)), terminal); failErr != nil {
		return errors.Join(err, failErr)
	}
	if w.Metrics != nil {
		result := "retry"
		if terminal {
			result = "failed"
		}
		w.Metrics.AppRuntimeCleanupCompleted.WithLabelValues(result).Inc()
		w.Metrics.AppRuntimeCleanupDuration.Observe(time.Since(started).Seconds())
	}
	return nil
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

func (w *Worker) runtimeNetworkName() string {
	if moby, ok := w.Runtime.(*Moby); ok && moby.NetworkName != "" {
		return moby.NetworkName
	}
	return defaultRuntimeNetwork
}

func (w *Worker) logRuntimeUnavailable(operation string, err error) {
	if w.Metrics != nil {
		w.Metrics.AppRuntimeErrors.WithLabelValues(operation).Inc()
	}
	w.Logger.Warn("App runtime dependency unavailable", "operation", operation, "error", safeRuntimeError(err))
}

func runtimeBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	seconds := math.Pow(2, float64(min(attempt, 6)))
	delay := time.Duration(seconds * float64(time.Second))
	if delay > maxRuntimeRetry {
		return maxRuntimeRetry
	}
	return delay
}

func supportedRuntimePlatform(deploymentPlatform string, image ociartifact.ImageInfo) bool {
	imagePlatform := image.OS + "/" + image.Architecture
	return strings.EqualFold(deploymentPlatform, imagePlatform) && strings.EqualFold(imagePlatform, hostPlatform())
}

func terminalRuntimeError(err error) bool {
	return errors.Is(err, ErrImageArtifactUnavailable) || errors.Is(err, ErrImageVerification) ||
		errors.Is(err, ociartifact.ErrInvalidArchive) || errors.Is(err, errUnsupportedImageVolumes) ||
		errors.Is(err, ErrRuntimeOwnershipConflict) || errors.Is(err, ErrRuntimeNetworkConflict) ||
		errors.Is(err, ErrUnsupportedRuntimePlatform)
}

func runtimeErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrImageArtifactUnavailable), errors.Is(err, ErrImageVerification), errors.Is(err, ociartifact.ErrInvalidArchive):
		return "image"
	case errors.Is(err, ErrRuntimeOwnershipConflict):
		return "ownership"
	case errors.Is(err, ErrRuntimeNetworkConflict):
		return "network"
	case errors.Is(err, ErrContainerCreate), errors.Is(err, ErrContainerStart), errors.Is(err, ErrContainerInspection):
		return "container"
	default:
		return "runtime"
	}
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
