// Package appbuilder consumes durable AppDeployment build jobs and publishes
// verified OCI archives. Build execution is delegated only to the dedicated
// BuildKit service; this package never uses the worker's Docker socket.
package appbuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
	"github.com/Stealth-deplover/stealth/internal/appstore"
	"github.com/Stealth-deplover/stealth/internal/buildkitmetadata"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionrunner"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	defaultBuildTimeout = 20 * time.Minute
	defaultLeaseAge     = 25 * time.Minute
	defaultPollInterval = 500 * time.Millisecond
	maxRetainedLogBytes = 4 << 20
	maxRetainedLogLines = 2000
)

type Persistence interface {
	RequeueStaleAppDeployments(context.Context, time.Duration) (int64, error)
	ClaimNextAppDeployment(context.Context, string) (repository.AppBuildJob, error)
	DeferAppDeploymentBuild(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string) error
	ReserveAppImagePublish(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, int64, repository.ArtifactCleanupInput) error
	CompleteAppDeploymentBuildWithCleanup(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string, string, int64, repository.ArtifactCleanupInput) (domain.AppDeployment, error)
	FailAppDeploymentBuild(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (domain.AppDeployment, error)
	AppendAppBuildLog(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, uuid.UUID, string, string) (domain.AppBuildLog, error)
}

var _ Persistence = (*repository.Repository)(nil)

const staleStagingSweepInterval = 5 * time.Minute

type Worker struct {
	Store        Persistence
	Artifacts    *appstore.Store
	Builder      *BuildKitClient
	WorkerID     string
	StagingRoot  string
	ArchiveLimit functionrunner.ArchiveLimits
	PollInterval time.Duration
	LeaseAge     time.Duration
	BuildTimeout time.Duration
	Logger       *slog.Logger
	Metrics      *observability.WorkerMetrics

	lastUnavailableLog time.Time
	logMu              sync.Mutex
}

func New(store Persistence, artifacts *appstore.Store, builder *BuildKitClient, workerID, stagingRoot string, logger *slog.Logger) (*Worker, error) {
	if store == nil || artifacts == nil || artifacts.Sources == nil || artifacts.Images == nil || builder == nil || !safeWorkerID(workerID) {
		return nil, errors.New("invalid App build worker dependencies")
	}
	if strings.TrimSpace(stagingRoot) == "" {
		return nil, errors.New("App build staging root is required")
	}
	absRoot, err := filepath.Abs(stagingRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve App build staging root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create App build staging root: %w", err)
	}
	info, err := os.Lstat(absRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("App build staging root must be a private real directory")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Store: store, Artifacts: artifacts, Builder: builder, WorkerID: workerID,
		StagingRoot: filepath.Clean(absRoot), ArchiveLimit: functionrunner.ArchiveLimits{},
		PollInterval: defaultPollInterval, LeaseAge: defaultLeaseAge, BuildTimeout: defaultBuildTimeout,
		Logger: logger, Metrics: observability.NewWorkerMetrics(),
	}, nil
}

// Run supervises the App build queue independently. Readiness is checked
// before claiming work, so a BuildKit outage leaves the durable queue intact.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Builder == nil || w.Artifacts == nil {
		return errors.New("App build worker is not configured")
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
	lastStagingSweep := time.Time{}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if requeued, err := w.Store.RequeueStaleAppDeployments(ctx, leaseAge); err != nil && ctx.Err() == nil {
			w.Logger.Error("requeue stale App builds failed", "error", err)
		} else if requeued > 0 && w.Metrics != nil {
			w.Metrics.AppBuildRequeued.Add(float64(requeued))
		}
		if lastStagingSweep.IsZero() || time.Since(lastStagingSweep) >= staleStagingSweepInterval {
			if removed, err := w.cleanStaleStaging(2 * leaseAge); err != nil {
				w.Logger.Warn("stale App build staging cleanup failed", "error", err)
			} else if removed > 0 {
				w.Logger.Info("removed stale App build staging directories", "count", removed)
			}
			removedArtifactTemps := 0
			for _, namespace := range []*appstore.Namespace{w.Artifacts.Sources, w.Artifacts.Images} {
				removed, err := namespace.CleanupStaleUploads(ctx, 2*leaseAge)
				if err != nil {
					w.Logger.Warn("stale App artifact upload cleanup failed", "error", err)
					continue
				}
				removedArtifactTemps += removed
			}
			if removedArtifactTemps > 0 {
				w.Logger.Info("removed stale App artifact upload staging files", "count", removedArtifactTemps)
			}
			lastStagingSweep = time.Now()
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			w.Logger.Error("App build worker iteration failed", "error", err)
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

// cleanStaleStaging removes private workspaces left by a process that exited
// before its deferred cleanup ran. A directory older than twice the build
// lease cannot belong to a live build: build timeouts are configured no longer
// than their lease, and a reclaimed deployment gets a fresh workspace.
func (w *Worker) cleanStaleStaging(maxAge time.Duration) (int, error) {
	if w == nil || maxAge <= 0 || w.StagingRoot == "" {
		return 0, errors.New("App build staging cleanup is not configured")
	}
	entries, err := os.ReadDir(w.StagingRoot)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, err := uuid.Parse(entry.Name())
		if err != nil || id == uuid.Nil || id.Version() != uuid.Version(7) || id.String() != entry.Name() {
			continue
		}
		path := filepath.Join(w.StagingRoot, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.ModTime().After(cutoff) {
			continue
		}
		if !insideRoot(w.StagingRoot, path) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// RunOnce probes BuildKit before claiming a lease and processes at most one
// job. The readiness failure is nonfatal to this queue and all other workers.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if err := w.Builder.Ready(ctx); err != nil {
		if w.Metrics != nil {
			w.Metrics.AppBuildErrors.WithLabelValues("buildkit_unavailable").Inc()
		}
		w.logUnavailable(err)
		return false, nil
	}
	leaseID, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	leaseToken := leaseID.String()
	job, err := w.Store.ClaimNextAppDeployment(ctx, leaseToken)
	if errors.Is(err, repository.ErrNoAppDeploymentJob) {
		return false, nil
	}
	if err != nil {
		if w.Metrics != nil {
			w.Metrics.AppBuildErrors.WithLabelValues("claim").Inc()
		}
		return false, err
	}
	if job.WorkerID != leaseToken {
		return true, errors.New("App build lease token did not match the claimed job")
	}
	started := time.Now()
	spanContext, span := observability.StartWorkerSpan(ctx, "apps.build",
		attribute.String("stealth.app.deployment.version", fmt.Sprint(job.Deployment.Version)))
	if w.Metrics != nil {
		w.Metrics.AppBuildsClaimed.Inc()
		w.Metrics.AppBuildInFlight.Inc()
	}
	result, buildErr := w.build(spanContext, job)
	if buildErr != nil {
		span.RecordError(errors.New("App build failed"))
		span.SetStatus(codes.Error, "App build failed")
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
	if w.Metrics != nil {
		w.Metrics.AppBuildInFlight.Dec()
		if result == "" {
			result = "error"
		}
		w.Metrics.AppBuildDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
		if result == "succeeded" || result == "failed" {
			w.Metrics.AppBuildsCompleted.WithLabelValues(result).Inc()
		}
		if buildErr != nil {
			w.Metrics.AppBuildErrors.WithLabelValues("build").Inc()
		}
	}
	return true, buildErr
}

func (w *Worker) build(parent context.Context, job repository.AppBuildJob) (result string, returned error) {
	projectID, err := uuid.Parse(job.Deployment.ProjectID)
	if err != nil {
		return "error", errors.New("invalid App build job")
	}
	appID, err := uuid.Parse(job.Deployment.AppID)
	if err != nil {
		return "error", errors.New("invalid App build job")
	}
	deploymentID, err := uuid.Parse(job.Deployment.ID)
	if err != nil {
		return "error", errors.New("invalid App build job")
	}
	if !safeWorkerID(job.WorkerID) || !safeRelativeArtifact(job.SourcePath) || job.Deployment.Source != "upload" {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "source unavailable")
	}
	platform, err := appbuildspec.CurrentHostPlatform()
	if err != nil || job.Deployment.Platform != platform {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "build platform is unavailable")
	}
	buildCtx, cancel := context.WithTimeout(parent, positiveDuration(w.BuildTimeout, defaultBuildTimeout))
	defer cancel()
	jobRoot := filepath.Join(w.StagingRoot, deploymentID.String())
	if !insideRoot(w.StagingRoot, jobRoot) {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "build workspace is unavailable")
	}
	if err := os.RemoveAll(jobRoot); err != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "build workspace is unavailable")
	}
	if err := os.Mkdir(jobRoot, 0o700); err != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "build workspace is unavailable")
	}
	defer func() { _ = os.RemoveAll(jobRoot) }()
	sourceRoot := filepath.Join(jobRoot, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "build workspace is unavailable")
	}
	if err := w.appendLog(parent, job.WorkerID, projectID, appID, deploymentID, "info", "Build started"); err != nil {
		w.Logger.Warn("could not persist App build log", "deployment_id", deploymentID, "error", err)
	}

	archive, err := w.Artifacts.Sources.OpenRelative(buildCtx, job.SourcePath)
	if err != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "source unavailable")
	}
	actualChecksum, checksumErr := checksumSeekable(archive)
	if checksumErr != nil {
		_ = archive.Close()
		if parent.Err() != nil {
			return "error", parent.Err()
		}
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "source unavailable")
	}
	if actualChecksum != job.Deployment.SourceChecksumSHA256 {
		_ = archive.Close()
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "source checksum mismatch")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		_ = archive.Close()
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "source unavailable")
	}
	limits := w.ArchiveLimit
	limits.MaxCompressed = job.Deployment.SourceSizeBytes
	if limits.MaxCompressed <= 0 {
		_ = archive.Close()
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "source unavailable")
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 1 << 30
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = 8192
	}
	if limits.MaxEntry <= 0 {
		limits.MaxEntry = limits.MaxBytes
	}
	stats, extractErr := functionrunner.Extract(buildCtx, archive, valueOr(job.Deployment.SourceName, ""), sourceRoot, limits)
	_ = archive.Close()
	if extractErr != nil || stats.Files == 0 {
		if parent.Err() != nil {
			return "error", parent.Err()
		}
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "BuildKit timeout")
		}
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "source archive invalid")
	}
	if err := w.appendLog(parent, job.WorkerID, projectID, appID, deploymentID, "info", "Source archive extracted"); err != nil {
		w.Logger.Warn("could not persist App build log", "deployment_id", deploymentID, "error", err)
	}

	definition := appbuildspec.Spec{
		DockerfilePath: job.Deployment.DockerfilePath, ContextDirectory: job.Deployment.ContextDirectory,
		Target: job.Deployment.Target, Platform: job.Deployment.Platform,
	}
	contextPath, err := ValidateBuildContext(sourceRoot, definition)
	if err != nil {
		if errors.Is(err, ErrUnsafeDockerfileFrontend) {
			return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "Dockerfile frontend override is not allowed")
		}
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "Dockerfile or context is invalid")
	}
	if err := w.appendLog(parent, job.WorkerID, projectID, appID, deploymentID, "info", "BuildKit connected; build started"); err != nil {
		w.Logger.Warn("could not persist App build log", "deployment_id", deploymentID, "error", err)
	}
	outputPath := filepath.Join(jobRoot, "image.oci.tar")
	metadataPath := filepath.Join(jobRoot, "buildkit-metadata.json")
	progress := &progressWriter{
		ctx: buildCtx, store: w.Store, workerID: job.WorkerID, projectID: projectID, appID: appID,
		deploymentID: deploymentID, redactRoot: w.StagingRoot,
	}
	buildCommandCtx, cancelBuildCommand := context.WithCancel(buildCtx)
	tooLarge := make(chan struct{}, 1)
	monitorDone := make(chan struct{})
	go monitorArtifactSize(buildCommandCtx, cancelBuildCommand, outputPath, w.Artifacts.Images.MaxBytes(), tooLarge, monitorDone)
	buildErr := w.Builder.Build(buildCommandCtx, BuildRequest{Definition: definition, ContextPath: contextPath, DockerfileRoot: sourceRoot, OutputPath: outputPath, MetadataPath: metadataPath}, progress)
	cancelBuildCommand()
	<-monitorDone
	progress.Flush()
	select {
	case <-tooLarge:
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "artifact too large")
	default:
	}
	if buildErr != nil {
		if errors.Is(parent.Err(), context.Canceled) || errors.Is(parent.Err(), context.DeadlineExceeded) {
			return "error", parent.Err()
		}
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "BuildKit timeout")
		}
		if errors.Is(buildErr, ErrBuildKitUnavailable) || errors.Is(w.Builder.Ready(parent), ErrBuildKitUnavailable) {
			if err := w.Store.DeferAppDeploymentBuild(parent, projectID, appID, deploymentID, job.WorkerID, "BuildKit unavailable; build remains queued"); err != nil {
				return "error", err
			}
			return "deferred", nil
		}
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "Dockerfile build failed")
	}
	if err := w.appendLog(parent, job.WorkerID, projectID, appID, deploymentID, "info", "OCI export produced"); err != nil {
		w.Logger.Warn("could not persist App build log", "deployment_id", deploymentID, "error", err)
	}
	metadataFile, err := os.Open(metadataPath)
	if err != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "BuildKit metadata invalid")
	}
	metadata, metadataErr := buildkitmetadata.Parse(metadataFile)
	closeErr := metadataFile.Close()
	if metadataErr != nil || closeErr != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "BuildKit metadata invalid")
	}
	imageFile, err := os.Open(outputPath)
	if err != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "OCI export invalid")
	}
	imageInfo, err := imageFile.Stat()
	if err != nil || !imageInfo.Mode().IsRegular() || imageInfo.Size() <= 0 {
		_ = imageFile.Close()
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "OCI export invalid")
	}
	if imageInfo.Size() > w.Artifacts.Images.MaxBytes() {
		_ = imageFile.Close()
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "artifact too large")
	}
	if err := ociartifact.Validate(imageFile, metadata.ImageDigest, w.Artifacts.Images.MaxBytes()); err != nil {
		_ = imageFile.Close()
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "OCI export invalid")
	}
	if _, err := imageFile.Seek(0, io.SeekStart); err != nil {
		_ = imageFile.Close()
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "OCI export invalid")
	}
	imageArtifactID, err := uuid.NewV7()
	if err != nil {
		_ = imageFile.Close()
		return "error", err
	}
	prepared, err := w.Artifacts.Images.BeginUpload(buildCtx, projectID, appID, imageArtifactID, imageFile, w.Artifacts.Images.MaxBytes())
	_ = imageFile.Close()
	if err != nil {
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "BuildKit timeout")
		}
		if errors.Is(err, appstore.ErrTooLarge) {
			return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "artifact too large")
		}
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "OCI export invalid")
	}
	defer w.Artifacts.Images.Cleanup(&prepared)
	if prepared.Size != imageInfo.Size() {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "OCI export invalid")
	}
	publishCleanup := repository.ArtifactCleanupInput{ProjectID: projectID, StoreKind: repository.ArtifactCleanupAppImages, Operation: repository.ArtifactCleanupRelative, RelativePath: prepared.RelativePath}
	if err := w.Store.ReserveAppImagePublish(parent, projectID, appID, deploymentID, job.WorkerID, prepared.Size, publishCleanup); err != nil {
		if errors.Is(err, repository.ErrAppArtifactQuotaExceeded) {
			return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "artifact quota exceeded")
		}
		if errors.Is(err, repository.ErrAppBuildNotOwned) {
			return "error", err
		}
		return "error", errors.New("OCI artifact publication could not be reserved")
	}
	if err := w.Artifacts.Images.Commit(parent, &prepared); err != nil {
		return w.fail(parent, job.WorkerID, projectID, appID, deploymentID, "OCI artifact could not be published")
	}
	if err := w.appendLog(parent, job.WorkerID, projectID, appID, deploymentID, "info", "OCI digest verified and artifact persisted"); err != nil {
		w.Logger.Warn("could not persist App build log", "deployment_id", deploymentID, "error", err)
	}
	if _, err := w.Store.CompleteAppDeploymentBuildWithCleanup(parent, projectID, appID, deploymentID, job.WorkerID, metadata.ImageDigest, prepared.RelativePath, prepared.Checksum, prepared.Size, publishCleanup); err != nil {
		// The durable cleanup reservation must remain: a DB commit failure may
		// be ambiguous, so deleting the artifact here could break a committed row.
		return "error", errors.New("App deployment completion was not confirmed")
	}
	if err := w.appendLog(parent, job.WorkerID, projectID, appID, deploymentID, "info", "Build completed; App remains not deployed"); err != nil {
		w.Logger.Warn("could not persist App build completion log", "deployment_id", deploymentID, "error", err)
	}
	return "succeeded", nil
}

