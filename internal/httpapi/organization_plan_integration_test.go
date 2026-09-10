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

func TestOrganizationPlanAndResourceLimitsIntegration(t *testing.T) {
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
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	client := newIntegrationClient(t)
	identifier := uuid.Must(uuid.NewV7())
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("plan-owner-%s@example.test", identifier),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, registration.Organization.ID, registration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	organizationURL := server.URL + "/v1/organizations/" + registration.Organization.ID
	var plan struct {
		Plan struct {
			OrganizationID string `json:"organization_id"`
			PlanKey        string `json:"plan_key"`
			Status         string `json:"status"`
			Limits         struct {
				Projects  int64 `json:"projects"`
				Databases int64 `json:"databases"`
			} `json:"limits"`
			Usage struct {
				Projects  int64 `json:"projects"`
				Members   int64 `json:"members"`
				Databases int64 `json:"databases"`
			} `json:"usage"`
		} `json:"plan"`
	}
	requestJSON(t, client, http.MethodGet, organizationURL+"/plan", nil, http.StatusOK, &plan)
	if plan.Plan.OrganizationID != registration.Organization.ID || plan.Plan.PlanKey != "free" || plan.Plan.Status != "active" || plan.Plan.Limits.Projects != 3 || plan.Plan.Limits.Databases != 5 || plan.Plan.Usage.Members != 1 {
		t.Fatalf("unexpected organization plan: %+v", plan.Plan)
	}

	var firstProject struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	for index := 0; index < 3; index++ {
		body := map[string]string{"name": fmt.Sprintf("plan-project-%d-%s", index, identifier.String()[:8])}
		if index == 0 {
			requestJSON(t, client, http.MethodPost, organizationURL+"/projects", body, http.StatusCreated, &firstProject)
			continue
		}
		requestJSON(t, client, http.MethodPost, organizationURL+"/projects", body, http.StatusCreated, nil)
	}
	requestJSON(t, client, http.MethodPost, organizationURL+"/projects", map[string]string{"name": "plan-project-over-limit"}, http.StatusConflict, nil)

	projectURL := server.URL + "/v1/projects/" + firstProject.Project.ID
	for index := 0; index < 5; index++ {
		requestJSON(t, client, http.MethodPost, projectURL+"/databases", map[string]string{"name": fmt.Sprintf("plan-database-%d", index)}, http.StatusCreated, nil)
	}
	requestJSON(t, client, http.MethodPost, projectURL+"/databases", map[string]string{"name": "plan-database-over-limit"}, http.StatusConflict, nil)

	requestJSON(t, client, http.MethodGet, organizationURL+"/plan", nil, http.StatusOK, &plan)
	if plan.Plan.Usage.Projects != 3 || plan.Plan.Usage.Databases != 5 {
		t.Fatalf("plan usage did not reflect writes: %+v", plan.Plan.Usage)
	}
}
