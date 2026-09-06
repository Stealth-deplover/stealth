package functionrunner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/repository"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.Repository.ClaimNextFunctionExecution(ctx, w.WorkerID)
	if errors.Is(err, repository.ErrNoExecutionJob) {
		return false, nil
	}
	if err != nil {
		if metrics := w.Metrics; metrics != nil {
			metrics.Errors.WithLabelValues("claim").Inc()
		}
		return false, err
	}
	metrics := w.Metrics
	started := time.Now()
	if metrics != nil {
		metrics.JobsClaimed.Inc()
		metrics.InFlight.Inc()
	}
	spanContext, span := observability.StartWorkerSpan(ctx, "functions.execute", attribute.String("stealth.function.runtime", job.Function.Runtime))
	err = w.handle(spanContext, job)
	if err != nil {
		span.RecordError(errors.New("function execution failed"))
		span.SetStatus(codes.Error, "function execution failed")
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
	if metrics != nil {
		metrics.InFlight.Dec()
		result := "finished"
		if err != nil {
			result = "error"
			metrics.Errors.WithLabelValues("process").Inc()
		}
		metrics.JobDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
	}
	return true, err
}

func (w *Worker) handle(parent context.Context, job repository.FunctionExecutionJob) error {
	if !job.Function.Enabled || job.Function.Status != "active" {
		return w.fail(parent, job, "function is disabled")
	}
	if job.Deployment.BuildStatus != "succeeded" {
		return w.fail(parent, job, "function build is not ready")
	}
	jobID := job.Execution.ID
	if _, err := uuid.Parse(jobID); err != nil {
		return w.fail(parent, job, "execution id is invalid")
	}
	stagingSubpath := filepath.ToSlash(filepath.Join("jobs", jobID))
	if !safeVolumeSubpath(stagingSubpath) {
		return w.fail(parent, job, "execution workspace path is invalid")
	}
	workspace := filepath.Join(w.StagingRoot, filepath.FromSlash(stagingSubpath))
	if err := ensureWithin(w.StagingRoot, workspace); err != nil {
		return w.fail(parent, job, "execution workspace path is invalid")
	}
	// A stale directory can only belong to this UUID and the worker-owned
	// staging root. Remove it before extraction so no previous archive entry is
	// accidentally reused after a retry.
	if err := os.RemoveAll(workspace); err != nil {
		return w.fail(parent, job, "execution workspace could not be reset")
	}
	if err := os.MkdirAll(workspace, defaultSourceDirMode); err != nil {
		return w.fail(parent, job, "execution workspace could not be created")
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	artifactPath := strings.TrimSpace(job.BuildPath)
	artifactChecksum := strings.TrimSpace(job.BuildChecksumSHA256)
	if artifactPath == "" || artifactChecksum == "" {
		return w.fail(parent, job, "function build artifact is unavailable")
	}
	archive, err := w.Store.OpenRelative(artifactPath)
	if err != nil {
		return w.fail(parent, job, "function build artifact is unavailable")
	}
	// The database checksum is the integrity boundary between the API upload
	// path and the worker. Hash the exact opaque bytes while Extract parses the
	// archive so a locally tampered artifact can never be executed silently.
	checkedArchive := newChecksumReader(archive)
	stats, extractErr := ExtractTrusted(parent, checkedArchive, "build.tar", workspace, w.ArchiveLimit)
	_ = archive.Close()
	if extractErr != nil {
		return w.fail(parent, job, redactFailure(extractErr.Error(), nil))
	}
	if !strings.EqualFold(artifactChecksum, checkedArchive.SumHex()) {
		return w.fail(parent, job, "function build artifact checksum mismatch")
	}
	if stats.Files == 0 {
		return w.fail(parent, job, "function source archive contains no files")
	}
	if err := validateEntrypointFile(workspace, job.Function.Entrypoint); err != nil {
		return w.fail(parent, job, "function entrypoint is unavailable")
	}
	variables, err := w.Repository.FunctionRuntimeVariablesForDeployment(parent, mustUUID(job.Execution.ProjectID), mustUUID(job.Execution.FunctionID), mustUUID(job.Execution.DeploymentID), w.Cipher)
	if err != nil {
		return w.fail(parent, job, "function runtime variables are unavailable")
	}
	secrets := make([]string, 0, len(variables))
	for _, variable := range variables {
		if variable.IsSecret {
			secrets = append(secrets, variable.Value)
		}
	}
	timeout := time.Duration(job.Function.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		return w.fail(parent, job, "function timeout is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	runtimeJob := job
	// Build commands already ran while producing the immutable artifact. The
	// invocation receives a read-only copy and must never repeat installation
	// or arbitrary build steps on every request.
	runtimeJob.Function.Commands = ""
	result, runErr := w.Executor.Execute(ctx, runtimeJob, stagingSubpath, variables)
	cancel()
	redactedStdout := Redact(result.Stdout, secrets)
	redactedStderr := Redact(result.Stderr, secrets)
	if job.Function.Logging {
		if strings.TrimSpace(redactedStderr) != "" {
			if err := w.appendLog(parent, job, "error", redactedStderr); err != nil {
				w.Logger.Error("append function execution log failed", "execution_id", jobID, "error", err)
			}
		}
	}
	if runErr != nil {
		message := redactedStderr
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			message = "function execution timed out"
		} else if strings.TrimSpace(message) == "" {
			message = runErr.Error()
		}
		return w.fail(parent, job, message, secrets...)
	}
	if result.Truncated {
		return w.fail(parent, job, ErrOutputTooLarge.Error(), secrets...)
	}
	output, contentType := normalizeOutput(redactedStdout)
	status := 200
	_, err = w.Repository.TransitionFunctionExecutionResultForWorker(parent, mustUUID(job.Execution.ProjectID), mustUUID(job.Execution.FunctionID), mustUUID(jobID), w.WorkerID, "succeeded", "", &status, output, &contentType)
	if metrics := w.Metrics; metrics != nil {
		if err != nil {
			metrics.Errors.WithLabelValues("transition").Inc()
		} else {
			metrics.JobsCompleted.WithLabelValues("succeeded").Inc()
		}
	}
	return err
}

func (w *Worker) fail(ctx context.Context, job repository.FunctionExecutionJob, message string, secrets ...string) error {
	message = Redact(message, secrets)
	message, _ = executionErrorText(message)
	_, err := w.Repository.TransitionFunctionExecutionResultForWorker(ctx, mustUUID(job.Execution.ProjectID), mustUUID(job.Execution.FunctionID), mustUUID(job.Execution.ID), w.WorkerID, "failed", message, nil, nil, nil)
	if metrics := w.Metrics; metrics != nil {
		if err != nil {
			metrics.Errors.WithLabelValues("transition").Inc()
		} else {
			metrics.JobsCompleted.WithLabelValues("failed").Inc()
		}
	}
	return err
}

func (w *Worker) appendLog(ctx context.Context, job repository.FunctionExecutionJob, level, message string) error {
	message, _ = executionLogText(message)
	if strings.TrimSpace(message) == "" {
		return nil
	}
	_, err := w.Repository.AppendFunctionExecutionLog(ctx, mustUUID(job.Execution.ProjectID), mustUUID(job.Execution.FunctionID), mustUUID(job.Execution.ID), uuid.Must(uuid.NewV7()), 0, level, message)
	return err
}

func normalizeOutput(value string) (json.RawMessage, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return json.RawMessage(`{}`), "application/json"
	}
	if json.Valid([]byte(value)) {
		return json.RawMessage(value), "application/json"
	}
	encoded, _ := json.Marshal(value)
	return json.RawMessage(encoded), "text/plain; charset=utf-8"
}
