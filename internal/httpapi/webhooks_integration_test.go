package httpapi_test

import (
	"bytes"
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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/functionsecret"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestProjectWebhooksIntegration(t *testing.T) {
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
	cipher, err := functionsecret.New(bytes.Repeat([]byte("w"), functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.NewWithDependencies(pool, repository.Dependencies{WebhookCipher: cipher}), logger))
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
		"email":    fmt.Sprintf("webhook-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})
	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "webhooks-" + ownerID.String()[:8]}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID

	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/webhooks", map[string]string{"name": "bad", "url": "http://example.com"}, http.StatusUnprocessableEntity, nil)
	var created struct {
		Webhook struct {
			ID     string   `json:"id"`
			Events []string `json:"events"`
			URL    string   `json:"url"`
		} `json:"webhook"`
		Secret string `json:"secret"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/webhooks", map[string]any{
		"name": "all-events",
		"url":  "https://hooks.example.com/stealth",
	}, http.StatusCreated, &created)
	if created.Webhook.ID == "" || !strings.HasPrefix(created.Secret, "whsec_") || len(created.Webhook.Events) != 1 || created.Webhook.Events[0] != "*" {
		t.Fatalf("unexpected webhook create response: %+v", created)
	}
	listBody := requestJSONRaw(t, ownerClient, http.MethodGet, projectURL+"/webhooks", nil, http.StatusOK)
	if bytes.Contains(listBody, []byte(created.Secret)) || bytes.Contains(listBody, []byte("secret_ciphertext")) {
		t.Fatalf("webhook list leaked signing secret: %s", listBody)
	}

	var readKey struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		Secret string `json:"secret"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "webhook-read", "scopes": []string{"webhooks.read"}}, http.StatusCreated, &readKey)
	readClient := &http.Client{}
	readHeaders := map[string]string{"X-Stealth-Key": readKey.Secret}
	requestJSONWithHeaders(t, readClient, http.MethodGet, projectURL+"/webhooks", nil, http.StatusOK, readHeaders)
	requestJSONWithHeaders(t, readClient, http.MethodPost, projectURL+"/webhooks", map[string]any{"name": "denied", "url": "https://hooks.example.com/denied"}, http.StatusForbidden, readHeaders)

	// A matching project mutation is written to the transactional outbox and
	// fan-outs to the configured endpoint before the HTTP request returns.
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "delivery-trigger", "scopes": []string{"users.read"}}, http.StatusCreated, &struct{}{})
	var deliveries struct {
		Deliveries []struct {
			EventName string `json:"event_name"`
			Status    string `json:"status"`
		} `json:"deliveries"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/webhooks/"+created.Webhook.ID+"/deliveries", nil, http.StatusOK, &deliveries)
	if len(deliveries.Deliveries) == 0 || deliveries.Deliveries[0].EventName == "" || deliveries.Deliveries[0].Status != "pending" {
		t.Fatalf("transactional webhook delivery was not queued: %+v", deliveries)
	}

	var rotated struct {
		Secret string `json:"secret"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/webhooks/"+created.Webhook.ID+"/rotate-secret", map[string]any{}, http.StatusOK, &rotated)
	if rotated.Secret == "" || rotated.Secret == created.Secret || !strings.HasPrefix(rotated.Secret, "whsec_") {
		t.Fatalf("secret rotation did not return a new one-time secret: %+v", rotated)
	}
}
