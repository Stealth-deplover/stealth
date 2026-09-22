package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/sitestore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlatformSiteAPIAndNarrowListenerIntegration(t *testing.T) {
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
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	repo := repository.New(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var previousWorkloadDomain *string
	if err := pool.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousWorkloadDomain); err != nil {
		t.Fatal(err)
	}
	controlHandler, platformHandler := httpapi.NewWithDependenciesAndPlatformSiteHandler(config.Config{
		StorageRoot:              root,
		StorageMaxFileSize:       1 << 20,
		StorageDefaultQuotaBytes: 1 << 20,
		SitesMaxArtifactSize:     1 << 20,
		SitesDefaultQuotaBytes:   1 << 20,
		SitesMaxExpandedBytes:    1 << 20,
		SitesMaxFiles:            64,
		FunctionsSecretKey:       bytes.Repeat([]byte("k"), 32),
		SessionCookieName:        "stealth_session",
		SessionTTL:               time.Hour,
	}, repo, logger, httpapi.Dependencies{})
	controlServer := httptest.NewServer(controlHandler)
	defer controlServer.Close()
	platformServer := httptest.NewServer(platformHandler)
	defer platformServer.Close()

	ownerClient := newIntegrationClient(t)
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, controlServer.URL+"/v1/account/registrations", map[string]string{
		"email":    "platform-api-" + uuid.Must(uuid.NewV7()).String() + "@example.test",
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		restoredDomain := any(nil)
		if previousWorkloadDomain != nil {
			restoredDomain = *previousWorkloadDomain
		}
		_, _ = pool.Exec(cleanupCtx, `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, restoredDomain)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, registration.Account.ID, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, controlServer.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "platform-api-project"}, http.StatusCreated, &project)
	projectURL := controlServer.URL + "/v1/projects/" + project.Project.ID
	var created struct {
		Site struct {
			ID               string  `json:"id"`
			PlatformHostname *string `json:"platform_hostname"`
		} `json:"site"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/sites", map[string]string{"name": "portfolio"}, http.StatusCreated, &created)
	if created.Site.PlatformHostname != nil {
		t.Fatalf("new Site hostname before workload domain = %q", *created.Site.PlatformHostname)
	}

	if _, err := pool.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain='apps.example.com',updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	var fetched struct {
		Site struct {
			ID               string  `json:"id"`
			PlatformHostname *string `json:"platform_hostname"`
		} `json:"site"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/sites/"+created.Site.ID, nil, http.StatusOK, &fetched)
	if fetched.Site.PlatformHostname == nil || *fetched.Site.PlatformHostname != "portfolio.apps.example.com" {
		t.Fatalf("Site API platform hostname = %v", fetched.Site.PlatformHostname)
	}

	projectID := uuid.MustParse(project.Project.ID)
	siteID := uuid.MustParse(created.Site.ID)
	deploymentID := uuid.Must(uuid.NewV7())
	relative, err := sitestore.ArtifactRelativePath(projectID, siteID, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("<!doctype html><title>platform</title>")
	artifactDirectory := filepath.Join(root, "sites", filepath.FromSlash(relative))
	if err := os.MkdirAll(artifactDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "index.html"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])
	accountID := uuid.MustParse(registration.Account.ID)
	if _, err := repo.CreateSiteDeployment(ctx, deploymentID, projectID, siteID, repository.SiteActor{Kind: repository.SiteConsoleActor, AccountID: accountID}, repository.SiteDeploymentInput{
		Source:             "upload",
		SizeBytes:          int64(len(content)),
		ArchiveSizeBytes:   int64(len(content)),
		ChecksumSHA256:     checksumHex,
		ArtifactPath:       relative,
		CreatedByAccountID: &accountID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ActivateSiteDeployment(ctx, projectID, siteID, deploymentID, repository.SiteActor{Kind: repository.SiteConsoleActor, AccountID: accountID}); err != nil {
		t.Fatal(err)
	}

	platformResponse := platformRequest(t, platformServer.URL+"/", "portfolio.apps.example.com")
	if platformResponse.StatusCode != http.StatusOK || string(platformResponse.Body) != string(content) {
		t.Fatalf("platform listener response = %d %q", platformResponse.StatusCode, platformResponse.Body)
	}
	if _, err := pool.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain='deploy.example.net',updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	oldDomainResponse := platformRequest(t, platformServer.URL+"/", "portfolio.apps.example.com")
	if oldDomainResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("old platform hostname remained public after domain change with status %d", oldDomainResponse.StatusCode)
	}
	if _, err := pool.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain='apps.example.com',updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/account", "/healthz", "/readyz", "/version", "/metrics"} {
		response := platformRequest(t, platformServer.URL+path, "portfolio.apps.example.com")
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("platform listener exposed control path %s with status %d body %q", path, response.StatusCode, response.Body)
		}
	}
	unknown := platformRequest(t, platformServer.URL+"/", "unknown.apps.example.com")
	if unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown platform hostname status = %d", unknown.StatusCode)
	}
	if _, err := repo.UpdateSite(ctx, projectID, siteID, repository.SiteActor{Kind: repository.SiteConsoleActor, AccountID: accountID}, repository.SitePatch{Enabled: boolPtrForHTTP(false)}); err != nil {
		t.Fatal(err)
	}
	disabled := platformRequest(t, platformServer.URL+"/", "portfolio.apps.example.com")
	if disabled.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled Site remained public with status %d", disabled.StatusCode)
	}
}

type platformHTTPResponse struct {
	StatusCode int
	Body       []byte
}

func platformRequest(t *testing.T, endpoint, host string) platformHTTPResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return platformHTTPResponse{StatusCode: response.StatusCode, Body: body}
}

func boolPtrForHTTP(value bool) *bool {
	return &value
}
