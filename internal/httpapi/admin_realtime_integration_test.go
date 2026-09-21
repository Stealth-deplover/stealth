package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("r"), 32),
	}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Dependencies{
		AdminRealtimeAuthRecheckInterval: 20 * time.Millisecond,
	}))
	defer server.Close()

	streamClient := newIntegrationClient(t)
	streamAdmin := registerAdminTestAccount(t, streamClient, server.URL, "admin-realtime-stream")
	mutatorClient := newIntegrationClient(t)
	mutatorAdmin := registerAdminTestAccount(t, mutatorClient, server.URL, "admin-realtime-mutator")
	requestJSON(t, newIntegrationClient(t), http.MethodGet, server.URL+"/v1/admin/realtime", nil, http.StatusUnauthorized, nil)
	requestJSON(t, mutatorClient, http.MethodGet, server.URL+"/v1/admin/realtime", nil, http.StatusForbidden, nil)
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
	var createdEvent struct {
		Type       string `json:"type"`
		ResourceID string `json:"resource_id"`
	}
	if err := json.Unmarshal([]byte(eventData), &createdEvent); err != nil {
		t.Fatalf("decode cross-session admin event: %v; data=%q", err, eventData)
	}
	if eventType != "admin" || eventID == "" || createdEvent.Type != "admin.alert.create" || createdEvent.ResourceID != alertID.String() {
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
	var updatedEvent struct {
		Type       string `json:"type"`
		ResourceID string `json:"resource_id"`
	}
	if err := json.Unmarshal([]byte(resumedData), &updatedEvent); err != nil {
		t.Fatalf("decode resumed admin event: %v; data=%q", err, resumedData)
	}
	if resumedEventID == eventID || resumedType != "admin" || updatedEvent.Type != "admin.alert.update" || updatedEvent.ResourceID != alertID.String() {
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

func TestAdminRealtimeSSEAuthorizationLifecycleIntegration(t *testing.T) {
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
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("s"), 32),
	}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Dependencies{
		AdminRealtimeAuthRecheckInterval: 20 * time.Millisecond,
	}))
	defer server.Close()

	roleClient := newIntegrationClient(t)
	roleAdmin := registerAdminTestAccount(t, roleClient, server.URL, "admin-realtime-role-revocation")
	sessionClient := newIntegrationClient(t)
	sessionAdmin := registerAdminTestAccount(t, sessionClient, server.URL, "admin-realtime-session-revocation")
	mutatorClient := newIntegrationClient(t)
	mutatorAdmin := registerAdminTestAccount(t, mutatorClient, server.URL, "admin-realtime-auth-mutator")
	if _, err := pool.Exec(ctx, `
		INSERT INTO instance_roles (account_id,role)
		VALUES ($1,'instance_admin'),($2,'instance_admin'),($3,'instance_admin')`, roleAdmin.accountID, sessionAdmin.accountID, mutatorAdmin.accountID); err != nil {
		t.Fatal(err)
	}
	var alertIDs []uuid.UUID
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, alertID := range alertIDs {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_events WHERE rule_id=$1 OR rule_id_snapshot=$1`, alertID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_realtime_events WHERE target_id=$1`, alertID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_rules WHERE id=$1`, alertID)
		}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id IN ($1,$2,$3)`, roleAdmin.accountID, sessionAdmin.accountID, mutatorAdmin.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2,$3)`, roleAdmin.organizationID, sessionAdmin.organizationID, mutatorAdmin.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id IN ($1,$2,$3)`, roleAdmin.accountID, sessionAdmin.accountID, mutatorAdmin.accountID)
	})

	roleResponse, roleReader, roleCancel := openAdminRealtimeStream(t, roleClient, server.URL)
	defer roleCancel()
	defer roleResponse.Body.Close()
	if _, err := pool.Exec(ctx, `DELETE FROM instance_roles WHERE account_id=$1 AND role='instance_admin'`, roleAdmin.accountID); err != nil {
		t.Fatal(err)
	}
	var roleAlert struct {
		Rule struct {
			ID string `json:"id"`
		} `json:"rule"`
	}
	requestJSON(t, mutatorClient, http.MethodPost, server.URL+"/v1/admin/alerts", map[string]any{
		"name":        "Role revocation event",
		"kind":        "metric_threshold",
		"condition":   map[string]any{"operator": "gte", "threshold": 1, "metric": "system.cpu.utilization"},
		"severity":    "warning",
		"for_seconds": 0,
		"enabled":     true,
	}, http.StatusCreated, &roleAlert)
	roleAlertID, err := uuid.Parse(roleAlert.Rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	alertIDs = append(alertIDs, roleAlertID)
	assertAdminRealtimeClosedWithoutEvent(t, roleReader)

	sessionResponse, sessionReader, sessionCancel := openAdminRealtimeStream(t, sessionClient, server.URL)
	defer sessionCancel()
	defer sessionResponse.Body.Close()
	var sessionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM sessions WHERE account_id=$1 ORDER BY created_at DESC LIMIT 1`, sessionAdmin.accountID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE account_id=$1 AND id=$2`, sessionAdmin.accountID, sessionID); err != nil {
		t.Fatal(err)
	}
	var sessionAlert struct {
		Rule struct {
			ID string `json:"id"`
		} `json:"rule"`
	}
	requestJSON(t, mutatorClient, http.MethodPost, server.URL+"/v1/admin/alerts", map[string]any{
		"name":        "Session revocation event",
		"kind":        "metric_threshold",
		"condition":   map[string]any{"operator": "gte", "threshold": 2, "metric": "system.cpu.utilization"},
		"severity":    "warning",
		"for_seconds": 0,
		"enabled":     true,
	}, http.StatusCreated, &sessionAlert)
	sessionAlertID, err := uuid.Parse(sessionAlert.Rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	alertIDs = append(alertIDs, sessionAlertID)
	assertAdminRealtimeClosedWithoutEvent(t, sessionReader)
}

func openAdminRealtimeStream(t *testing.T, client *http.Client, baseURL string) (*http.Response, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	streamContext, streamCancel := context.WithTimeout(context.Background(), 3*time.Second)
	request, err := http.NewRequestWithContext(streamContext, http.MethodGet, baseURL+"/v1/admin/realtime", nil)
	if err != nil {
		streamCancel()
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		streamCancel()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		streamCancel()
		t.Fatalf("admin SSE response status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	if line, readErr := reader.ReadString('\n'); readErr != nil || strings.TrimSpace(line) != "retry: 3000" {
		response.Body.Close()
		streamCancel()
		t.Fatalf("admin SSE prelude = %q, err = %v", line, readErr)
	}
	if line, readErr := reader.ReadString('\n'); readErr != nil || strings.TrimSpace(line) != "" {
		response.Body.Close()
		streamCancel()
		t.Fatalf("admin SSE prelude terminator = %q, err = %v", line, readErr)
	}
	return response, reader, streamCancel
}

func assertAdminRealtimeClosedWithoutEvent(t *testing.T, reader *bufio.Reader) {
	t.Helper()
	closed := make(chan error, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				closed <- err
				return
			}
			if strings.HasPrefix(line, "id: ") {
				closed <- fmt.Errorf("revoked admin SSE emitted an event: %q", strings.TrimSpace(line))
				return
			}
		}
	}()
	select {
	case err := <-closed:
		if err != io.EOF {
			t.Fatalf("revoked admin SSE closed with unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoked admin SSE did not close within the authorization recheck bound")
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
