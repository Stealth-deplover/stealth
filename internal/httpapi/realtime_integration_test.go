package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
	// remains available to the durable outbox and is included in the metadata
	// assertions below, but an initial SSE connection must not replay it.
	var eventCount int
	var eventVersion int
	var organizationID string
	var publishStatus string
	if err := pool.QueryRow(ctx, `SELECT count(*),max(event_version),min(organization_id::text),min(publish_status) FROM webhook_events WHERE project_id=$1 AND event_name='project.create'`, project.Project.ID).Scan(&eventCount, &eventVersion, &organizationID, &publishStatus); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("project.create event count = %d, want 1", eventCount)
	}
	if eventVersion != 1 || organizationID != registration.Organization.ID || publishStatus != "pending" {
		t.Fatalf("outbox metadata version=%d organization=%q status=%q", eventVersion, organizationID, publishStatus)
	}
	realtimeRepo := repository.New(pool)
	targetProjectID := uuid.MustParse(project.Project.ID)
	claimed, err := realtimeRepo.ClaimNextRealtimeEventForProject(ctx, targetProjectID, "integration-publisher-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ProjectID.String() != project.Project.ID || claimed.AttemptCount != 1 {
		t.Fatalf("claimed realtime event = %#v", claimed)
	}
	if _, err := realtimeRepo.ClaimNextRealtimeEventForProject(ctx, targetProjectID, "integration-publisher-b", time.Minute); !errors.Is(err, repository.ErrNoRealtimeEvent) {
		t.Fatalf("second publisher claim error = %v, want ErrNoRealtimeEvent", err)
	}
	retryAt := time.Now().UTC().Add(time.Hour)
	if err := realtimeRepo.FinishRealtimeEvent(ctx, claimed.EventID, "integration-publisher-a", false, &retryAt, "temporary fanout outage"); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT publish_status,publish_attempts FROM webhook_events WHERE id=$1`, claimed.EventID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 1 {
		t.Fatalf("retried outbox event status=%q attempts=%d", status, attempts)
	}

	// Create two retained events before connecting. The initial connection
	// starts at the current tail, so neither event should be emitted.
	createProjectKey := func(name string) {
		requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": name, "scopes": []string{"realtime.read"}}, http.StatusCreated, &struct {
			Secret string `json:"secret"`
		}{})
	}
	latestEventID := func(eventName string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM webhook_events WHERE project_id=$1 AND event_name=$2 ORDER BY id DESC LIMIT 1`, targetProjectID, eventName).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	createProjectKey("realtime-old-a")
	oldA := latestEventID("project_api_key.create")
	createProjectKey("realtime-old-b")
	oldB := latestEventID("project_api_key.create")

	streamContext, streamCancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer streamCancel()
	request, err := http.NewRequestWithContext(streamContext, http.MethodGet, projectURL+"/realtime?events=project_api_key.create", nil)
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
	line, readErr := reader.ReadString('\n')
	if readErr != nil || strings.TrimSpace(line) != "retry: 3000" {
		t.Fatalf("SSE prelude = %q, err = %v", line, readErr)
	}
	if line, readErr = reader.ReadString('\n'); readErr != nil || strings.TrimSpace(line) != "" {
		t.Fatalf("SSE prelude terminator = %q, err = %v", line, readErr)
	}

	// This mutation occurs after the tail lookup. PostgreSQL reconciliation must
	// deliver it even though this integration server has no Redis broker.
	createProjectKey("realtime-new")
	newEvent := latestEventID("project_api_key.create")
	receivedID, receivedType := readRealtimeSSEEvent(t, reader)
	if receivedID != newEvent.String() || receivedType != "project_api_key.create" {
		t.Fatalf("initial SSE event = id %q type %q, want id %q and project_api_key.create; old ids were %s and %s", receivedID, receivedType, newEvent, oldA, oldB)
	}
	if receivedID == oldA.String() || receivedID == oldB.String() {
		t.Fatal("initial SSE connection replayed a retained historical event")
	}
	streamCancel()
	_ = response.Body.Close()

	// A retained cursor is a resume boundary. Events created after it are
	// replayed in order, while the event at the cursor is not repeated.
	createProjectKey("realtime-resume-b")
	resumeB := latestEventID("project_api_key.create")
	createProjectKey("realtime-resume-c")
	resumeC := latestEventID("project_api_key.create")
	reconnectContext, reconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer reconnectCancel()
	reconnectRequest, err := http.NewRequestWithContext(reconnectContext, http.MethodGet, projectURL+"/realtime?events=project_api_key.create", nil)
	if err != nil {
		t.Fatal(err)
	}
	reconnectRequest.Header.Set("Last-Event-ID", newEvent.String())
	reconnectResponse, err := ownerClient.Do(reconnectRequest)
	if err != nil {
		t.Fatal(err)
	}
	if reconnectResponse.StatusCode != http.StatusOK {
		_ = reconnectResponse.Body.Close()
		t.Fatalf("reconnect SSE status = %d", reconnectResponse.StatusCode)
	}
	reconnectReader := bufio.NewReader(reconnectResponse.Body)
	resumeID, resumeType := readRealtimeSSEEvent(t, reconnectReader)
	if resumeID != resumeB.String() || resumeType != "project_api_key.create" {
		t.Fatalf("first reconnect event = id %q type %q, want %s", resumeID, resumeType, resumeB)
	}
	resumeID, resumeType = readRealtimeSSEEvent(t, reconnectReader)
	if resumeID != resumeC.String() || resumeType != "project_api_key.create" {
		t.Fatalf("second reconnect event = id %q type %q, want %s", resumeID, resumeType, resumeC)
	}
	_ = reconnectResponse.Body.Close()

	// An expired cursor cannot be replayed. It resets to the current retained
	// tail, allowing the Console's canonical refetch to recover state.
	expiredCursor := uuid.Must(uuid.NewV7())
	createdAt := time.Now().UTC().Add(-48 * time.Hour)
	expiresAt := createdAt.Add(time.Minute)
	if _, err := pool.Exec(ctx, `INSERT INTO webhook_events (id,project_id,organization_id,event_name,target_type,payload,created_at,expires_at) VALUES ($1,$2,$3,'realtime.test.expired','realtime_test','{}'::jsonb,$4,$5)`, expiredCursor, targetProjectID, registration.Organization.ID, createdAt, expiresAt); err != nil {
		t.Fatal(err)
	}
	startCursor, err := realtimeRepo.ResolveRealtimeStartCursor(ctx, targetProjectID, repository.DatabaseActor{Kind: repository.DatabaseConsoleActor, AccountID: uuid.MustParse(registration.Account.ID)}, &expiredCursor)
	if err != nil || startCursor == nil || *startCursor == expiredCursor {
		t.Fatalf("expired realtime cursor = %v, err = %v; expected reset to retained tail", startCursor, err)
	}

	// Pruning is bounded and must retain live rows. The expiry index and the
	// repository batch limit keep this operation short even as the table grows.
	expiredA := uuid.Must(uuid.NewV7())
	expiredB := uuid.Must(uuid.NewV7())
	oldCreatedAt := time.Now().UTC().Add(-72 * time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO webhook_events (id,project_id,organization_id,event_name,target_type,payload,created_at,expires_at) VALUES ($1,$2,$3,'realtime.test.expired','realtime_test','{}'::jsonb,$4,$5),($6,$2,$3,'realtime.test.expired','realtime_test','{}'::jsonb,$4,$7)`, expiredA, targetProjectID, registration.Organization.ID, oldCreatedAt, oldCreatedAt.Add(time.Minute), expiredB, oldCreatedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	nonExpired := uuid.Must(uuid.NewV7())
	activeCreatedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `INSERT INTO webhook_events (id,project_id,organization_id,event_name,target_type,payload,created_at,expires_at) VALUES ($1,$2,$3,'realtime.test.active','realtime_test','{}'::jsonb,$4,$5)`, nonExpired, targetProjectID, registration.Organization.ID, activeCreatedAt, activeCreatedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	deleted, err := realtimeRepo.PruneExpiredWebhookEventsBatch(ctx, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("bounded realtime prune deleted=%d err=%v, want one row", deleted, err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_events WHERE id=$1)`, expiredA).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("bounded realtime prune did not delete the oldest expired event")
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_events WHERE id=$1)`, expiredB).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("bounded realtime prune deleted more than one expired event")
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_events WHERE id=$1)`, nonExpired).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("realtime prune deleted a non-expired event")
	}
	if deleted, err := realtimeRepo.PruneExpiredWebhookEventsBatch(ctx, 1); err != nil || deleted != 1 {
		t.Fatalf("second bounded realtime prune deleted=%d err=%v, want remaining expired row", deleted, err)
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

	// A valid Console session from another organization must not turn a
	// project identifier into a cross-tenant subscription capability.
	otherClient := newIntegrationClient(t)
	otherOwnerID := uuid.Must(uuid.NewV7())
	otherRegistration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{}
	requestJSON(t, otherClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("realtime-other-%s@example.test", otherOwnerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &otherRegistration)
	otherProject := struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}{}
	requestJSON(t, otherClient, http.MethodPost, server.URL+"/v1/organizations/"+otherRegistration.Organization.ID+"/projects", map[string]string{"name": "other-realtime-" + otherOwnerID.String()[:8]}, http.StatusCreated, &otherProject)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, otherRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, otherRegistration.Account.ID)
	})
	requestJSONWithHeaders(t, ownerClient, http.MethodGet, server.URL+"/v1/projects/"+otherProject.Project.ID+"/realtime", nil, http.StatusNotFound, nil)
}

func readRealtimeSSEEvent(t *testing.T, reader *bufio.Reader) (string, string) {
	t.Helper()
	var id, event string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case line == "":
			if id != "" && event != "" {
				return id, event
			}
			id, event = "", ""
		}
	}
}