func monitorArtifactSize(ctx context.Context, cancel context.CancelFunc, path string, maximum int64, tooLarge chan<- struct{}, done chan<- struct{}) {
	defer close(done)
	if maximum <= 0 {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err == nil && info.Mode().IsRegular() && info.Size() > maximum {
				cancel()
				select {
				case tooLarge <- struct{}{}:
				default:
				}
				return
			}
		}
	}
}

func (w *Worker) fail(ctx context.Context, workerID string, projectID, appID, deploymentID uuid.UUID, message string) (string, error) {
	if ctx.Err() != nil {
		return "error", ctx.Err()
	}
	if _, err := w.Store.FailAppDeploymentBuild(ctx, projectID, appID, deploymentID, workerID, message); err != nil {
		return "error", err
	}
	if err := w.appendLog(ctx, workerID, projectID, appID, deploymentID, "error", message); err != nil {
		w.Logger.Warn("could not persist App build failure log", "deployment_id", deploymentID, "error", err)
	}
	return "failed", nil
}

func (w *Worker) appendLog(ctx context.Context, workerID string, projectID, appID, deploymentID uuid.UUID, level, message string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = w.Store.AppendAppBuildLog(ctx, projectID, appID, deploymentID, workerID, id, level, message)
	return err
}

func (w *Worker) logUnavailable(err error) {
	w.logMu.Lock()
	defer w.logMu.Unlock()
	if time.Since(w.lastUnavailableLog) < time.Minute {
		return
	}
	w.lastUnavailableLog = time.Now()
	w.Logger.Warn("dedicated BuildKit service is unavailable; App build jobs remain queued", "error", err)
}

