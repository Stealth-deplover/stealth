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
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProjectServiceLayoutIntegration(t *testing.T) {
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
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
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
		"email":    fmt.Sprintf("layout-owner-%s@example.test", ownerID),
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
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "layout-" + ownerID.String()[:8]}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID

	var initial struct {
		Layout    []any `json:"layout"`
		CanManage bool  `json:"can_manage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/service-layout", nil, http.StatusOK, &initial)
	if len(initial.Layout) != 0 || !initial.CanManage {
		t.Fatalf("initial layout = %+v", initial)
	}

	var function struct {
		Function struct {
			ID string `json:"id"`
		} `json:"function"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/functions", map[string]any{"name": "layout-worker"}, http.StatusCreated, &function)
	functionID := function.Function.ID
	var saved struct {
		Layout []struct {
			ResourceType string `json:"resource_type"`
			ResourceID   string `json:"resource_id"`
			X            int    `json:"x"`
			Y            int    `json:"y"`
		} `json:"layout"`
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, ownerClient, http.MethodPut, projectURL+"/service-layout", map[string]any{
		"layout": []map[string]any{{"resource_type": "function", "resource_id": functionID, "x": 320, "y": 144}},
	}, http.StatusOK, &saved)
	if len(saved.Layout) != 1 || saved.Layout[0].ResourceType != "function" || saved.Layout[0].ResourceID != functionID || saved.Layout[0].X != 320 || saved.Layout[0].Y != 144 || !saved.CanManage {
		t.Fatalf("saved layout = %+v", saved)
	}

	var listed struct {
		Layout []struct {
			ResourceID string `json:"resource_id"`
			X          int    `json:"x"`
			Y          int    `json:"y"`
		} `json:"layout"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/service-layout", nil, http.StatusOK, &listed)
	if len(listed.Layout) != 1 || listed.Layout[0].ResourceID != functionID || listed.Layout[0].X != 320 || listed.Layout[0].Y != 144 {
		t.Fatalf("listed layout = %+v", listed.Layout)
	}

	requestJSON(t, ownerClient, http.MethodPut, projectURL+"/service-layout", map[string]any{
		"layout": []map[string]any{{"resource_type": "function", "resource_id": uuid.Must(uuid.NewV7()).String(), "x": 0, "y": 0}},
	}, http.StatusNotFound, nil)
	requestJSON(t, ownerClient, http.MethodPut, projectURL+"/service-layout", map[string]any{
		"layout": []map[string]any{{"resource_type": "unknown", "resource_id": functionID, "x": 0, "y": 0}},
	}, http.StatusUnprocessableEntity, nil)
}
