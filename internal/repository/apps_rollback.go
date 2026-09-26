package repository

import (
	"context"
	"errors"
	"math"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AppRollbackResult struct {
	App        domain.App
	Deployment domain.AppDeployment
}

// RollbackAppDeployment atomically selects an older immutable release and
// restores the WorkloadSpec captured when that release was built. Environment
// values, App metadata, and worker-owned observed state are left untouched.
func (r *Repository) RollbackAppDeployment(ctx context.Context, projectID, appID, deploymentID uuid.UUID, actor AppActor) (AppRollbackResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AppRollbackResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := r.requireAppWriteTx(ctx, tx, projectID, actor); err != nil {
		return AppRollbackResult{}, err
	}
	app, err := appByID(ctx, tx, projectID, appID, true)
	if err != nil {
		return AppRollbackResult{}, err
	}
	if app.DesiredDeploymentID == nil {
		return AppRollbackResult{}, ErrAppRollbackNotAvailable
	}
	currentDeploymentID, err := uuid.Parse(*app.DesiredDeploymentID)
	if err != nil || currentDeploymentID == uuid.Nil {
		return AppRollbackResult{}, ErrAppRollbackNotAvailable
	}

	var currentVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT version FROM app_deployments
		WHERE project_id=$1 AND app_id=$2 AND id=$3
		FOR UPDATE`, projectID, appID, currentDeploymentID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return AppRollbackResult{}, ErrAppRollbackNotAvailable
	} else if err != nil {
		return AppRollbackResult{}, err
	}
	if currentDeploymentID == deploymentID {
		return AppRollbackResult{}, ErrAppDeploymentAlreadySelected
	}

	target, _, imagePath, _, _, _, _, err := appDeploymentByID(ctx, tx, projectID, appID, deploymentID, true, true)
	if err != nil {
		if errors.Is(err, ErrAppDeploymentSnapshotInvalid) {
			return AppRollbackResult{}, ErrAppRollbackNotAvailable
		}
		return AppRollbackResult{}, err
	}
	if target.Version >= currentVersion {
		return AppRollbackResult{}, ErrAppRollbackNotAvailable
	}
	if target.Status != "ready" || target.BuildStatus != "succeeded" {
		return AppRollbackResult{}, ErrAppDeploymentNotReady
	}
	if !appRollbackArtifactReady(target, imagePath, projectID, appID, deploymentID) {
		return AppRollbackResult{}, ErrAppRollbackNotAvailable
	}

	canonicalSpec, workloadDigest, err := canonicalAppRollbackWorkload(target.WorkloadSnapshot, target.WorkloadSpecSHA256)
	if err != nil {
		return AppRollbackResult{}, ErrAppRollbackNotAvailable
	}
	if app.DesiredGeneration == math.MaxInt64 {
		return AppRollbackResult{}, ErrAppRollbackGenerationLimit
	}
	newGeneration := app.DesiredGeneration + 1
	updated, err := tx.Exec(ctx, `
		UPDATE project_apps
		SET workload_spec=$4::jsonb,workload_spec_sha256=$5,desired_deployment_id=$3,
		    desired_generation=desired_generation+1,runtime_status='pending',runtime_error=NULL,updated_at=now()
		WHERE project_id=$1 AND id=$2 AND desired_generation=$6
		  AND desired_deployment_id=$7`,
		projectID, appID, deploymentID, canonicalSpec, workloadDigest, app.DesiredGeneration, currentDeploymentID)
	if err != nil {
		return AppRollbackResult{}, err
	}
	if updated.RowsAffected() != 1 {
		return AppRollbackResult{}, ErrAppRollbackNotAvailable
	}
	if err := resetAppRuntimeRetryTx(ctx, tx, appID); err != nil {
		return AppRollbackResult{}, err
	}
	metadata := map[string]any{
		"app_id":                    appID.String(),
		"from_deployment_id":        currentDeploymentID.String(),
		"from_version":              currentVersion,
		"to_deployment_id":          deploymentID.String(),
		"to_version":                target.Version,
		"from_workload_spec_sha256": app.WorkloadSpecSHA256,
		"to_workload_spec_sha256":   workloadDigest,
		"new_desired_generation":    newGeneration,
	}
	if err := r.auditAppDeploymentTx(ctx, tx, projectID, &actor, "app_deployment.rollback", deploymentID, metadata); err != nil {
		return AppRollbackResult{}, err
	}
	updatedApp, err := appByID(ctx, tx, projectID, appID, false)
	if err != nil {
		return AppRollbackResult{}, err
	}
	updatedTarget, _, _, _, _, _, _, err := appDeploymentByID(ctx, tx, projectID, appID, deploymentID, false, false)
	if err != nil {
		return AppRollbackResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppRollbackResult{}, err
	}
	return AppRollbackResult{App: updatedApp, Deployment: updatedTarget}, nil
}

func validAppImageDigest(value string) bool {
	return len(value) == len("sha256:")+64 && value[:len("sha256:")] == "sha256:" && validAppSHA256(value[len("sha256:"):])
}

func appRollbackArtifactReady(target domain.AppDeployment, imagePath *string, projectID, appID, deploymentID uuid.UUID) bool {
	return target.ImageDigest != nil && validAppImageDigest(*target.ImageDigest) &&
		target.ImageArchiveSHA256 != nil && validAppSHA256(*target.ImageArchiveSHA256) &&
		target.ImageSizeBytes != nil && *target.ImageSizeBytes > 0 &&
		imagePath != nil && validAppDeploymentArtifactPath(*imagePath, projectID, appID, deploymentID)
}

func canonicalAppRollbackWorkload(snapshot workloadspec.Spec, storedDigest string) ([]byte, string, error) {
	canonical, err := workloadspec.MarshalCanonical(snapshot)
	if err != nil {
		return nil, "", err
	}
	digest, err := workloadspec.Digest(snapshot)
	if err != nil || digest != storedDigest {
		return nil, "", ErrAppDeploymentSnapshotInvalid
	}
	return canonical, digest, nil
}
