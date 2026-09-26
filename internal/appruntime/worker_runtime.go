package appruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"io"
	"math"
	"strings"
	"time"
)

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
	if found && !container.State.Running {
		w.recordAppProcessExit(job, container)
	}
	if found && ContainerMatchesDesiredExceptName(container, job, image, w.runtimeNetworkName()) {
		if strings.TrimPrefix(container.Name, "/") != job.ContainerName {
			if err := w.requireCurrent(ctx, job); err != nil {
				return err
			}
			container, err = w.Runtime.RenameApp(ctx, job, container.ID, job.ContainerName)
			if err != nil {
				return errors.Join(ErrRuntimeOwnershipConflict, err)
			}
			if !ContainerMatchesDesired(container, job, image, w.runtimeNetworkName()) {
				return ErrRuntimeOwnershipConflict
			}
		}
		if container.State.Running {
			return w.completeRunning(ctx, job, container, imageInfo, runtimeTag)
		}
		// Docker restart policy is deliberately disabled. Stealth retries an
		// exited process through this durable, backoff-controlled reconcile.
		if err := w.requireCurrent(ctx, job); err != nil {
			return err
		}
		identity, err := repository.NewAppRuntimeRouteIdentity()
		if err != nil {
			return err
		}
		if err := w.Store.ResetAppHealthBeforeRuntimeRestart(ctx, job, identity); err != nil {
			return err
		}
		job.RouteIdentity = identity
		job.ContainerName = repository.AppRuntimeContainerNameForIncarnation(appID, identity)
		// The reset is a durable route fence. Recheck the lease before the
		// Docker side effect so an expired worker cannot restart after handoff.
		if err := w.requireCurrent(ctx, job); err != nil {
			return err
		}
		container, err = w.Runtime.RenameApp(ctx, job, container.ID, job.ContainerName)
		if err != nil {
			return errors.Join(ErrRuntimeOwnershipConflict, err)
		}
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
	identity, err := repository.NewAppRuntimeRouteIdentity()
	if err != nil {
		return err
	}
	if err := w.Store.RotateAppRuntimeIdentityBeforeCreate(ctx, job, identity); err != nil {
		return err
	}
	job.RouteIdentity = identity
	job.ContainerName = repository.AppRuntimeContainerNameForIncarnation(appID, identity)
	if err := w.requireCurrent(ctx, job); err != nil {
		return err
	}
	runtimeEnvironment, err := w.runtimeEnvironment(ctx, job)
	if err != nil {
		return err
	}
	defer wipeRuntimeEnvironment(runtimeEnvironment)
	container, err = w.Runtime.CreateApp(ctx, job, image, runtimeEnvironment)
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
		Name:        job.ContainerName,
		ImageID:     imageID,
		ImageDigest: imageInfo.ManifestDigest,
		RuntimeTag:  runtimeTag,
		Address:     containerAddress(container, w.runtimeNetworkName()),
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

func (w *Worker) recordAppProcessExit(job repository.AppRuntimeJob, container Container) {
	reason := "unknown"
	switch {
	case container.State.OOMKilled:
		reason = "oom"
	case container.State.Status == "exited" && container.State.ExitCode == 0:
		reason = "clean_exit"
	case container.State.Status == "exited" && container.State.ExitCode != 0:
		reason = "nonzero_exit"
	}
	if w.Metrics != nil {
		w.Metrics.AppRuntimeProcessExits.WithLabelValues(reason).Inc()
	}
	w.Logger.Warn("App runtime process is stopped", "app_id", job.App.ID, "generation", job.App.DesiredGeneration, "reason", reason, "exit_code", container.State.ExitCode)
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
