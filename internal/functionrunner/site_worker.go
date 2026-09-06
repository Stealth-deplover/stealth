package functionrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/functionstore"
	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/sitestore"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// SiteBuildExecutor is implemented by DockerExecutor. Keeping it separate
// from BuildExecutor makes the source-build queue testable without requiring
// a Docker daemon or coupling Site metadata to Function settings.
type SiteBuildExecutor interface {
	BuildSite(context.Context, repository.SiteBuildJob, string, io.Writer) error
}

// SiteWorker consumes source Site deployments and publishes immutable static
// directories. It is intentionally a separate queue loop so a long Site
// build cannot block Function executions or their build leases.
type SiteWorker struct {
	Repository   *repository.Repository
	SourceStore  *functionstore.Store
	PublicStore  *sitestore.Store
	Builder      SiteBuildExecutor
	WorkerID     string
	StagingRoot  string
	ArchiveLimit ArchiveLimits
	PollInterval time.Duration
	LeaseAge     time.Duration
	BuildTimeout time.Duration
	Logger       *slog.Logger
	Metrics      *observability.WorkerMetrics
}

func NewSiteWorker(repo *repository.Repository, sourceStore *functionstore.Store, publicStore *sitestore.Store, builder SiteBuildExecutor, workerID, stagingRoot string, logger *slog.Logger) (*SiteWorker, error) {
	if repo == nil || sourceStore == nil || publicStore == nil || builder == nil || !validWorkerID(workerID) {
		return nil, fmt.Errorf("invalid site worker dependencies")
	}
	if strings.TrimSpace(stagingRoot) == "" {
		stagingRoot = defaultStagingRoot
	}
	stagingRoot, err := filepath.Abs(stagingRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve site worker staging root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stagingRoot, "site-builds"), defaultSourceDirMode); err != nil {
		return nil, fmt.Errorf("create site worker staging root: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SiteWorker{
		Repository:   repo,
		SourceStore:  sourceStore,
		PublicStore:  publicStore,
		Builder:      builder,
		WorkerID:     workerID,
		StagingRoot:  filepath.Clean(stagingRoot),
		ArchiveLimit: ArchiveLimits{},
		PollInterval: defaultWorkerPoll,
		LeaseAge:     defaultLeaseAge,
		BuildTimeout: defaultBuildTimeout,
		Logger:       logger,
		Metrics:      observability.NewWorkerMetrics(),
	}, nil
}

