package repository

// Site artifact persistence owns immutable storage-path cleanup and public
// artifact resolution. Callers receive only a server-resolved artifact path;
// request URL components never become storage locators.

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) DeleteSiteDeploymentWithArtifact(ctx context.Context, projectID, siteID, deploymentID uuid.UUID, actor SiteActor) (SiteStoragePaths, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return SiteStoragePaths{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return SiteStoragePaths{}, err
	}
	site, err := r.siteByID(ctx, tx, projectID, siteID, true)
	if err != nil {
		return SiteStoragePaths{}, err
	}
	item, sourcePath, artifactPath, err := r.siteDeploymentByIDWithPaths(ctx, tx, projectID, siteID, deploymentID, true)
	if err != nil {
		return SiteStoragePaths{}, err
	}
	if (site.ActiveDeploymentID != nil && *site.ActiveDeploymentID == deploymentID.String()) || item.Status == "active" {
		return SiteStoragePaths{}, ErrSiteDeploymentActive
	}
	if _, err := tx.Exec(ctx, `DELETE FROM site_deployments WHERE project_id=$1 AND site_id=$2 AND id=$3`, projectID, siteID, deploymentID); err != nil {
		return SiteStoragePaths{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_sites SET artifact_used_bytes=GREATEST(0,artifact_used_bytes-$3),artifact_reserved_bytes=GREATEST(0,artifact_reserved_bytes-$4),updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, siteID, item.SizeBytes, item.ReservedBytes); err != nil {
		return SiteStoragePaths{}, err
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site_deployment.delete", "site_deployment", deploymentID, map[string]any{"version": item.Version, "size_bytes": item.SizeBytes}); err != nil {
		return SiteStoragePaths{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteStoragePaths{}, err
	}
	return SiteStoragePaths{SourcePath: sourcePath, ArtifactPath: artifactPath}, nil
}

func (r *Repository) getSiteArtifact(ctx context.Context, siteID, deploymentID uuid.UUID, allowReady bool) (SitePublicArtifact, error) {
	site, err := scanSite(r.pool.QueryRow(ctx, `SELECT `+siteProjection+` FROM project_sites WHERE id=$1`, siteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SitePublicArtifact{}, ErrNotFound
	}
	if err != nil {
		return SitePublicArtifact{}, err
	}
	if !site.Enabled || site.Status != "active" || (!allowReady && site.ActiveDeploymentID == nil) {
		return SitePublicArtifact{}, ErrNotFound
	}
	projectID, err := uuid.Parse(site.ProjectID)
	if err != nil {
		return SitePublicArtifact{}, ErrNotFound
	}
	deployment, artifactPath, err := r.siteDeploymentByID(ctx, r.pool, projectID, siteID, deploymentID, false, true)
	if err != nil {
		return SitePublicArtifact{}, err
	}
	if deployment.BuildStatus != "succeeded" || (deployment.Status != "active" && (!allowReady || deployment.Status != "ready")) {
		return SitePublicArtifact{}, ErrNotFound
	}
	return SitePublicArtifact{Site: site, Deployment: deployment, ArtifactPath: artifactPath}, nil
}

// GetSiteDeploymentArtifact resolves a ready or active immutable Site release
// for a public preview URL. A preview is available only while the Site itself
// remains enabled and active; disabled Sites never leak unpublished artifacts.
func (r *Repository) GetSiteDeploymentArtifact(ctx context.Context, siteID, deploymentID uuid.UUID) (SitePublicArtifact, error) {
	return r.getSiteArtifact(ctx, siteID, deploymentID, true)
}

func (r *Repository) GetActiveSiteArtifact(ctx context.Context, siteID uuid.UUID) (SitePublicArtifact, error) {
	site, err := scanSite(r.pool.QueryRow(ctx, `SELECT `+siteProjection+` FROM project_sites WHERE id=$1`, siteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SitePublicArtifact{}, ErrNotFound
	}
	if err != nil || site.ActiveDeploymentID == nil {
		if err != nil {
			return SitePublicArtifact{}, err
		}
		return SitePublicArtifact{}, ErrNotFound
	}
	deploymentID, err := uuid.Parse(*site.ActiveDeploymentID)
	if err != nil {
		return SitePublicArtifact{}, ErrNotFound
	}
	artifact, err := r.getSiteArtifact(ctx, siteID, deploymentID, false)
	if err != nil {
		return SitePublicArtifact{}, err
	}
	if artifact.Deployment.Status != "active" || artifact.Site.ActiveDeploymentID == nil || *artifact.Site.ActiveDeploymentID != deploymentID.String() {
		return SitePublicArtifact{}, ErrNotFound
	}
	return artifact, nil
}

func validSiteArtifactPath(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		id, err := uuid.Parse(part)
		if err != nil || id == uuid.Nil || id.Version() != uuid.Version(7) {
			return false
		}
	}
	return true
}

func validSiteSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validSiteBuildRuntime(value string) bool {
	switch value {
	case "node-22", "python-3.13", "go-1.24":
		return true
	default:
		return false
	}
}

func validSiteOutputDirectory(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	if value == "." {
		return true
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, char := range part {
			if !(char == '-' || char == '_' || char == '.' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
				return false
			}
		}
	}
	return true
}
