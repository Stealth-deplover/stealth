package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAppsAPIControlPlaneAuthorizationAndProjectionIntegration(t *testing.T) {
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
	var previousWorkloadDomain *string
	if err := pool.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousWorkloadDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		var restore any
		if previousWorkloadDomain != nil {
			restore = *previousWorkloadDomain
		}
		_, _ = pool.Exec(cleanupCtx, `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, restore)
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName: "stealth_session",
		SessionTTL:        time.Hour,
		AppSessionTTL:     2 * time.Hour,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewMemoryLimiter()}))
	t.Cleanup(server.Close)

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	var ownerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("apps-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &ownerRegistration)

	var firstProject, secondProject struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{
		"name": "apps-first-" + ownerID.String()[:8],
	}, http.StatusCreated, &firstProject)
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{
		"name": "apps-second-" + ownerID.String()[:8],
	}, http.StatusCreated, &secondProject)
	projectURL := server.URL + "/v1/projects/" + firstProject.Project.ID
	secondProjectURL := server.URL + "/v1/projects/" + secondProject.Project.ID

	viewerClient := newIntegrationClient(t)
	viewerID := uuid.Must(uuid.NewV7())
	var viewerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("apps-viewer-%s@example.test", viewerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &viewerRegistration)
	adminClient := newIntegrationClient(t)
	adminID := uuid.Must(uuid.NewV7())
	var adminRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, adminClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("apps-admin-%s@example.test", adminID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &adminRegistration)
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'viewer')`, ownerRegistration.Organization.ID, viewerRegistration.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'admin')`, ownerRegistration.Organization.ID, adminRegistration.Account.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id IN ($1,$2,$3) OR actor_account_id IN ($4,$5,$6)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID, adminRegistration.Organization.ID, ownerRegistration.Account.ID, viewerRegistration.Account.ID, adminRegistration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2,$3)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID, adminRegistration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id IN ($1,$2,$3)`, ownerRegistration.Account.ID, viewerRegistration.Account.ID, adminRegistration.Account.ID)
	})

	createKey := func(project, name string, scopes []string) string {
		t.Helper()
		var response struct {
			Key struct {
				ID string `json:"id"`
			} `json:"key"`
			Secret string `json:"secret"`
		}
		requestJSON(t, ownerClient, http.MethodPost, project+"/api-keys", map[string]any{"name": name, "scopes": scopes}, http.StatusCreated, &response)
		if response.Key.ID == "" || response.Secret == "" {
			t.Fatalf("API key response was incomplete for %s", name)
		}
		return response.Secret
	}
	readSecret := createKey(projectURL, "Apps read scope", []string{"apps.read"})
	writeSecret := createKey(projectURL, "Apps write scope", []string{"apps.write"})
	wrongProjectSecret := createKey(secondProjectURL, "Wrong project Apps key", []string{"apps.read"})
	readHeaders := map[string]string{"X-Stealth-Key": readSecret}
	writeHeaders := map[string]string{"X-Stealth-Key": writeSecret}
	wrongProjectHeaders := map[string]string{"X-Stealth-Key": wrongProjectSecret}

	requestJSON(t, viewerClient, http.MethodPost, projectURL+"/apps", map[string]any{"name": "viewer-app"}, http.StatusForbidden, nil)

	var created struct {
		App domain.App `json:"app"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/apps", map[string]any{
		"name": "api-service",
		"workload": map[string]any{
			"schema_version": "v1",
			"port":           8080,
			"health_check": map[string]any{
				"protocol": "http",
				"path":     "/healthz",
			},
		},
	}, http.StatusCreated, &created)
	if created.App.ID == "" || created.App.RuntimeStatus != "not_deployed" || created.App.DesiredGeneration != 1 || created.App.ObservedGeneration != 0 || created.App.RuntimeError != nil {
		t.Fatalf("created App fabricated runtime state: %+v", created.App)
	}
	if created.App.Workload.SchemaVersion != "v1" || created.App.Workload.Port != 8080 || created.App.Workload.Resources.MemoryBytes != 536870912 || created.App.Workload.RestartPolicy != "always" {
		t.Fatalf("created App did not return complete normalized WorkloadSpec: %+v", created.App.Workload)
	}
	if len(created.App.WorkloadSpecSHA256) != 64 || created.App.PlatformHostname != nil {
		t.Fatalf("App digest or unset platform hostname projection is invalid: %+v", created.App)
	}
	var memberPage struct {
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, viewerClient, http.MethodGet, projectURL+"/apps", nil, http.StatusOK, &memberPage)
	if memberPage.CanManage {
		t.Fatal("read-only project member received App management capability")
	}
	var adminPage struct {
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, adminClient, http.MethodGet, projectURL+"/apps", nil, http.StatusOK, &adminPage)
	if !adminPage.CanManage {
		t.Fatal("project admin did not receive App management capability")
	}
	requestJSON(t, adminClient, http.MethodPatch, projectURL+"/apps/"+created.App.ID, map[string]any{"name": created.App.Name}, http.StatusOK, nil)
	requestJSON(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps", nil, http.StatusUnauthorized, nil)
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/apps/"+created.App.ID, nil, http.StatusOK, &struct{}{})
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/apps/"+created.App.ID+"/deploy", nil, http.StatusNotFound, nil)

	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps", nil, http.StatusOK, readHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps/"+created.App.ID, nil, http.StatusOK, readHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/apps", map[string]any{"name": "read-key-write"}, http.StatusForbidden, readHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/apps", map[string]any{"name": "write-key-app"}, http.StatusCreated, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps", nil, http.StatusForbidden, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps", nil, http.StatusUnauthorized, wrongProjectHeaders)

	for _, field := range []string{
		"platform_label",
		"platform_hostname",
		"runtime_status",
		"runtime_error",
		"desired_generation",
		"observed_generation",
		"workload_spec_sha256",
	} {
		requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/apps/"+created.App.ID, map[string]any{field: "client-value"}, http.StatusBadRequest, nil)
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/apps", map[string]any{
		"name":     "unsafe-workload",
		"workload": map[string]any{"port": 8080, "privileged": true},
	}, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/apps", map[string]any{
		"name":     "future-workload",
		"workload": map[string]any{"schema_version": "v2", "port": 8080},
	}, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/apps/"+created.App.ID, map[string]any{}, http.StatusUnprocessableEntity, nil)

	newName := "backend"
	var renamed struct {
		App domain.App `json:"app"`
	}
	requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/apps/"+created.App.ID, map[string]any{"name": newName}, http.StatusOK, &renamed)
	if renamed.App.DesiredGeneration != 1 || renamed.App.WorkloadSpecSHA256 != created.App.WorkloadSpecSHA256 || renamed.App.RuntimeStatus != "not_deployed" {
		t.Fatalf("rename changed App runtime identity: %+v", renamed.App)
	}

	var page struct {
		Apps       []domain.App `json:"apps"`
		Pagination struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
		CanManage bool `json:"can_manage"`
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps?limit=1", nil, http.StatusOK, readHeaders)
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/apps?limit=1", nil, http.StatusOK, &page)
	if len(page.Apps) != 1 || !page.CanManage || page.Pagination.NextCursor == nil {
		t.Fatalf("App pagination response = %+v", page)
	}
	var plan struct {
		Plan struct {
			Limits struct {
				Apps int64 `json:"apps"`
			} `json:"limits"`
			Usage struct {
				Apps int64 `json:"apps"`
			} `json:"usage"`
		} `json:"plan"`
	}
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/plan", nil, http.StatusOK, &plan)
	if plan.Plan.Limits.Apps != 3 || plan.Plan.Usage.Apps != 2 {
		t.Fatalf("organization App plan projection = limit %d, usage %d", plan.Plan.Limits.Apps, plan.Plan.Usage.Apps)
	}

	var appAudit struct {
		Events []struct {
			Action   string         `json:"action"`
			TargetID string         `json:"target_id"`
			Metadata map[string]any `json:"metadata"`
		} `json:"events"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/audit-events", nil, http.StatusOK, &appAudit)
	seenCreate := false
	for _, event := range appAudit.Events {
		if event.Action == "app.create" && event.TargetID == created.App.ID {
			seenCreate = true
			if _, exists := event.Metadata["workload"]; exists {
				t.Fatal("App audit event exposed WorkloadSpec")
			}
		}
	}
	if !seenCreate {
		t.Fatal("App creation did not write app.create audit event")
	}
	requestJSON(t, ownerClient, http.MethodPut, projectURL+"/service-layout", map[string]any{
		"layout": []map[string]any{{"resource_type": "app", "resource_id": created.App.ID, "x": 12, "y": 34}},
	}, http.StatusOK, nil)

	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/apps/"+created.App.ID, nil, http.StatusNoContent, nil)
	var layoutAfterDelete struct {
		Layout []any `json:"layout"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/service-layout", nil, http.StatusOK, &layoutAfterDelete)
	if len(layoutAfterDelete.Layout) != 0 {
		t.Fatalf("deleted App remained in effective service layout: %+v", layoutAfterDelete.Layout)
	}
	var claimCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_hostname_claims WHERE resource_type='app' AND resource_id=$1`, uuid.MustParse(created.App.ID)).Scan(&claimCount); err != nil {
		t.Fatal(err)
	}
	if claimCount != 0 {
		t.Fatalf("App deletion left %d platform claims", claimCount)
	}
}
