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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/agentrunner"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestProjectAgentsControlPlaneIntegration(t *testing.T) {
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
	server := httpapi.New(config.Config{
		SessionCookieName: "stealth_session",
		SessionTTL:        time.Hour,
		AgentProviderCatalog: []config.AgentProviderCatalogItem{
			{ID: "local", Name: "Local gateway", Models: []string{"model-a", "model-b"}},
			{ID: "remote", Name: "Remote gateway", Models: []string{"model-x"}},
		},
	}, repository.New(pool), logger)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

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
	requestJSON(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/account/registrations", map[string]string{
		"email": fmt.Sprintf("agent-owner-%s@example.test", ownerID), "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &ownerRegistration)

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{
		"name": "agents-" + ownerID.String()[:8],
	}, http.StatusCreated, &project)
	var catalog struct {
		Providers []struct {
			ID     string   `json:"id"`
			Models []string `json:"models"`
		} `json:"providers"`
		Roles     []string `json:"roles"`
		Tools     []string `json:"tools"`
		Execution struct {
			Mode  string `json:"mode"`
			Ready bool   `json:"ready"`
		} `json:"execution"`
	}
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agent-catalog", nil, http.StatusOK, &catalog)
	if len(catalog.Providers) < 1 || catalog.Providers[0].ID == "" || len(catalog.Providers[0].Models) < 1 || len(catalog.Roles) < 1 || len(catalog.Tools) < 1 || catalog.Execution.Mode != "queue_only" || catalog.Execution.Ready {
		t.Fatalf("unexpected Agent catalog: %+v", catalog)
	}
	if len(catalog.Providers) != 2 || catalog.Providers[0].ID != "local" || len(catalog.Providers[0].Models) != 2 || catalog.Providers[1].ID != "remote" || catalog.Providers[1].Models[0] != "model-x" {
		t.Fatalf("unexpected provider/model catalog: %+v", catalog.Providers)
	}

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
	requestJSON(t, viewerClient, http.MethodPost, httpServer.URL+"/v1/account/registrations", map[string]string{
		"email": fmt.Sprintf("agent-viewer-%s@example.test", viewerID), "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &viewerRegistration)
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'viewer')`, ownerRegistration.Organization.ID, viewerRegistration.Account.ID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE target_id IN (SELECT id FROM project_agents WHERE project_id=$1) OR actor_account_id IN ($2,$3) OR organization_id IN ($4,$5)`, project.Project.ID, ownerRegistration.Account.ID, viewerRegistration.Account.ID, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id IN ($1,$2)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id IN ($1,$2)`, ownerRegistration.Account.ID, viewerRegistration.Account.ID)
	})

	createBody := requestJSONRaw(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents", map[string]any{
		"project_id":   project.Project.ID,
		"name":         "Frontend Engineer",
		"description":  "Build and review the web console.",
		"role":         "Frontend",
		"branch":       "main",
		"provider":     "local",
		"model":        "model-a",
		"tools":        []string{"Read files", "Edit files", "Run tests"},
		"instructions": "Inspect the repository before editing.",
	}, http.StatusCreated)
	var created struct {
		Agent struct {
			ID          string   `json:"id"`
			ProjectID   string   `json:"project_id"`
			ProjectName string   `json:"project_name"`
			Name        string   `json:"name"`
			Role        string   `json:"role"`
			Status      string   `json:"status"`
			Tools       []string `json:"tools"`
		} `json:"agent"`
		CanManage bool `json:"can_manage"`
	}
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatal(err)
	}
	if created.Agent.ID == "" || created.Agent.ProjectID != project.Project.ID || created.Agent.ProjectName == "" || created.Agent.Name != "Frontend Engineer" || created.Agent.Role != "Frontend" || created.Agent.Status != "idle" || len(created.Agent.Tools) != 3 || !created.CanManage {
		t.Fatalf("unexpected agent response: %s", createBody)
	}

	invalidPairBody := requestJSONRaw(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents", map[string]any{
		"project_id": project.Project.ID, "name": "Cross-provider Agent", "role": "General", "provider": "local", "model": "model-x",
	}, http.StatusUnprocessableEntity)
	var invalidPairError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(invalidPairBody, &invalidPairError); err != nil {
		t.Fatal(err)
	}
	if invalidPairError.Error.Code != "provider_model_invalid" {
		t.Fatalf("unexpected provider/model validation error: %s", invalidPairBody)
	}
	requestJSON(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents", map[string]any{
		"project_id": project.Project.ID, "name": "Unknown-provider Agent", "role": "General", "provider": "missing", "model": "model-a",
	}, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents", map[string]any{
		"project_id": project.Project.ID, "name": "Unknown-model Agent", "role": "General", "provider": "local", "model": "missing",
	}, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodPatch, httpServer.URL+"/v1/agents/"+created.Agent.ID, map[string]any{
		"provider": "remote", "model": "model-a",
	}, http.StatusUnprocessableEntity, nil)

	var list struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
		Pagination struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agents?limit=1&project_id="+project.Project.ID, nil, http.StatusOK, &list)
	if len(list.Agents) != 1 || list.Agents[0].ID != created.Agent.ID || !list.CanManage {
		t.Fatalf("unexpected agent list: %+v", list)
	}

	var viewerAgent struct {
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, viewerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID, nil, http.StatusOK, &viewerAgent)
	if viewerAgent.CanManage {
		t.Fatal("viewer received can_manage=true for Agent detail")
	}
	runBody := requestJSONRaw(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs", map[string]any{
		"prompt": "Inspect the project and report the first safe improvement.",
	}, http.StatusAccepted)
	var createdRun struct {
		Run struct {
			ID        string `json:"id"`
			AgentID   string `json:"agent_id"`
			ProjectID string `json:"project_id"`
			Prompt    string `json:"prompt"`
			Status    string `json:"status"`
			Steps     []any  `json:"steps"`
			Changes   []any  `json:"changes"`
		} `json:"run"`
		CanManage bool `json:"can_manage"`
	}
	if err := json.Unmarshal(runBody, &createdRun); err != nil {
		t.Fatal(err)
	}
	if createdRun.Run.ID == "" || createdRun.Run.AgentID != created.Agent.ID || createdRun.Run.ProjectID != project.Project.ID || createdRun.Run.Prompt == "" || createdRun.Run.Status != "queued" || createdRun.Run.Steps == nil || createdRun.Run.Changes == nil || !createdRun.CanManage {
		t.Fatalf("unexpected run response: %s", runBody)
	}
	var ownerRuns struct {
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs", nil, http.StatusOK, &ownerRuns)
	if !ownerRuns.CanManage {
		t.Fatal("owner did not receive can_manage=true for Agent runs")
	}
	var viewerRun struct {
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, viewerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+createdRun.Run.ID, nil, http.StatusOK, &viewerRun)
	if viewerRun.CanManage {
		t.Fatal("viewer received can_manage=true for Agent run")
	}
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+createdRun.Run.ID, nil, http.StatusOK, &struct{}{})
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+createdRun.Run.ID+"/logs", nil, http.StatusOK, &struct{}{})
	requestJSON(t, viewerClient, http.MethodPost, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs", map[string]string{"prompt": "Viewer cannot enqueue"}, http.StatusForbidden, nil)
	requestJSON(t, viewerClient, http.MethodPost, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+createdRun.Run.ID+"/cancel", nil, http.StatusForbidden, nil)
	requestJSON(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+createdRun.Run.ID+"/cancel", nil, http.StatusOK, &struct{}{})
	requestJSON(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+createdRun.Run.ID+"/cancel", nil, http.StatusConflict, nil)
	workerRunBody := requestJSONRaw(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs", map[string]string{"prompt": "Exercise the worker lifecycle"}, http.StatusAccepted)
	var workerRun struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.Unmarshal(workerRunBody, &workerRun); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(pool)
	workerID := "agent-integration-worker"
	registry := agentrunner.NewRegistry()
	if err := registry.Register("local", agentrunner.AdapterFunc(func(_ context.Context, job agentrunner.Job) (repository.AgentRunResult, error) {
		if job.Run.ID != workerRun.Run.ID || job.Agent.ID != created.Agent.ID {
			t.Fatalf("adapter received unexpected job: %+v", job)
		}
		output := "Worker completed the control-plane lifecycle."
		return repository.AgentRunResult{
			Status:     "completed",
			OutputText: &output,
			Steps:      []domain.AgentRunStep{{ID: "step-1", Type: "check", Label: "Worker check", Target: "control-plane", Status: "done"}},
			Changes:    []domain.AgentRunChange{{Path: "README.md", Additions: 1, Deletions: 0, Status: "modified"}},
		}, nil
	})); err != nil {
		t.Fatal(err)
	}
	agentWorker, err := agentrunner.New(repo, workerID, registry, logger)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := agentWorker.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("Agent worker RunOnce() = processed=%v err=%v", processed, err)
	}
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+workerRun.Run.ID+"/logs", nil, http.StatusOK, &struct{}{})
	output := "Worker completed the control-plane lifecycle."
	var completedRun struct {
		Run struct {
			Status     string `json:"status"`
			OutputText string `json:"output_text"`
			Steps      []any  `json:"steps"`
			Changes    []any  `json:"changes"`
		} `json:"run"`
	}
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID+"/runs/"+workerRun.Run.ID, nil, http.StatusOK, &completedRun)
	if completedRun.Run.Status != "completed" || completedRun.Run.OutputText != output || len(completedRun.Run.Steps) != 1 || len(completedRun.Run.Changes) != 1 {
		t.Fatalf("unexpected completed run: %+v", completedRun.Run)
	}
	requestJSON(t, viewerClient, http.MethodPost, httpServer.URL+"/v1/agents", map[string]any{
		"project_id": project.Project.ID, "name": "Viewer Agent", "role": "General", "provider": "local", "model": "model-a",
	}, http.StatusForbidden, nil)
	requestJSON(t, viewerClient, http.MethodPatch, httpServer.URL+"/v1/agents/"+created.Agent.ID, map[string]string{"name": "Viewer Rename"}, http.StatusForbidden, nil)
	requestJSON(t, viewerClient, http.MethodDelete, httpServer.URL+"/v1/agents/"+created.Agent.ID, nil, http.StatusForbidden, nil)

	requestJSON(t, ownerClient, http.MethodPost, httpServer.URL+"/v1/agents", map[string]any{
		"project_id": project.Project.ID, "name": "Frontend Engineer", "role": "Frontend", "provider": "local", "model": "model-a",
	}, http.StatusConflict, nil)
	requestJSON(t, ownerClient, http.MethodPatch, httpServer.URL+"/v1/agents/"+created.Agent.ID, map[string]any{
		"name": "Frontend Platform Engineer", "current_task": "Wire the Agent control plane", "tools": []string{"Search code", "Read files"},
	}, http.StatusOK, &struct{}{})
	requestJSON(t, ownerClient, http.MethodDelete, httpServer.URL+"/v1/agents/"+created.Agent.ID, nil, http.StatusNoContent, nil)
	requestJSON(t, ownerClient, http.MethodGet, httpServer.URL+"/v1/agents/"+created.Agent.ID, nil, http.StatusNotFound, nil)
}
