package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInstanceDomainSettingsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run instance domain integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT account_id FROM instance_roles WHERE role='instance_owner'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}

	ownerToken, ownerTokenHash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	ownerSessionID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, ownerSessionID, ownerID, ownerTokenHash, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httpapi.New(config.Config{
		PublicAppURL:       "https://Cloud.Example.com/console",
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("k"), 32),
	}, repository.New(pool), logger)
	testServer := httptest.NewServer(server)
	t.Cleanup(testServer.Close)

	ownerClient := newIntegrationClient(t)
	serverURL, err := url.Parse(testServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	ownerClient.Jar.SetCookies(serverURL, []*http.Cookie{{Name: "stealth_session", Value: ownerToken, Path: "/"}})

	adminClient := newIntegrationClient(t)
	admin := registerAdminTestAccount(t, adminClient, testServer.URL, "domain-admin")
	if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_admin')`, admin.accountID); err != nil {
		t.Fatal(err)
	}
	organizationOwnerClient := newIntegrationClient(t)
	organizationOwner := registerAdminTestAccount(t, organizationOwnerClient, testServer.URL, "domain-org-owner")
	regularClient := newIntegrationClient(t)
	regular := registerAdminTestAccount(t, regularClient, testServer.URL, "domain-regular")

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `UPDATE instance_roles SET role='instance_owner' WHERE account_id=$1`, ownerID)
		_, _ = pool.Exec(cleanupCtx, `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM sessions WHERE id=$1`, ownerSessionID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id IN ($1,$2,$3) OR organization_id IN ($4,$5,$6)`, admin.accountID, organizationOwner.accountID, regular.accountID, admin.organizationID, organizationOwner.organizationID, regular.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM instance_roles WHERE account_id=$1`, admin.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2,$3)`, admin.organizationID, organizationOwner.organizationID, regular.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id IN ($1,$2,$3)`, admin.accountID, organizationOwner.accountID, regular.accountID)
	})

	settingsURL := testServer.URL + "/v1/admin/domain-settings"
	var settings instanceDomainSettingsResponse
	requestJSON(t, ownerClient, http.MethodGet, settingsURL, nil, http.StatusOK, &settings)
	if settings.InstanceHostname != "cloud.example.com" || settings.WorkloadBaseDomain != nil {
		t.Fatalf("initial settings = %+v, want canonical Console hostname and unset workload domain", settings)
	}
	requestJSON(t, adminClient, http.MethodGet, settingsURL, nil, http.StatusOK, &settings)
	requestJSON(t, organizationOwnerClient, http.MethodGet, settingsURL, nil, http.StatusForbidden, nil)
	requestJSON(t, regularClient, http.MethodGet, settingsURL, nil, http.StatusForbidden, nil)
	requestJSON(t, newIntegrationClient(t), http.MethodGet, settingsURL, nil, http.StatusUnauthorized, nil)

	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":" Apps.Example.COM. "}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "apps.example.com")
	assertStoredWorkloadDomain(t, ctx, pool, "apps.example.com")

	for _, client := range []*http.Client{adminClient, organizationOwnerClient, regularClient} {
		requestRawJSON(t, client, http.MethodPatch, settingsURL, `{"workload_base_domain":"blocked.example.com"}`, http.StatusForbidden, nil)
	}
	requestRawJSON(t, newIntegrationClient(t), http.MethodPatch, settingsURL, `{"workload_base_domain":"blocked.example.com"}`, http.StatusUnauthorized, nil)

	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"deploy.example.co.uk"}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "deploy.example.co.uk")
	assertStoredWorkloadDomain(t, ctx, pool, "deploy.example.co.uk")

	for _, value := range []string{
		"127.0.0.1",
		"https://apps.example.com",
		"apps.example.com:443",
		"co.uk",
		"cloud.example.com",
		"example.com",
		"xn--.com",
		"apps.example..com",
	} {
		requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"`+value+`"}`, http.StatusUnprocessableEntity, nil)
	}
	assertStoredWorkloadDomain(t, ctx, pool, "deploy.example.co.uk")

	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{}`, http.StatusBadRequest, nil)
	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":null}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "")
	assertStoredWorkloadDomain(t, ctx, pool, "")

	if _, err := pool.Exec(ctx, `UPDATE instance_roles SET role='instance_admin' WHERE account_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"apps.example.com"}`, http.StatusForbidden, nil)
	if _, err := pool.Exec(ctx, `UPDATE instance_roles SET role='instance_owner' WHERE account_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"apps.example.com"}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "apps.example.com")
}

type instanceDomainSettingsResponse struct {
	InstanceHostname   string  `json:"instance_hostname"`
	WorkloadBaseDomain *string `json:"workload_base_domain"`
}

func assertDomainSettings(t *testing.T, settings instanceDomainSettingsResponse, instanceHostname, workloadBaseDomain string) {
	t.Helper()
	if settings.InstanceHostname != instanceHostname {
		t.Fatalf("instance hostname = %q, want %q", settings.InstanceHostname, instanceHostname)
	}
	if workloadBaseDomain == "" {
		if settings.WorkloadBaseDomain != nil {
			t.Fatalf("workload base domain = %q, want null", *settings.WorkloadBaseDomain)
		}
		return
	}
	if settings.WorkloadBaseDomain == nil || *settings.WorkloadBaseDomain != workloadBaseDomain {
		t.Fatalf("workload base domain = %v, want %q", settings.WorkloadBaseDomain, workloadBaseDomain)
	}
}

func assertStoredWorkloadDomain(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want string) {
	t.Helper()
	var stored *string
	if err := pool.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want == "" {
		if stored != nil {
			t.Fatalf("stored workload base domain = %q, want null", *stored)
		}
		return
	}
	if stored == nil || *stored != want {
		t.Fatalf("stored workload base domain = %v, want %q", stored, want)
	}
}

func requestRawJSON(t *testing.T, client *http.Client, method, endpoint, body string, expectedStatus int, target any) {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		contents, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s: expected %d, got %d: %s", method, endpoint, expectedStatus, response.StatusCode, contents)
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}
