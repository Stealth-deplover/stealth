package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminRealtimeSSEIntegration(t *testing.T) {
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
	server := httptest.NewServer(httpapi.New(config.Config{
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("r"), 32),
	}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	streamClient := newIntegrationClient(t)
	streamAdmin := registerAdminTestAccount(t, streamClient, server.URL, "admin-realtime-stream")
	mutatorClient := newIntegrationClient(t)
	mutatorAdmin := registerAdminTestAccount(t, mutatorClient, server.URL, "admin-realtime-mutator")
	if _, err := pool.Exec(ctx, `
		INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_admin'),($2,'instance_admin')`, streamAdmin.accountID, mutatorAdmin.accountID); err != nil {
		t.Fatal(err)
	}
	alertID := uuid.Nil
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if alertID != uuid.Nil {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_events WHERE rule_id=$1 OR rule_id_snapshot=$1`, alertID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_rules WHERE id=$1`, alertID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_realtime_events WHERE target_id=$1`, alertID)
		}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id IN ($1,$2)`, streamAdmin.accountID, mutatorAdmin.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2)`, streamAdmin.organizationID, mutatorAdmin.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id IN ($1,$2)`, streamAdmin.accountID, mutatorAdmin.accountID)
	})

	streamContext, streamCancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer streamCancel()
	request, err := http.NewRequestWithContext(streamContext, http.MethodGet, server.URL+"/v1/admin/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := streamClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("admin SSE response status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	if line, readErr := reader.ReadString('\n'); readErr != nil || strings.TrimSpace(line) != "retry: 3000" {
		t.Fatalf("admin SSE prelude = %q, err = %v", line, readErr)
	}
	if line, readErr := reader.ReadString('\n'); readErr != nil || strings.TrimSpace(line) != "" {
		t.Fatalf("admin SSE prelude terminator = %q, err = %v", line, readErr)
	}

	var created struct {
		Rule struct {
			ID string `json:"id"`
		} `json:"rule"`
	}
	requestJSON(t, mutatorClient, http.MethodPost, server.URL+"/v1/admin/alerts", map[string]any{
		"name":        "Cross-session alert",
		"kind":        "metric_threshold",
		"condition":   map[string]any{"operator": "gte", "threshold": 1, "metric": "system.cpu.utilization"},
		"severity":    "warning",
		"for_seconds": 0,
		"enabled":     true,
	}, http.StatusCreated, &created)
	alertID, err = uuid.Parse(created.Rule.ID)
	if err != nil {
		t.Fatal(err)
	}

	eventID, eventType, eventData := readAdminRealtimeSSEEvent(t, reader)
	if eventType != "admin" || eventID == "" || !strings.Contains(eventData, `"type":"admin.alert.create"`) || !strings.Contains(eventData, alertID.String()) {
		t.Fatalf("cross-session admin event = id %q type %q data %q", eventID, eventType, eventData)
	}
	if strings.Contains(eventData, "password") || strings.Contains(eventData, "secret") || strings.Contains(eventData, "token") {
		t.Fatalf("admin realtime event contains sensitive field name: %q", eventData)
	}

	// A retained cursor resumes after the event delivered to the first session.
	streamCancel()
	_ = response.Body.Close()
	resumeContext, resumeCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer resumeCancel()
	request, err = http.NewRequestWithContext(resumeContext, http.MethodGet, server.URL+"/v1/admin/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", eventID)
	resumed, err := streamClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Body.Close()
	if resumed.StatusCode != http.StatusOK {
		t.Fatalf("admin SSE resume status=%d", resumed.StatusCode)
	}
	// Use the independent authenticated mutator session for the second
	// mutation. The stream session must learn about it through SSE only.
	requestJSON(t, mutatorClient, http.MethodPut, server.URL+"/v1/admin/alerts/"+alertID.String(), map[string]any{
		"name":        "Cross-session alert renamed",
		"kind":        "metric_threshold",
		"condition":   map[string]any{"operator": "gte", "threshold": 2, "metric": "system.cpu.utilization"},
		"severity":    "critical",
		"for_seconds": 0,
		"enabled":     true,
	}, http.StatusOK, nil)

	resumeReader := bufio.NewReader(resumed.Body)
	if _, err := resumeReader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeReader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	resumedEventID, resumedType, resumedData := readAdminRealtimeSSEEvent(t, resumeReader)
	if resumedEventID == eventID || resumedType != "admin" || !strings.Contains(resumedData, `"type":"admin.alert.update"`) {
		t.Fatalf("admin SSE resumed event = id %q type %q data %q", resumedEventID, resumedType, resumedData)
	}
	var alerts struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	requestJSON(t, streamClient, http.MethodGet, server.URL+"/v1/admin/alerts?limit=100", nil, http.StatusOK, &alerts)
	foundRenamed := false
	for _, item := range alerts.Items {
		if item.ID == alertID.String() && item.Name == "Cross-session alert renamed" {
			foundRenamed = true
			break
		}
	}
	if !foundRenamed {
		t.Fatalf("canonical Admin refetch did not observe cross-session update: %#v", alerts.Items)
	}
}

func readAdminRealtimeSSEEvent(t *testing.T, reader *bufio.Reader) (string, string, string) {
	t.Helper()
	var id, event, data string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read admin SSE event: %v", err)
		}
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		case line == "" && id != "" && event != "":
			return id, event, data
		}
	}
}
