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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/ratelimit"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestAgentRunRateLimitUsesProjectScopeIntegration(t *testing.T) {
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName:         "stealth_session",
		SessionTTL:                time.Hour,
		ProjectOperationRateLimit: 1,
		StorageRoot:               t.TempDir(),
		AgentProviderCatalog: []config.AgentProviderCatalogItem{
			{ID: "local", Name: "Local gateway", Models: []string{"model-a"}},
		},
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewMemoryLimiter()}))
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
		"email": fmt.Sprintf("agent-rate-limit-%s@example.test", ownerID), "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, registration.Organization.ID, registration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	createProject := func(name string) string {
		t.Helper()
		var response struct {
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
		}
		requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": name}, http.StatusCreated, &response)
		return response.Project.ID
	}
	projectOne := createProject("agent-rate-limit-one-" + ownerID.String()[:8])
	projectTwo := createProject("agent-rate-limit-two-" + ownerID.String()[:8])

	createAgent := func(projectID, name string) string {
		t.Helper()
		var response struct {
			Agent struct {
				ID string `json:"id"`
			} `json:"agent"`
		}
		requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/agents", map[string]any{
			"project_id": projectID,
			"name":       name,
			"role":       "General",
			"provider":   "local",
			"model":      "model-a",
		}, http.StatusCreated, &response)
		return response.Agent.ID
	}
	agentOne := createAgent(projectOne, "Agent One")
	agentTwo := createAgent(projectOne, "Agent Two")
	agentThree := createAgent(projectTwo, "Agent Three")

	var firstRun struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/agents/"+agentOne+"/runs", map[string]string{"prompt": "first project run"}, http.StatusAccepted, &firstRun)
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/agents/"+agentTwo+"/runs", map[string]string{"prompt": "same project budget"}, http.StatusTooManyRequests, nil)
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/agents/"+agentOne+"/runs/"+firstRun.Run.ID+"/cancel", nil, http.StatusOK, nil)
	var thirdRun struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/agents/"+agentThree+"/runs", map[string]string{"prompt": "independent project budget"}, http.StatusAccepted, &thirdRun)
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/agents/"+agentThree+"/runs/"+thirdRun.Run.ID+"/cancel", nil, http.StatusOK, nil)
}
