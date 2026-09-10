package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProjectUsageIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil))))
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
		"email":    fmt.Sprintf("usage-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "usage-" + ownerID.String()[:8]}, http.StatusCreated, &project)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, registration.Account.ID, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	projectURL := server.URL + "/v1/projects/" + project.Project.ID
	var initial struct {
		Usage struct {
			ProjectID        string    `json:"project_id"`
			ApplicationUsers int64     `json:"application_users"`
			APIRequests30D   int64     `json:"api_request_count_30d"`
			CapturedAt       time.Time `json:"captured_at"`
		} `json:"usage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/usage", nil, http.StatusOK, &initial)
	if initial.Usage.ProjectID != project.Project.ID || initial.Usage.ApplicationUsers != 0 || initial.Usage.CapturedAt.IsZero() {
		t.Fatalf("unexpected initial usage: %+v", initial.Usage)
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/users", map[string]string{"email": "usage-user@example.test", "password": "correct-horse-battery-staple"}, http.StatusCreated, nil)
	var after struct {
		Usage struct {
			ApplicationUsers int64 `json:"application_users"`
			APIRequests30D   int64 `json:"api_request_count_30d"`
		} `json:"usage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/usage", nil, http.StatusOK, &after)
	if after.Usage.ApplicationUsers != 1 {
		t.Fatalf("application user count = %d, want 1", after.Usage.ApplicationUsers)
	}
	if after.Usage.APIRequests30D < 1 {
		t.Fatalf("API request count = %d, want at least one recorded project request", after.Usage.APIRequests30D)
	}

	usageDate := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_usage_daily (project_id,usage_date,api_request_count,api_egress_bytes,function_invocation_count,function_failure_count,function_compute_ms)
		VALUES ($1,$2::date,7,700,2,1,50)
		ON CONFLICT (project_id,usage_date) DO UPDATE SET
			api_request_count=EXCLUDED.api_request_count,
			api_egress_bytes=EXCLUDED.api_egress_bytes,
			function_invocation_count=EXCLUDED.function_invocation_count,
			function_failure_count=EXCLUDED.function_failure_count,
			function_compute_ms=EXCLUDED.function_compute_ms`, project.Project.ID, usageDate); err != nil {
		t.Fatal(err)
	}
	var metering struct {
		Metering struct {
			ProjectID string `json:"project_id"`
			From      string `json:"from"`
			To        string `json:"to"`
			Days      []struct {
				Date                    string `json:"date"`
				APIRequestCount         int64  `json:"api_request_count"`
				APIEgressBytes          int64  `json:"api_egress_bytes"`
				FunctionInvocationCount int64  `json:"function_invocation_count"`
				FunctionFailureCount    int64  `json:"function_failure_count"`
				FunctionComputeMS       int64  `json:"function_compute_ms"`
			} `json:"days"`
			Totals struct {
				APIRequestCount         int64 `json:"api_request_count"`
				APIEgressBytes          int64 `json:"api_egress_bytes"`
				FunctionInvocationCount int64 `json:"function_invocation_count"`
				FunctionFailureCount    int64 `json:"function_failure_count"`
				FunctionComputeMS       int64 `json:"function_compute_ms"`
			} `json:"totals"`
		} `json:"metering"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/usage/metering?from="+usageDate+"&to="+usageDate, nil, http.StatusOK, &metering)
	if metering.Metering.ProjectID != project.Project.ID || metering.Metering.From != usageDate || metering.Metering.To != usageDate || len(metering.Metering.Days) != 1 {
		t.Fatalf("unexpected metering window: %+v", metering.Metering)
	}
	day := metering.Metering.Days[0]
	if day.Date != usageDate || day.APIRequestCount != 7 || day.APIEgressBytes != 700 || day.FunctionInvocationCount != 2 || day.FunctionFailureCount != 1 || day.FunctionComputeMS != 50 {
		t.Fatalf("unexpected metering day: %+v", day)
	}
	if metering.Metering.Totals.APIRequestCount != 7 || metering.Metering.Totals.FunctionComputeMS != 50 {
		t.Fatalf("unexpected metering totals: %+v", metering.Metering.Totals)
	}
	csvResponse, err := ownerClient.Get(projectURL + "/usage/metering?from=" + usageDate + "&to=" + usageDate + "&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	csvBody, readErr := io.ReadAll(csvResponse.Body)
	_ = csvResponse.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if csvResponse.StatusCode != http.StatusOK || !strings.HasPrefix(csvResponse.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("CSV response status/content type = %d/%q", csvResponse.StatusCode, csvResponse.Header.Get("Content-Type"))
	}
	if !strings.Contains(csvResponse.Header.Get("Content-Disposition"), "stealth-usage-"+project.Project.ID) || !strings.Contains(string(csvBody), "TOTAL,7,700,2,1,50") {
		t.Fatalf("unexpected CSV export: disposition=%q body=%q", csvResponse.Header.Get("Content-Disposition"), string(csvBody))
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/usage/metering?from=not-a-date", nil, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/usage/metering?from=2020-01-01&to=2022-01-01", nil, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/usage/metering?format=xml", nil, http.StatusUnprocessableEntity, nil)
}
