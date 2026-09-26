package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type appRuntimeLogTelemetryFake struct {
	query  telemetry.ContainerLogsQuery
	result telemetry.ContainerLogsResult
	err    error
	calls  int
}

func (*appRuntimeLogTelemetryFake) Ping(context.Context) error { return nil }
func (*appRuntimeLogTelemetryFake) QueryLogs(context.Context, telemetry.LogsQuery) (telemetry.LogsResult, error) {
	return telemetry.LogsResult{}, nil
}
func (*appRuntimeLogTelemetryFake) QueryTraces(context.Context, telemetry.TracesQuery) (telemetry.TracesResult, error) {
	return telemetry.TracesResult{}, nil
}
func (*appRuntimeLogTelemetryFake) QueryMetrics(context.Context, telemetry.MetricsQuery) (telemetry.MetricsResult, error) {
	return telemetry.MetricsResult{}, nil
}
func (*appRuntimeLogTelemetryFake) ListSources(context.Context, telemetry.SourcesQuery) (telemetry.SourcesResult, error) {
	return telemetry.SourcesResult{}, nil
}
func (f *appRuntimeLogTelemetryFake) QueryContainerLogs(_ context.Context, query telemetry.ContainerLogsQuery) (telemetry.ContainerLogsResult, error) {
	f.calls++
	f.query = query
	return f.result, f.err
}

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
	runtimeLogs := &appRuntimeLogTelemetryFake{result: telemetry.ContainerLogsResult{Items: []telemetry.ContainerLogRecord{{
		Timestamp: time.Now().UTC(), Severity: "INFO", Body: "runtime-output-marker", EventID: "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4d3c",
	}}}}
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		StorageRoot:                   t.TempDir(),
		AppsMaxSourceArchiveBytes:     128 << 20,
		AppsMaxExpandedSourceBytes:    1 << 30,
		AppsMaxSourceFiles:            8192,
		AppsMaxImageArchiveBytes:      2 << 30,
		AppsDefaultArtifactQuotaBytes: 5 << 30,
		AppsSecretKey:                 []byte(strings.Repeat("a", 32)),
		SessionCookieName:             "stealth_session",
		SessionTTL:                    time.Hour,
		AppSessionTTL:                 2 * time.Hour,
		TelemetryMaxQueryRange:        2 * time.Hour,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewMemoryLimiter(), TelemetryStore: runtimeLogs}))
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
	variablesURL := projectURL + "/apps/" + created.App.ID + "/variables"
	requestJSON(t, newIntegrationClient(t), http.MethodGet, variablesURL, nil, http.StatusUnauthorized, nil)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, variablesURL, nil, http.StatusForbidden, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, secondProjectURL+"/apps/"+created.App.ID+"/variables", nil, http.StatusUnauthorized, wrongProjectHeaders)
	requestJSON(t, ownerClient, http.MethodGet, secondProjectURL+"/apps/"+created.App.ID+"/variables", nil, http.StatusNotFound, nil)
	secretValue := "API-SECRET-HTTP-NEVER-RETURN-THIS"
	variableBody := requestJSONRaw(t, ownerClient, http.MethodPost, variablesURL, map[string]any{
		"key": "PROVIDER_TOKEN", "is_secret": true, "value": secretValue, "description": "provider access token",
	}, http.StatusCreated)
	if strings.Contains(string(variableBody), secretValue) || strings.Contains(string(variableBody), "ciphertext") || strings.Contains(string(variableBody), "nonce") {
		t.Fatalf("App environment mutation response exposed plaintext or crypto fields: %s", variableBody)
	}
	var variableResponse struct {
		Variable domain.AppEnvironmentVariable `json:"variable"`
	}
	if err := json.Unmarshal(variableBody, &variableResponse); err != nil {
		t.Fatal(err)
	}
	if variableResponse.Variable.Key != "PROVIDER_TOKEN" || !variableResponse.Variable.IsSecret || !variableResponse.Variable.HasValue || variableResponse.Variable.ID == "" {
		t.Fatalf("App environment response metadata = %+v", variableResponse.Variable)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, variablesURL, map[string]any{"key": "READ_DENIED", "value": "x"}, http.StatusForbidden, readHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, variablesURL, map[string]any{"key": "WRITE_ALLOWED", "value": "written-by-key"}, http.StatusCreated, writeHeaders)
	listBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, variablesURL, nil, http.StatusOK, readHeaders)
	if strings.Contains(string(listBody), secretValue) || strings.Contains(string(listBody), "ciphertext") {
		t.Fatalf("App environment list exposed plaintext or ciphertext: %s", listBody)
	}
	var variablesPage struct {
		Variables []domain.AppEnvironmentVariable `json:"variables"`
		CanManage bool                            `json:"can_manage"`
	}
	if err := json.Unmarshal(listBody, &variablesPage); err != nil {
		t.Fatal(err)
	}
	if len(variablesPage.Variables) != 2 || variablesPage.CanManage {
		t.Fatalf("apps.read variable page = %+v", variablesPage)
	}
	oldGeneration := created.App.DesiredGeneration
	requestJSON(t, ownerClient, http.MethodPatch, variablesURL+"/"+variableResponse.Variable.ID, map[string]any{"description": "rotated by ops"}, http.StatusOK, nil)
	var updatedApp struct {
		App domain.App `json:"app"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/apps/"+created.App.ID, nil, http.StatusOK, &updatedApp)
	if updatedApp.App.DesiredGeneration != oldGeneration+2 {
		t.Fatalf("metadata-only edit advanced desired generation; current=%d prior=%d", updatedApp.App.DesiredGeneration, oldGeneration)
	}
	newSecretValue := "APP-SECRET-REPLACED"
	requestJSON(t, ownerClient, http.MethodPatch, variablesURL+"/"+variableResponse.Variable.ID, map[string]any{"value": newSecretValue}, http.StatusOK, nil)
	var auditMetadata string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(string_agg(metadata::text,' '),'') FROM audit_events WHERE target_id=$1`, created.App.ID).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditMetadata, secretValue) || strings.Contains(auditMetadata, newSecretValue) {
		t.Fatalf("App environment plaintext appeared in audit metadata: %s", auditMetadata)
	}
	requestJSON(t, ownerClient, http.MethodDelete, variablesURL+"/"+variableResponse.Variable.ID, nil, http.StatusNoContent, nil)
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
	var writeKeyApp struct {
		App domain.App `json:"app"`
	}
	writeKeyAppBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/apps", map[string]any{"name": "write-key-app"}, http.StatusCreated, writeHeaders)
	if err := json.Unmarshal(writeKeyAppBody, &writeKeyApp); err != nil {
		t.Fatal(err)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps", nil, http.StatusForbidden, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps", nil, http.StatusUnauthorized, wrongProjectHeaders)

	// Runtime log source IDs are inserted here only to exercise the HTTP read
	// boundary. Production mappings are registered by fenced runtime converge.
	containerID := strings.Repeat("c", 64)
	if _, err := pool.Exec(ctx, `INSERT INTO app_runtime_log_sources (project_id,app_id,container_id) VALUES ($1,$2,$3)`, firstProject.Project.ID, created.App.ID, containerID); err != nil {
		t.Fatal(err)
	}
	logsURL := projectURL + "/apps/" + created.App.ID + "/logs?container_id=" + strings.Repeat("f", 64)
	var runtimeLogsPage struct {
		Logs       []domain.AppRuntimeLog `json:"logs"`
		NextCursor string                 `json:"next_cursor"`
	}
	runtimeBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, logsURL, nil, http.StatusOK, readHeaders)
	if err := json.Unmarshal(runtimeBody, &runtimeLogsPage); err != nil {
		t.Fatal(err)
	}
	if len(runtimeLogsPage.Logs) != 1 || runtimeLogsPage.Logs[0].Message != "runtime-output-marker" || runtimeLogsPage.NextCursor == "" {
		t.Fatalf("runtime log API response = %+v", runtimeLogsPage)
	}
	if strings.Contains(string(runtimeBody), containerID) || strings.Contains(string(runtimeBody), "event_id") {
		t.Fatalf("runtime log API exposed internal container or event identity: %s", runtimeBody)
	}
	if len(runtimeLogs.query.ContainerIDs) != 1 || runtimeLogs.query.ContainerIDs[0] != containerID || runtimeLogs.query.Limit != 100 {
		t.Fatalf("runtime query did not use only trusted PG mapping/default bound: %+v", runtimeLogs.query)
	}
	requestJSONRaw(t, viewerClient, http.MethodGet, logsURL, nil, http.StatusOK)
	resumeURL := projectURL + "/apps/" + created.App.ID + "/logs?cursor=" + runtimeLogsPage.NextCursor
	requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, resumeURL, nil, http.StatusOK, readHeaders)
	if runtimeLogs.query.After == nil || runtimeLogs.query.After.EventID != "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4d3c" {
		t.Fatalf("runtime cursor was not resumed by the server: %+v", runtimeLogs.query.After)
	}

	// A cursor older than the initial one-hour window must anchor the resumed
	// query range instead of inheriting the moving no-cursor default.
	oldCursorAt := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	oldCursor := telemetry.EncodeLogCursor(telemetry.LogCursor{
		Timestamp: oldCursorAt,
		EventID:   "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4d3d",
	})
	oldCursorURL := projectURL + "/apps/" + created.App.ID + "/logs?cursor=" + url.QueryEscape(oldCursor)
	requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, oldCursorURL, nil, http.StatusOK, readHeaders)
	if runtimeLogs.query.Range.From.IsZero() || !runtimeLogs.query.Range.From.Equal(oldCursorAt) || runtimeLogs.query.After == nil || !runtimeLogs.query.After.Timestamp.Equal(oldCursorAt) {
		t.Fatalf("old cursor did not anchor query range at its timestamp: range=%+v cursor=%+v", runtimeLogs.query.Range, runtimeLogs.query.After)
	}

	assertRuntimeCursorRejectedWithoutQuery := func(name, cursor string, from *time.Time) []byte {
		t.Helper()
		values := url.Values{}
		values.Set("cursor", cursor)
		if from != nil {
			values.Set("from", from.UTC().Format(time.RFC3339Nano))
		}
		before := runtimeLogs.calls
		body := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet,
			projectURL+"/apps/"+created.App.ID+"/logs?"+values.Encode(), nil, http.StatusBadRequest, readHeaders)
		if runtimeLogs.calls != before {
			t.Fatalf("%s cursor validation executed a telemetry query", name)
		}
		if !strings.Contains(string(body), `"code":"validation_error"`) {
			t.Fatalf("%s cursor did not return a validation error: %s", name, body)
		}
		return body
	}
	assertRuntimeCursorRejectedWithoutQuery("malformed", "tampered-cursor", nil)

	futureCursor := telemetry.EncodeLogCursor(telemetry.LogCursor{
		Timestamp: time.Now().UTC().Add(time.Minute),
		EventID:   "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4d3e",
	})
	assertRuntimeCursorRejectedWithoutQuery("future", futureCursor, nil)

	expiredCursor := telemetry.EncodeLogCursor(telemetry.LogCursor{
		Timestamp: time.Now().UTC().Add(-3 * time.Hour),
		EventID:   "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4d3f",
	})
	expiredBody := assertRuntimeCursorRejectedWithoutQuery("expired", expiredCursor, nil)
	if !strings.Contains(string(expiredBody), "runtime log cursor is outside the allowed query window") {
		t.Fatalf("expired cursor did not explain the bounded query window: %s", expiredBody)
	}

	contradictoryCursorAt := time.Now().UTC().Add(-30 * time.Minute)
	contradictoryCursor := telemetry.EncodeLogCursor(telemetry.LogCursor{
		Timestamp: contradictoryCursorAt,
		EventID:   "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4e",
	})
	fromAfterCursor := contradictoryCursorAt.Add(time.Minute)
	assertRuntimeCursorRejectedWithoutQuery("from after cursor", contradictoryCursor, &fromAfterCursor)

	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps/"+created.App.ID+"/logs", nil, http.StatusForbidden, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps/"+created.App.ID+"/logs", nil, http.StatusUnauthorized, wrongProjectHeaders)
	requestJSON(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps/"+created.App.ID+"/logs", nil, http.StatusUnauthorized, nil)
	requestJSON(t, ownerClient, http.MethodGet, secondProjectURL+"/apps/"+created.App.ID+"/logs", nil, http.StatusNotFound, nil)
	runtimeLogs.err = fmt.Errorf("clickhouse password=must-not-leak")
	unavailableBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/apps/"+created.App.ID+"/logs", nil, http.StatusServiceUnavailable, readHeaders)
	var unavailableResponse struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(unavailableBody, &unavailableResponse); err != nil {
		t.Fatalf("telemetry failure response JSON: %v", err)
	}
	if strings.Contains(string(unavailableBody), "must-not-leak") || unavailableResponse.Error.Code != "telemetry_unavailable" || unavailableResponse.Error.Message != "telemetry backend is unavailable" {
		t.Fatalf("telemetry failure response was not generic: %s", unavailableBody)
	}

	deploymentURL := projectURL + "/apps/" + writeKeyApp.App.ID + "/deployments"
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, deploymentURL, nil, http.StatusForbidden, writeHeaders)
	queuedBody := uploadFunctionMultipart(t, newIntegrationClient(t), deploymentURL, "source.tgz", gitArchiveBytes(t), writeHeaders, http.StatusAccepted)
	var queued struct {
		Deployment domain.AppDeployment `json:"deployment"`
	}
	if err := json.Unmarshal(queuedBody, &queued); err != nil {
		t.Fatal(err)
	}
	if queued.Deployment.Status != "queued" || queued.Deployment.BuildStatus != "queued" || queued.Deployment.SourceChecksumSHA256 == "" || queued.Deployment.ImageDigest != nil || queued.Deployment.Selected {
		t.Fatalf("deployment upload did not return truthful queued state: %+v", queued.Deployment)
	}
	encodedDeployment, err := json.Marshal(queued.Deployment)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedDeployment), "source_path") || strings.Contains(string(encodedDeployment), "image_path") || strings.Contains(string(encodedDeployment), "build_worker_id") {
		t.Fatalf("deployment API exposed private artifact or lease data: %s", encodedDeployment)
	}
	deploymentID := queued.Deployment.ID
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, deploymentURL, nil, http.StatusOK, readHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, deploymentURL+"/"+deploymentID, nil, http.StatusOK, readHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, deploymentURL+"/"+deploymentID+"/logs", nil, http.StatusOK, readHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, deploymentURL, nil, http.StatusUnauthorized, wrongProjectHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, deploymentURL+"/"+deploymentID+"/select", nil, http.StatusConflict, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodDelete, deploymentURL+"/"+deploymentID, nil, http.StatusNoContent, writeHeaders)

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
