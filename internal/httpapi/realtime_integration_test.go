package httpapi_test

import (
	"bufio"
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

func TestProjectRealtimeSSEIntegration(t *testing.T) {
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
	cipher, err := functionsecret.New(bytes.Repeat([]byte("r"), functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour, AppSessionTTL: time.Hour}, repository.NewWithDependencies(pool, repository.Dependencies{WebhookCipher: cipher}), logger))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	registration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("realtime-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})
	project := struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "realtime-" + ownerID.String()[:8]}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID

	// The project event is retained even though no webhook is configured. It
	// can therefore be replayed by an SSE client from the event outbox.
	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE project_id=$1 AND event_name='project.create'`, project.Project.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("project.create event count = %d, want 1", eventCount)
	}

	streamContext, streamCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer streamCancel()
	request, err := http.NewRequestWithContext(streamContext, http.MethodGet, projectURL+"/realtime?events=project.create", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := ownerClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE response status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	foundEvent := false
	for i := 0; i < 32; i++ {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.TrimSpace(line) == "event: project.create" {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Fatal("SSE stream did not replay project.create")
	}

	// API-key consumers need the dedicated read scope and cannot use an
	// unrelated database scope as a substitute.
	key := struct {
		Secret string `json:"secret"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "realtime-reader", "scopes": []string{"realtime.read"}}, http.StatusCreated, &key)
	keyContext, keyCancel := context.WithTimeout(context.Background(), 2*time.Second)
	keyRequest, err := http.NewRequestWithContext(keyContext, http.MethodGet, projectURL+"/realtime?events=project.create", nil)
	if err != nil {
		t.Fatal(err)
	}
	keyRequest.Header.Set("X-Stealth-Key", key.Secret)
	keyResponse, err := (&http.Client{}).Do(keyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if keyResponse.StatusCode != http.StatusOK {
		t.Fatalf("API-key SSE status = %d", keyResponse.StatusCode)
	}
	_ = keyResponse.Body.Close()
	keyCancel()
	limitedKey := struct {
		Secret string `json:"secret"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "database-only", "scopes": []string{"databases.read"}}, http.StatusCreated, &limitedKey)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/realtime", nil, http.StatusForbidden, map[string]string{"X-Stealth-Key": limitedKey.Secret})
}