func safeWorkerID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func safeRelativeArtifact(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		id, err := uuid.Parse(part)
		if err != nil || id == uuid.Nil || id.Version() != uuid.Version(7) || id.String() != part {
			return false
		}
	}
	return true
}

func insideRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func valueOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func checksumSeekable(file io.ReadSeeker) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressWriter struct {
	ctx          context.Context
	store        Persistence
	workerID     string
	projectID    uuid.UUID
	appID        uuid.UUID
	deploymentID uuid.UUID
	redactRoot   string
	buffer       []byte
	retained     int
	lines        int
	truncated    bool
}

func (w *progressWriter) Write(data []byte) (int, error) {
	written := len(data)
	for _, char := range data {
		if char == '\n' {
			w.writeLine(w.buffer)
			w.buffer = w.buffer[:0]
			continue
		}
		if len(w.buffer) < 16<<10 {
			w.buffer = append(w.buffer, char)
		}
	}
	return written, nil
}

func (w *progressWriter) Flush() {
	if len(w.buffer) > 0 {
		w.writeLine(w.buffer)
		w.buffer = nil
	}
	if w.truncated {
		w.writeLine([]byte("Build output truncated after reaching the retention limit"))
	}
}

func (w *progressWriter) writeLine(raw []byte) {
	if w.lines >= maxRetainedLogLines || w.retained >= maxRetainedLogBytes {
		w.truncated = true
		return
	}
	line := strings.ToValidUTF8(string(raw), "�")
	line = strings.ReplaceAll(line, w.redactRoot, "<worker-staging>")
	line = strings.ReplaceAll(line, "\r", " ")
	line = strings.ReplaceAll(line, "\x00", " ")
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(line) > 4096 {
		line = line[:4096]
	}
	for len(line) > 0 && len(line)+w.retained > maxRetainedLogBytes {
		line = line[:len(line)-1]
	}
	if line == "" {
		w.truncated = true
		return
	}
	id, err := uuid.NewV7()
	if err == nil {
		_, _ = w.store.AppendAppBuildLog(w.ctx, w.projectID, w.appID, w.deploymentID, w.workerID, id, "info", line)
	}
	w.lines++
	w.retained += len(line)
}
