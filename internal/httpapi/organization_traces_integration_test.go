package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/migrate"
)

func TestOrganizationTracesIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
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
	server := httptestNewAuthServer(t, pool, &recordingMailer{})
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerEmail := fmt.Sprintf("trace-owner-%s@example.test", uuid.Must(uuid.NewV7()))
	var owner struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email": ownerEmail, "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &owner)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, owner.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, owner.Account.ID)
	})

	projectsURL := server.URL + "/v1/organizations/" + owner.Organization.ID + "/projects"
	request, err := http.NewRequest(http.MethodGet, projectsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := ownerClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list projects status = %d, body=%s", response.StatusCode, body)
	}
	traceID := response.Header.Get("X-Trace-ID")
	if len(traceID) != 32 {
		t.Fatalf("X-Trace-ID = %q, want 32 hexadecimal characters", traceID)
	}

	var listed struct {
		Traces []struct {
			ID             string `json:"id"`
			TraceID        string `json:"trace_id"`
			OrganizationID string `json:"organization_id"`
			Route          string `json:"route"`
			Method         string `json:"method"`
			Status         int    `json:"status"`
			DurationMS     int64  `json:"duration_ms"`
		} `json:"traces"`
		Pagination struct {
			Limit      int     `json:"limit"`
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
	}
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/organizations/"+owner.Organization.ID+"/traces?limit=100", nil, http.StatusOK, &listed)
	var found bool
	for _, trace := range listed.Traces {
		if trace.TraceID != traceID {
			continue
		}
		found = true
		if trace.ID == "" || trace.OrganizationID != owner.Organization.ID || trace.Route != "/v1/organizations/{organizationID}/projects" || trace.Method != http.MethodGet || trace.Status != http.StatusOK || trace.DurationMS < 0 {
			t.Fatalf("trace projection = %+v", trace)
		}
	}
	if !found {
		t.Fatalf("trace %q was not returned: %+v", traceID, listed.Traces)
	}
	if listed.Pagination.Limit != 100 {
		t.Fatalf("trace pagination = %+v", listed.Pagination)
	}

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectsURL, map[string]string{"name": "trace-project"}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID
	projectResponse, err := ownerClient.Get(projectURL)
	if err != nil {
		t.Fatal(err)
	}
	projectResponse.Body.Close()
	projectTraceID := projectResponse.Header.Get("X-Trace-ID")
	if projectResponse.StatusCode != http.StatusOK || len(projectTraceID) != 32 {
		t.Fatalf("project trace request status=%d trace=%q", projectResponse.StatusCode, projectTraceID)
	}
	var projectTraces struct {
		Traces []struct {
			ID        string  `json:"id"`
			TraceID   string  `json:"trace_id"`
			ProjectID *string `json:"project_id"`
			Route     string  `json:"route"`
			Method    string  `json:"method"`
		} `json:"traces"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/traces?limit=100", nil, http.StatusOK, &projectTraces)
	var projectTraceFound bool
	for _, trace := range projectTraces.Traces {
		if trace.TraceID != projectTraceID {
			continue
		}
		projectTraceFound = true
		if trace.ID == "" || trace.ProjectID == nil || *trace.ProjectID != project.Project.ID || trace.Route != "/v1/projects/{projectID}" || trace.Method != http.MethodGet {
			t.Fatalf("project trace projection = %+v", trace)
		}
	}
	if !projectTraceFound {
		t.Fatalf("project trace %q was not returned: %+v", projectTraceID, projectTraces.Traces)
	}

	outsiderClient := newIntegrationClient(t)
	outsiderEmail := fmt.Sprintf("trace-outsider-%s@example.test", uuid.Must(uuid.NewV7()))
	var outsider struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, outsiderClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email": outsiderEmail, "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &outsider)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, outsider.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, outsider.Account.ID)
	})
	requestJSON(t, outsiderClient, http.MethodGet, server.URL+"/v1/organizations/"+owner.Organization.ID+"/traces", nil, http.StatusForbidden, nil)
	requestJSON(t, outsiderClient, http.MethodGet, projectURL+"/traces", nil, http.StatusForbidden, nil)
}
