package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProjectCORSPolicyIntegration(t *testing.T) {
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
		"email":    fmt.Sprintf("cors-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "cors-" + ownerID.String()[:8]}, http.StatusCreated, &project)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, registration.Account.ID, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	projectURL := server.URL + "/v1/projects/" + project.Project.ID
	var initialSettings struct {
		Settings struct {
			CORSOrigins []string `json:"cors_origins"`
		} `json:"settings"`
	}
	initialSettingsBody := requestJSONRaw(t, ownerClient, http.MethodGet, projectURL+"/auth/settings", nil, http.StatusOK)
	if err := json.Unmarshal(initialSettingsBody, &initialSettings); err != nil {
		t.Fatal(err)
	}
	if initialSettings.Settings.CORSOrigins == nil {
		t.Fatal("initial cors_origins must be an empty JSON array, not null")
	}
	requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/auth/settings", map[string]any{"cors_origins": []string{"https://APP.Example.com", "http://localhost:3000"}}, http.StatusOK, nil)

	preflight, err := http.NewRequest(http.MethodOptions, projectURL+"/account/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	preflight.Header.Set("Origin", "https://app.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type")
	preflightResponse, err := ownerClient.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	preflightResponse.Body.Close()
	if preflightResponse.StatusCode != http.StatusNoContent || preflightResponse.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" || preflightResponse.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("unexpected preflight response: status=%d headers=%v", preflightResponse.StatusCode, preflightResponse.Header)
	}

	allowed, err := http.NewRequest(http.MethodGet, projectURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed.Header.Set("Origin", "https://app.example.com")
	allowedResponse, err := ownerClient.Do(allowed)
	if err != nil {
		t.Fatal(err)
	}
	allowedResponse.Body.Close()
	if allowedResponse.StatusCode != http.StatusOK || allowedResponse.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("allowed origin response: status=%d headers=%v", allowedResponse.StatusCode, allowedResponse.Header)
	}

	denied, err := http.NewRequest(http.MethodGet, projectURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	denied.Header.Set("Origin", "https://evil.example.com")
	deniedResponse, err := ownerClient.Do(denied)
	if err != nil {
		t.Fatal(err)
	}
	deniedResponse.Body.Close()
	if deniedResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("denied origin response status=%d", deniedResponse.StatusCode)
	}
}
