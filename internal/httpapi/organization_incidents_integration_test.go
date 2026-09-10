package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrganizationIncidentsControlPlaneIntegration(t *testing.T) {
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
	ownerEmail := fmt.Sprintf("incident-owner-%s@example.test", uuid.Must(uuid.NewV7()))
	password := "correct-horse-battery-staple"
	var owner struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": ownerEmail, "password": password}, http.StatusCreated, &owner)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, owner.Account.ID, owner.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, owner.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, owner.Account.ID)
	})

	viewerClient := newIntegrationClient(t)
	viewerEmail := fmt.Sprintf("incident-viewer-%s@example.test", uuid.Must(uuid.NewV7()))
	var viewer struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": viewerEmail, "password": password}, http.StatusCreated, &viewer)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, viewer.Account.ID, viewer.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, viewer.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, viewer.Account.ID)
	})
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+owner.Organization.ID+"/memberships", map[string]string{"email": viewerEmail, "role": "viewer"}, http.StatusCreated, nil)

	var created struct {
		Incident struct {
			ID       string   `json:"id"`
			Title    string   `json:"title"`
			Severity string   `json:"severity"`
			Status   string   `json:"status"`
			Services []string `json:"services"`
			Updates  []struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"updates"`
		} `json:"incident"`
	}
	incidentURL := server.URL + "/v1/organizations/" + owner.Organization.ID + "/incidents"
	requestJSON(t, ownerClient, http.MethodPost, incidentURL, map[string]any{
		"title":    "Elevated sandbox failures",
		"severity": "warning",
		"services": []string{"Sandbox Service", "API"},
		"message":  "Provision failures crossed the alert threshold.",
	}, http.StatusCreated, &created)
	if created.Incident.ID == "" || created.Incident.Title != "Elevated sandbox failures" || created.Incident.Severity != "warning" || created.Incident.Status != "investigating" || len(created.Incident.Services) != 2 || len(created.Incident.Updates) != 1 {
		t.Fatalf("created incident = %+v", created.Incident)
	}

	var listed struct {
		Incidents []struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			ResolvedAt *any   `json:"resolved_at"`
		} `json:"incidents"`
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, viewerClient, http.MethodGet, incidentURL, nil, http.StatusOK, &listed)
	if listed.CanManage || len(listed.Incidents) != 1 || listed.Incidents[0].ID != created.Incident.ID || listed.Incidents[0].Status != "investigating" {
		t.Fatalf("viewer incident list = %+v", listed)
	}
	requestJSON(t, viewerClient, http.MethodPost, incidentURL, map[string]any{"title": "viewer cannot open", "severity": "info", "services": []string{"API"}}, http.StatusForbidden, nil)
	requestJSON(t, viewerClient, http.MethodPatch, incidentURL+"/"+created.Incident.ID, map[string]any{"status": "resolved"}, http.StatusForbidden, nil)

	var updated struct {
		Incident struct {
			Status     string `json:"status"`
			ResolvedAt *any   `json:"resolved_at"`
			Updates    []struct {
				Status string `json:"status"`
			} `json:"updates"`
		} `json:"incident"`
	}
	requestJSON(t, ownerClient, http.MethodPatch, incidentURL+"/"+created.Incident.ID, map[string]any{"status": "resolved", "message": "The error rate is back to baseline."}, http.StatusOK, &updated)
	if updated.Incident.Status != "resolved" || updated.Incident.ResolvedAt == nil || len(updated.Incident.Updates) != 2 || updated.Incident.Updates[1].Status != "resolved" {
		t.Fatalf("updated incident = %+v", updated.Incident)
	}
	requestJSON(t, ownerClient, http.MethodPatch, incidentURL+"/"+created.Incident.ID, map[string]any{"status": "monitoring"}, http.StatusConflict, nil)

	var events struct {
		Events []struct {
			Action   string `json:"action"`
			TargetID string `json:"target_id"`
		} `json:"events"`
	}
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/organizations/"+owner.Organization.ID+"/audit-events?limit=100", nil, http.StatusOK, &events)
	seenCreate, seenUpdate := false, false
	for _, event := range events.Events {
		if event.TargetID != created.Incident.ID {
			continue
		}
		seenCreate = seenCreate || event.Action == "organization.incident.create"
		seenUpdate = seenUpdate || event.Action == "organization.incident.update"
	}
	if !seenCreate || !seenUpdate {
		t.Fatalf("incident audit events missing: %+v", events.Events)
	}
}
