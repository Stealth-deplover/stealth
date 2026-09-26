package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetAppDiagnostics returns a consistent PostgreSQL snapshot. It never reads
// Docker, artifact files, or worker lease identity.
func (r *Repository) GetAppDiagnostics(ctx context.Context, projectID, appID uuid.UUID, actor AppActor) (domain.AppDiagnostics, error) {
	if _, err := r.requireAppRead(ctx, projectID, actor); err != nil {
		return domain.AppDiagnostics{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.AppDiagnostics{}, err
	}
	defer tx.Rollback(ctx)

	app, err := appByID(ctx, tx, projectID, appID, false)
	if err != nil {
		return domain.AppDiagnostics{}, err
	}
	var appliedGeneration *int64
	var failureCount int
	var nextRetryAt, lastFailureAt, lastInspectedAt, lastTransitionAt, lastStartedAt, lastStoppedAt, healthCheckedAt *time.Time
	var desiredID, desiredStatus, desiredBuildStatus, desiredImageDigest, desiredArchiveChecksum, desiredImagePath *string
	var desiredVersion, desiredImageSize *int64
	var appliedID *string
	var appliedVersion *int64
	err = tx.QueryRow(ctx, `
		SELECT runtime.applied_generation,COALESCE(runtime.failure_count,0),runtime.next_retry_at,
		       runtime.last_failure_at,runtime.last_inspected_at,runtime.last_transition_at,
		       runtime.last_started_at,runtime.last_stopped_at,runtime.health_checked_at,
		       desired.id::text,desired.version,desired.status,desired.build_status,desired.image_digest,
		       desired.image_archive_sha256,desired.image_size_bytes,desired.image_path,
		       applied.id::text,applied.version
		FROM project_apps app
		LEFT JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		LEFT JOIN app_deployments desired ON desired.id=app.desired_deployment_id
		  AND desired.app_id=app.id AND desired.project_id=app.project_id
		LEFT JOIN app_deployments applied ON applied.id=runtime.applied_deployment_id
		  AND applied.app_id=app.id AND applied.project_id=app.project_id
		WHERE app.project_id=$1 AND app.id=$2`, projectID, appID).Scan(
		&appliedGeneration, &failureCount, &nextRetryAt, &lastFailureAt, &lastInspectedAt,
		&lastTransitionAt, &lastStartedAt, &lastStoppedAt, &healthCheckedAt,
		&desiredID, &desiredVersion, &desiredStatus, &desiredBuildStatus, &desiredImageDigest,
		&desiredArchiveChecksum, &desiredImageSize, &desiredImagePath,
		&appliedID, &appliedVersion,
	)
	if err != nil {
		return domain.AppDiagnostics{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AppDiagnostics{}, err
	}

	var desiredDeployment, appliedDeployment *domain.AppDiagnosticDeploymentRef
	if desiredID != nil && desiredVersion != nil {
		desiredDeployment = &domain.AppDiagnosticDeploymentRef{ID: *desiredID, Version: *desiredVersion}
	}
	if appliedID != nil && appliedVersion != nil {
		appliedDeployment = &domain.AppDiagnosticDeploymentRef{ID: *appliedID, Version: *appliedVersion}
	}
	desiredArtifactPathValid := false
	if desiredID != nil {
		deploymentID, parseErr := uuid.Parse(*desiredID)
		projectUUID, projectErr := uuid.Parse(app.ProjectID)
		appUUID, appErr := uuid.Parse(app.ID)
		desiredArtifactPathValid = parseErr == nil && projectErr == nil && appErr == nil && desiredImagePath != nil &&
			validAppDeploymentArtifactPath(*desiredImagePath, projectUUID, appUUID, deploymentID)
	}
	desiredArtifactReady := app.DesiredDeploymentID != nil && desiredID != nil && desiredStatus != nil && *desiredStatus == "ready" &&
		desiredBuildStatus != nil && *desiredBuildStatus == "succeeded" && desiredImageDigest != nil && validAppImageDigest(*desiredImageDigest) &&
		desiredArchiveChecksum != nil && validAppSHA256(*desiredArchiveChecksum) && desiredImageSize != nil && *desiredImageSize > 0 &&
		desiredArtifactPathValid

	diagnostics := domain.AppDiagnostics{
		AppID: app.ID, ProjectID: app.ProjectID,
		DesiredGeneration: app.DesiredGeneration, ObservedGeneration: app.ObservedGeneration,
		AppliedGeneration: appliedGeneration, DesiredDeployment: desiredDeployment,
		AppliedDeployment: appliedDeployment, DesiredArtifactReady: desiredArtifactReady,
		RuntimeStatus: app.RuntimeStatus, RuntimeError: app.RuntimeError,
		HealthStatus: app.HealthStatus, RouteStatus: app.RouteStatus,
		FailureCount: failureCount, NextRetryAt: nextRetryAt, LastFailureAt: lastFailureAt,
		LastInspectedAt: lastInspectedAt, LastTransitionAt: lastTransitionAt,
		LastStartedAt: lastStartedAt, LastStoppedAt: lastStoppedAt,
		HealthCheckedAt: healthCheckedAt, Issues: make([]domain.AppDiagnosticIssue, 0, 8),
	}
	diagnostics.ConvergenceStatus = appDiagnosticsConvergence(app, appliedGeneration, appliedDeployment, desiredArtifactReady)
	diagnostics.Issues = appDiagnosticIssues(app, diagnostics)
	return diagnostics, nil
}

func appDiagnosticsConvergence(app domain.App, appliedGeneration *int64, applied *domain.AppDiagnosticDeploymentRef, artifactReady bool) string {
	if !app.Enabled && (app.RuntimeStatus == "stopped" || app.RuntimeStatus == "not_deployed") {
		return "stopped"
	}
	if app.Enabled && app.DesiredDeploymentID == nil {
		return "not_deployed"
	}
	switch app.RuntimeStatus {
	case "failed":
		return "failed"
	case "degraded":
		return "degraded"
	}
	if app.HealthStatus == "unhealthy" {
		return "degraded"
	}
	if app.ObservedGeneration != app.DesiredGeneration || appliedGeneration == nil || *appliedGeneration != app.DesiredGeneration ||
		app.DesiredDeploymentID == nil || applied == nil || applied.ID != *app.DesiredDeploymentID ||
		app.RuntimeStatus != "running" || app.HealthStatus != "healthy" || !artifactReady {
		return "reconciling"
	}
	if app.PlatformHostname != nil && app.RouteStatus != "active" {
		return "reconciling"
	}
	return "converged"
}

func appDiagnosticIssues(app domain.App, diagnostics domain.AppDiagnostics) []domain.AppDiagnosticIssue {
	issues := make([]domain.AppDiagnosticIssue, 0, 8)
	add := func(code, severity, message string) {
		issues = append(issues, domain.AppDiagnosticIssue{Code: code, Severity: severity, Message: message})
	}
	if !app.Enabled {
		add("disabled", "info", "The App is disabled; runtime convergence follows the disabled desired state.")
	}
	if app.Enabled && app.DesiredDeploymentID == nil {
		add("not_deployed", "info", "No deployment is selected. Select a ready deployment to start the App.")
	}
	if app.DesiredDeploymentID != nil && !diagnostics.DesiredArtifactReady {
		add("desired_artifact_unavailable", "error", "The desired deployment is missing verified artifact metadata; the runtime worker will refuse to import it.")
	}
	if app.DesiredDeploymentID != nil && (app.DesiredGeneration != app.ObservedGeneration || diagnostics.AppliedGeneration == nil || *diagnostics.AppliedGeneration != app.DesiredGeneration) {
		applied := "no applied generation"
		if diagnostics.AppliedGeneration != nil {
			applied = fmt.Sprintf("applied generation %d", *diagnostics.AppliedGeneration)
		}
		add("generation_pending", "info", fmt.Sprintf("Generation %d is waiting to replace %s.", app.DesiredGeneration, applied))
	}
	if app.DesiredDeploymentID != nil && (diagnostics.DesiredDeployment == nil || diagnostics.AppliedDeployment == nil || diagnostics.AppliedDeployment.ID != *app.DesiredDeploymentID) {
		add("deployment_pending", "info", "The desired deployment has not yet converged as the applied release.")
	}
	switch app.RuntimeStatus {
	case "failed":
		add("runtime_failed", "error", "The runtime worker marked the desired App generation failed.")
	case "degraded":
		add("runtime_degraded", "warning", "The runtime worker detected drift or an unavailable runtime process.")
	}
	if app.HealthStatus == "pending" && app.DesiredDeploymentID != nil && app.RuntimeStatus == "running" {
		add("health_pending", "info", "A fresh health check has not converged for the current runtime generation.")
	}
	if app.HealthStatus == "unhealthy" {
		add("health_unhealthy", "warning", "The current runtime generation is failing its configured health check.")
	}
	if app.RouteStatus == "waiting_for_runtime" {
		add("route_waiting_runtime", "info", "The public route is waiting for the desired runtime generation.")
	}
	if app.RouteStatus == "waiting_for_health" {
		add("route_waiting_health", "info", "The public route is waiting for fresh health on the desired runtime generation.")
	}
	if diagnostics.NextRetryAt != nil {
		add("retry_scheduled", "info", "Runtime retry is scheduled using the persisted bounded backoff.")
	}
	return issues
}
