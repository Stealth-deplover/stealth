package functionrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/repository"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func (w *Worker) RunBuildOnce(ctx context.Context) (bool, error) {
	if w == nil || w.Repository == nil || w.Builder == nil {
		return false, nil
	}
	job, err := w.Repository.ClaimNextFunctionDeployment(ctx, w.WorkerID)
	if errors.Is(err, repository.ErrNoDeploymentJob) {
		return false, nil
	}
	if err != nil {
		if metrics := w.Metrics; metrics != nil {
			metrics.Errors.WithLabelValues("build_claim").Inc()
		}
		return false, err
	}
	metrics := w.Metrics
	started := time.Now()
	if metrics != nil {
		metrics.BuildsClaimed.Inc()
		metrics.BuildInFlight.Inc()
	}
	spanContext, span := observability.StartWorkerSpan(ctx, "functions.build", attribute.String("stealth.function.runtime", job.Function.Runtime))
	result, buildErr := w.buildDeployment(spanContext, job)
	span.SetAttributes(attribute.String("stealth.operation.result", result))
	if buildErr != nil {
		// Build errors may contain compiler output or user-provided secret
		// values. Keep the trace status bounded and let the redacted build log
		// carry the operator-facing detail.
		span.RecordError(errors.New("function build failed"))
		span.SetStatus(codes.Error, "function build failed")
		w.Logger.Error("function build failed", "deployment_id", job.Deployment.ID, "function_id", job.Deployment.FunctionID, "project_id", job.Deployment.ProjectID, "error", buildErr)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
	if metrics != nil {
		metrics.BuildInFlight.Dec()
		if result == "" {
			result = "error"
		}
		metrics.BuildDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
		if result == "succeeded" || result == "failed" {
			metrics.BuildsCompleted.WithLabelValues(result).Inc()
		}
		if buildErr != nil {
			metrics.Errors.WithLabelValues("build").Inc()
		}
	}
	return true, buildErr
}

