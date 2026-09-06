package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/ratelimit"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/sitestore"
)

func TestSiteDeploymentPreviewIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := repository.New(pool)
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		StorageRoot:              root,
		StorageMaxFileSize:       1 << 20,
		StorageDefaultQuotaBytes: 1 << 20,
		SitesMaxArtifactSize:     1 << 20,
		SitesDefaultQuotaBytes:   1 << 20,
		SitesMaxExpandedBytes:    1 << 20,
		SitesMaxFiles:            64,
		FunctionsSecretKey:       bytes.Repeat([]byte{0x41}, 32),
		SessionCookieName:        "stealth_session",
		SessionTTL:               time.Hour,
		AppSessionTTL:            time.Hour,
	}, repo, logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewMemoryLimiter()}))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("site-preview-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, registration.Organization.ID, registration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{
		"name": "site-preview-" + ownerID.String()[:8],
	}, http.StatusCreated, &project)
	projectID := uuid.MustParse(project.Project.ID)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID
	var site struct {
		Site struct {
			ID string `json:"id"`
		} `json:"site"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/sites", map[string]string{"name": "preview-site"}, http.StatusCreated, &site)
	siteID := uuid.MustParse(site.Site.ID)
	deploymentID := uuid.Must(uuid.NewV7())
	relative, err := sitestore.ArtifactRelativePath(projectID, siteID, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("<!doctype html><title>preview</title>")
	artifactDir := filepath.Join(root, "sites", filepath.FromSlash(relative))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "index.html"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])
	accountID := uuid.MustParse(registration.Account.ID)
	sourceName := "preview.zip"
	deployment, err := repo.CreateSiteDeployment(ctx, deploymentID, projectID, siteID, repository.SiteActor{
		Kind:      repository.SiteConsoleActor,
		AccountID: accountID,
	}, repository.SiteDeploymentInput{
		Source:             "upload",
		SourceName:         &sourceName,
		SizeBytes:          int64(len(content)),
		ArchiveSizeBytes:   int64(len(content)),
		ChecksumSHA256:     checksumHex,
		ArtifactPath:       relative,
		CreatedByAccountID: &accountID,
		Activate:           false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Status != "ready" || deployment.BuildStatus != "succeeded" {
		t.Fatalf("preview deployment status = %s/%s", deployment.Status, deployment.BuildStatus)
	}

	previewURL := server.URL + "/v1/sites/" + site.Site.ID + "/deployments/" + deployment.ID
	previewResponse, err := newIntegrationClient(t).Get(previewURL)
	if err != nil {
		t.Fatal(err)
	}
	previewBody, readErr := io.ReadAll(previewResponse.Body)
	_ = previewResponse.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if previewResponse.StatusCode != http.StatusOK || !bytes.Equal(previewBody, content) {
		t.Fatalf("preview response = %d %q", previewResponse.StatusCode, previewBody)
	}

	activeResponse, err := newIntegrationClient(t).Get(server.URL + "/v1/sites/" + site.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = activeResponse.Body.Close()
	if activeResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("inactive Site response = %d, want 404", activeResponse.StatusCode)
	}

	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/sites/"+site.Site.ID+"/deployments/"+deployment.ID+"/activate", nil, http.StatusOK, &struct{}{})
	activeResponse, err = newIntegrationClient(t).Get(server.URL + "/v1/sites/" + site.Site.ID + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	activeBody, readErr := io.ReadAll(activeResponse.Body)
	_ = activeResponse.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if activeResponse.StatusCode != http.StatusOK || !bytes.Equal(activeBody, content) {
		t.Fatalf("active response = %d %q", activeResponse.StatusCode, activeBody)
	}

	requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/sites/"+site.Site.ID, map[string]bool{"enabled": false}, http.StatusOK, &struct{}{})
	disabledPreview, err := newIntegrationClient(t).Get(previewURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = disabledPreview.Body.Close()
	if disabledPreview.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled Site preview response = %d, want 404", disabledPreview.StatusCode)
	}
}