func (w *SiteWorker) Run(ctx context.Context) error {
	if w == nil || w.Repository == nil || w.SourceStore == nil || w.PublicStore == nil || w.Builder == nil {
		return errors.New("site worker is not configured")
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
		if requeued, err := w.Repository.RequeueStaleSiteDeployments(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) {
			if metrics := w.Metrics; metrics != nil {
				metrics.Errors.WithLabelValues("site_requeue_build").Inc()
			}
			w.Logger.Error("requeue stale site builds failed", "error", err)
		} else if requeued > 0 {
			if metrics := w.Metrics; metrics != nil {
				metrics.BuildRequeued.Add(float64(requeued))
			}
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			w.Logger.Error("site build failed", "error", err)
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

func (w *SiteWorker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.Repository.ClaimNextSiteDeployment(ctx, w.WorkerID)
	if errors.Is(err, repository.ErrNoSiteDeploymentJob) {
		return false, nil
	}
	if err != nil {
		if metrics := w.Metrics; metrics != nil {
			metrics.Errors.WithLabelValues("site_build_claim").Inc()
		}
		return false, err
	}
	started := time.Now()
	if metrics := w.Metrics; metrics != nil {
		metrics.BuildsClaimed.Inc()
		metrics.BuildInFlight.Inc()
	}
	spanContext, span := observability.StartWorkerSpan(ctx, "sites.build", attribute.String("stealth.site.framework", job.Site.Framework))
	result, buildErr := w.buildDeployment(spanContext, job)
	span.SetAttributes(attribute.String("stealth.operation.result", result))
	if buildErr != nil {
		span.RecordError(errors.New("site build failed"))
		span.SetStatus(codes.Error, "site build failed")
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
	if metrics := w.Metrics; metrics != nil {
		metrics.BuildInFlight.Dec()
		if result == "" {
			result = "error"
		}
		metrics.BuildDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
		if result == "succeeded" || result == "failed" {
			metrics.BuildsCompleted.WithLabelValues(result).Inc()
		}
		if buildErr != nil {
			metrics.Errors.WithLabelValues("site_build").Inc()
		}
	}
	return true, buildErr
}

func (w *SiteWorker) buildDeployment(parent context.Context, job repository.SiteBuildJob) (string, error) {
	projectID, err := uuid.Parse(job.Deployment.ProjectID)
	if err != nil {
		return "error", fmt.Errorf("invalid site build project id: %w", err)
	}
	siteID, err := uuid.Parse(job.Deployment.SiteID)
	if err != nil {
		return "error", fmt.Errorf("invalid site build site id: %w", err)
	}
	deploymentID, err := uuid.Parse(job.Deployment.ID)
	if err != nil {
		return "error", fmt.Errorf("invalid site build deployment id: %w", err)
	}
	stagingSubpath := filepath.ToSlash(filepath.Join("site-builds", deploymentID.String()))
	if !safeVolumeSubpath(stagingSubpath) {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build workspace path is invalid")
	}
	workspace := filepath.Join(w.StagingRoot, filepath.FromSlash(stagingSubpath))
	if err := ensureWithin(w.StagingRoot, workspace); err != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build workspace path is invalid")
	}
	if err := os.RemoveAll(workspace); err != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build workspace could not be reset")
	}
	if err := os.MkdirAll(workspace, defaultSourceDirMode); err != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build workspace could not be created")
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	w.emitBuildLog(parent, projectID, siteID, deploymentID, "info", "build started")

	archive, err := w.SourceStore.OpenRelative(job.SourcePath)
	if err != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site source archive is unavailable")
	}
	checkedArchive := newChecksumReader(archive)
	limits := w.ArchiveLimit.withDefaults()
	sourceLimits := limits
	sourceLimits.StripTopLevel = job.Deployment.Source == "github" || job.Deployment.Source == "gitlab"
	stats, extractErr := Extract(parent, checkedArchive, valueOr(job.Deployment.SourceName, ""), workspace, sourceLimits)
	_ = archive.Close()
	if extractErr != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, redactFailure(extractErr.Error(), nil))
	}
	if expected := strings.TrimSpace(job.Deployment.ChecksumSHA256); expected == "" || !strings.EqualFold(expected, checkedArchive.SumHex()) {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site source archive checksum mismatch")
	}
	if stats.Files == 0 {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site source archive contains no files")
	}
	w.emitBuildLog(parent, projectID, siteID, deploymentID, "info", fmt.Sprintf("source archive extracted (%d files, %d bytes)", stats.Files, stats.Bytes))

	buildTimeout := w.BuildTimeout
	if buildTimeout <= 0 {
		buildTimeout = defaultBuildTimeout
	}
	buildCtx, cancel := context.WithTimeout(parent, buildTimeout)
	defer cancel()
	artifactStaging, artifactPath, err := w.PublicStore.BeginStaging(projectID, siteID, deploymentID)
	if err != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build output workspace could not be created")
	}
	committed := false
	defer func() {
		if !committed {
			w.PublicStore.CleanupStaging(artifactStaging)
		}
	}()
	w.emitBuildLog(parent, projectID, siteID, deploymentID, "info", "build command started")
	pipeReader, pipeWriter := io.Pipe()
	buildErrors := make(chan error, 1)
	go func() {
		buildErr := w.Builder.BuildSite(buildCtx, job, stagingSubpath, pipeWriter)
		_ = pipeWriter.CloseWithError(buildErr)
		buildErrors <- buildErr
	}()
	checkedBuild := newChecksumReader(pipeReader)
	outputStats, outputErr := Extract(parent, checkedBuild, "site-build.tar", artifactStaging, limits)
	if outputErr != nil {
		_ = pipeReader.CloseWithError(outputErr)
		cancel()
	}
	buildErr := <-buildErrors
	if outputErr != nil {
		if errors.Is(parent.Err(), context.Canceled) {
			return "error", parent.Err()
		}
		message := outputErr.Error()
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			message = "site build timed out"
		}
		return w.failBuild(parent, projectID, siteID, deploymentID, redactFailure(message, nil))
	}
	if buildErr != nil {
		if errors.Is(parent.Err(), context.Canceled) {
			return "error", parent.Err()
		}
		message := buildErr.Error()
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			message = "site build timed out"
		}
		return w.failBuild(parent, projectID, siteID, deploymentID, redactFailure(message, nil))
	}
	if outputStats.Files == 0 {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build output contains no files")
	}
	if err := sitestore.ValidateEntrypoint(artifactStaging, "index.html"); err != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build output must contain a regular index.html at its root")
	}
	w.emitBuildLog(parent, projectID, siteID, deploymentID, "info", fmt.Sprintf("build artifact produced (%d files, %d bytes)", outputStats.Files, outputStats.Bytes))
	if err := w.PublicStore.CommitDirectory(artifactStaging, artifactPath); err != nil {
		return w.failBuild(parent, projectID, siteID, deploymentID, "site build artifact could not be committed")
	}
	committed = true
	if _, err := w.Repository.CompleteSiteDeploymentBuild(parent, projectID, siteID, deploymentID, w.WorkerID, checkedBuild.SumHex(), outputStats.Bytes); err != nil {
		_ = w.PublicStore.RemoveRelative(artifactPath)
		if errors.Is(err, repository.ErrSiteQuotaExceeded) {
			return w.failBuild(parent, projectID, siteID, deploymentID, "site build artifact exceeds the remaining quota")
		}
		return "error", err
	}
	w.emitBuildLog(parent, projectID, siteID, deploymentID, "info", "build completed")
	return "succeeded", nil
}

func (w *SiteWorker) failBuild(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, message string) (string, error) {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "error", ctx.Err()
	}
	if _, err := w.Repository.FailSiteDeploymentBuild(ctx, projectID, siteID, deploymentID, w.WorkerID, normalizeBuildLogMessage(message)); err != nil {
		return "error", err
	}
	w.emitBuildLog(ctx, projectID, siteID, deploymentID, "error", message)
	return "failed", nil
}

func (w *SiteWorker) emitBuildLog(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, level, message string) {
	if err := w.appendBuildLog(ctx, projectID, siteID, deploymentID, level, message); err != nil && w.Logger != nil {
		w.Logger.Error("append site build log failed", "deployment_id", deploymentID, "error", err)
	}
}

func (w *SiteWorker) appendBuildLog(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, level, message string) error {
	message = redactFailure(normalizeBuildLogMessage(message), nil)
	if strings.TrimSpace(message) == "" {
		return nil
	}
	_, err := w.Repository.AppendSiteBuildLog(ctx, projectID, siteID, deploymentID, uuid.Must(uuid.NewV7()), 0, level, message)
	return err
}