func (w *Worker) buildDeployment(parent context.Context, job repository.FunctionBuildJob) (string, error) {
	projectID, err := uuid.Parse(job.Deployment.ProjectID)
	if err != nil {
		return "error", fmt.Errorf("invalid build project id: %w", err)
	}
	functionID, err := uuid.Parse(job.Deployment.FunctionID)
	if err != nil {
		return "error", fmt.Errorf("invalid build function id: %w", err)
	}
	deploymentID, err := uuid.Parse(job.Deployment.ID)
	if err != nil {
		return "error", fmt.Errorf("invalid build deployment id: %w", err)
	}
	stagingSubpath := filepath.ToSlash(filepath.Join("builds", deploymentID.String()))
	if !safeVolumeSubpath(stagingSubpath) {
		return w.failBuild(parent, projectID, functionID, deploymentID, "build workspace path is invalid", nil)
	}
	workspace := filepath.Join(w.StagingRoot, filepath.FromSlash(stagingSubpath))
	if err := ensureWithin(w.StagingRoot, workspace); err != nil {
		return w.failBuild(parent, projectID, functionID, deploymentID, "build workspace path is invalid", nil)
	}
	if err := os.RemoveAll(workspace); err != nil {
		return w.failBuild(parent, projectID, functionID, deploymentID, "build workspace could not be reset", nil)
	}
	if err := os.MkdirAll(workspace, defaultSourceDirMode); err != nil {
		return w.failBuild(parent, projectID, functionID, deploymentID, "build workspace could not be created", nil)
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	archive, err := w.Store.OpenRelative(job.SourcePath)
	if err != nil {
		return w.failBuild(parent, projectID, functionID, deploymentID, "function source artifact is unavailable", nil)
	}
	checkedArchive := newChecksumReader(archive)
	stats, extractErr := Extract(parent, checkedArchive, valueOr(job.Deployment.SourceName, ""), workspace, w.ArchiveLimit)
	_ = archive.Close()
	if extractErr != nil {
		return w.failBuild(parent, projectID, functionID, deploymentID, redactFailure(extractErr.Error(), nil), nil)
	}
	if expected := strings.TrimSpace(job.Deployment.ChecksumSHA256); expected == "" || !strings.EqualFold(expected, checkedArchive.SumHex()) {
		return w.failBuild(parent, projectID, functionID, deploymentID, "function source artifact checksum mismatch", nil)
	}
	if stats.Files == 0 {
		return w.failBuild(parent, projectID, functionID, deploymentID, "function source archive contains no files", nil)
	}
	if err := validateEntrypointFile(workspace, job.Function.Entrypoint); err != nil {
		return w.failBuild(parent, projectID, functionID, deploymentID, "function entrypoint is unavailable", nil)
	}
	variables, err := w.Repository.FunctionRuntimeVariablesForDeployment(parent, projectID, functionID, deploymentID, w.Cipher)
	if err != nil {
		return w.failBuild(parent, projectID, functionID, deploymentID, "function runtime variables are unavailable", nil)
	}
	secrets := make([]string, 0, len(variables))
	for _, variable := range variables {
		if variable.IsSecret {
			secrets = append(secrets, variable.Value)
		}
	}

	buildTimeout := w.BuildTimeout
	if buildTimeout <= 0 {
		buildTimeout = defaultBuildTimeout
	}
	buildCtx, cancel := context.WithTimeout(parent, buildTimeout)
	defer cancel()
	artifactID := uuid.Must(uuid.NewV7())
	pipeReader, pipeWriter := io.Pipe()
	buildErrors := make(chan error, 1)
	go func() {
		buildErr := w.Builder.Build(buildCtx, job, stagingSubpath, variables, pipeWriter)
		_ = pipeWriter.CloseWithError(buildErr)
		buildErrors <- buildErr
	}()
	prepared, uploadErr := w.Store.BeginUpload(buildCtx, projectID, functionID, artifactID, pipeReader)
	if uploadErr != nil {
		_ = pipeReader.CloseWithError(uploadErr)
		cancel()
		buildErr := <-buildErrors
		if errors.Is(parent.Err(), context.Canceled) {
			return "error", parent.Err()
		}
		message := uploadErr.Error()
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			message = "function build timed out"
		} else if buildErr != nil && !errors.Is(buildErr, context.Canceled) {
			message = buildErr.Error()
		}
		return w.failBuild(parent, projectID, functionID, deploymentID, redactFailure(message, secrets), nil)
	}
	cancel()
	buildErr := <-buildErrors
	if buildErr != nil {
		if errors.Is(parent.Err(), context.Canceled) {
			w.Store.Cleanup(&prepared)
			return "error", parent.Err()
		}
		message := buildErr.Error()
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			message = "function build timed out"
		}
		w.Store.Cleanup(&prepared)
		return w.failBuild(parent, projectID, functionID, deploymentID, redactFailure(message, secrets), nil)
	}
	validationErr := validateBuiltArtifact(parent, prepared.TempPath, w.StagingRoot, deploymentID.String(), job.Function.Entrypoint, w.ArchiveLimit)
	if validationErr != nil {
		w.Store.Cleanup(&prepared)
		return w.failBuild(parent, projectID, functionID, deploymentID, redactFailure(validationErr.Error(), secrets), nil)
	}
	if err := w.Store.Commit(&prepared); err != nil {
		w.Store.Cleanup(&prepared)
		return w.failBuild(parent, projectID, functionID, deploymentID, "function build artifact could not be committed", nil)
	}
	if _, err := w.Repository.CompleteFunctionDeploymentBuild(parent, projectID, functionID, deploymentID, w.WorkerID, prepared.RelativePath, prepared.Size, prepared.Checksum); err != nil {
		_ = w.Store.RemoveRelative(prepared.RelativePath)
		if errors.Is(err, repository.ErrFunctionQuotaExceeded) {
			return w.failBuild(parent, projectID, functionID, deploymentID, "function build artifact exceeds the remaining quota", secrets)
		}
		return "error", err
	}
	if err := w.appendBuildLog(parent, job, "info", "build completed"); err != nil {
		w.Logger.Error("append function build log failed", "deployment_id", deploymentID, "error", err)
	}
	return "succeeded", nil
}

func (w *Worker) failBuild(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, message string, secrets []string) (string, error) {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "error", ctx.Err()
	}
	message = redactFailure(message, secrets)
	if _, err := w.Repository.FailFunctionDeploymentBuild(ctx, projectID, functionID, deploymentID, w.WorkerID, message); err != nil {
		return "error", err
	}
	job := repository.FunctionBuildJob{Deployment: domain.FunctionDeployment{ID: deploymentID.String(), FunctionID: functionID.String(), ProjectID: projectID.String()}}
	if err := w.appendBuildLog(ctx, job, "error", message); err != nil {
		w.Logger.Error("append function build failure log failed", "deployment_id", deploymentID, "error", err)
	}
	return "failed", nil
}

func validateBuiltArtifact(ctx context.Context, tempPath, stagingRoot, deploymentID, entrypoint string, limits ArchiveLimits) error {
	if strings.TrimSpace(tempPath) == "" || !safeVolumeSubpath(filepath.ToSlash(filepath.Join("build-validation", deploymentID))) {
		return ErrArchiveTraversal
	}
	validationSubpath := filepath.ToSlash(filepath.Join("build-validation", deploymentID))
	destination := filepath.Join(stagingRoot, filepath.FromSlash(validationSubpath))
	if err := ensureWithin(stagingRoot, destination); err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, defaultSourceDirMode); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(destination) }()
	artifact, err := os.Open(tempPath)
	if err != nil {
		return err
	}
	stats, extractErr := ExtractTrusted(ctx, artifact, "build.tar", destination, limits)
	closeErr := artifact.Close()
	if extractErr != nil {
		return extractErr
	}
	if closeErr != nil {
		return closeErr
	}
	if stats.Files == 0 {
		return errors.New("function build artifact contains no files")
	}
	return validateEntrypointFile(destination, entrypoint)
}

type checksumReader struct {
	reader io.Reader
	hash   hash.Hash
}

func newChecksumReader(reader io.Reader) *checksumReader {
	return &checksumReader{reader: reader, hash: sha256.New()}
}

func (r *checksumReader) Read(p []byte) (int, error) {
	if r == nil || r.reader == nil || r.hash == nil {
		return 0, io.EOF
	}
	n, err := r.reader.Read(p)
	if n > 0 {
		_, _ = r.hash.Write(p[:n])
	}
	return n, err
}

func (r *checksumReader) SumHex() string {
	if r == nil || r.hash == nil {
		return ""
	}
	return hex.EncodeToString(r.hash.Sum(nil))
}

func (w *Worker) appendBuildLog(ctx context.Context, job repository.FunctionBuildJob, level, message string) error {
	message = normalizeBuildLogMessage(message)
	if message == "" {
		return nil
	}
	_, err := w.Repository.AppendFunctionBuildLog(ctx, mustUUID(job.Deployment.ProjectID), mustUUID(job.Deployment.FunctionID), mustUUID(job.Deployment.ID), uuid.Must(uuid.NewV7()), 0, level, message)
	return err
}

func normalizeBuildLogMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > maxFailureMessageSize {
		message = message[:maxFailureMessageSize]
	}
	return message
}

func validateEntrypointFile(workspace, entrypoint string) error {
	if !safeRuntimeEntrypoint(entrypoint) {
		return ErrRuntimeUnavailable
	}
	target := filepath.Join(workspace, filepath.FromSlash(entrypoint))
	if err := ensureWithin(workspace, target); err != nil {
		return err
	}
	parts := strings.Split(entrypoint, "/")
	current := workspace
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrArchiveEntry
		}
		if index < len(parts)-1 && !info.IsDir() {
			return ErrArchiveEntry
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return ErrArchiveEntry
		}
	}
	return nil
}

func ensureWithin(root, candidate string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return ErrArchiveTraversal
	}
	return nil
}

func valueOr(value *string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return *value
}
