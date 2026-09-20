package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeleteAdminAlertRulePreservesHistoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	server := httptest.NewServer(httpapi.New(config.Config{
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("k"), 32),
	}, repo, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := newIntegrationClient(t)
	admin := registerAdminTestAccount(t, client, server.URL, "alert-history")
	accountID, err := uuid.Parse(admin.accountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_admin')`, accountID); err != nil {
		t.Fatal(err)
	}
	var emptyHistory struct {
		Items []json.RawMessage `json:"items"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alerts/"+uuid.Must(uuid.NewV7()).String()+"/events?limit=10", nil, http.StatusOK, &emptyHistory)
	if len(emptyHistory.Items) != 0 {
		t.Fatalf("history for an unknown rule = %d items, want empty", len(emptyHistory.Items))
	}
	for _, suffix := range []string{
		"/v1/admin/alert-events?cursor=not-a-cursor",
		"/v1/admin/alert-events?from=not-a-timestamp",
		"/v1/admin/alert-events?to=not-a-timestamp",
		"/v1/admin/alert-events?from=2026-09-20T12:00:00Z&to=2026-09-20T12:00:00Z",
		"/v1/admin/alert-events?limit=0",
		"/v1/admin/alert-events?limit=101",
	} {
		requestJSON(t, client, http.MethodGet, server.URL+suffix, nil, http.StatusBadRequest, nil)
	}
	channelID := uuid.Must(uuid.NewV7())
	var ruleID uuid.UUID
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_notification_deliveries WHERE channel_id=$1`, channelID)
		if ruleID != uuid.Nil {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_events WHERE rule_id=$1 OR rule_id_snapshot=$1`, ruleID)
		}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_notification_channels WHERE id=$1`, channelID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, accountID, admin.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, admin.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, accountID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO admin_notification_channels (id,name,kind,config_encrypted,created_by_account_id)
		VALUES ($1,'HTTP history channel','webhook','\\x01'::bytea,$2)`, channelID, accountID); err != nil {
		t.Fatal(err)
	}

	var created struct {
		Rule struct {
			ID string `json:"id"`
		} `json:"rule"`
	}
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/admin/alerts", map[string]any{
		"name":        "HTTP alert history",
		"kind":        "metric_threshold",
		"condition":   map[string]any{"operator": "gte", "threshold": 1, "metric": "system.cpu.utilization"},
		"severity":    "critical",
		"for_seconds": 0,
		"enabled":     true,
	}, http.StatusCreated, &created)
	ruleID, err = uuid.Parse(created.Rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	value := 91.0
	if err := repo.EvaluateAdminAlert(ctx, ruleID, true, &value, "HTTP alert history is firing"); err != nil {
		t.Fatal(err)
	}
	var firingEventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM admin_alert_events WHERE rule_id=$1 AND state='firing'`, ruleID).Scan(&firingEventID); err != nil {
		t.Fatal(err)
	}
	value = 10
	if err := repo.EvaluateAdminAlert(ctx, ruleID, false, &value, "HTTP alert history is resolved"); err != nil {
		t.Fatal(err)
	}
	var resolvedEventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM admin_alert_events WHERE rule_id=$1 AND state='resolved'`, ruleID).Scan(&resolvedEventID); err != nil {
		t.Fatal(err)
	}

	requestJSON(t, client, http.MethodDelete, server.URL+"/v1/admin/alerts/"+ruleID.String(), nil, http.StatusNoContent, nil)
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alerts/"+ruleID.String(), nil, http.StatusNotFound, nil)
	var history struct {
		Items []struct {
			ID               string    `json:"id"`
			RuleID           string    `json:"rule_id"`
			RuleName         string    `json:"rule_name"`
			RuleKind         string    `json:"rule_kind"`
			Severity         string    `json:"severity"`
			State            string    `json:"state"`
			Value            *float64  `json:"value"`
			Message          string    `json:"message"`
			OccurredAt       time.Time `json:"occurred_at"`
			SourceRuleExists bool      `json:"source_rule_exists"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alerts/"+ruleID.String()+"/events?limit=1", nil, http.StatusOK, &history)
	if len(history.Items) != 1 || history.NextCursor == nil || *history.NextCursor == "" {
		t.Fatalf("first alert history page = %#v, want one item and next cursor", history)
	}
	firstID := history.Items[0].ID
	var nextHistory struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alerts/"+ruleID.String()+"/events?limit=1&cursor="+url.QueryEscape(*history.NextCursor), nil, http.StatusOK, &nextHistory)
	if len(nextHistory.Items) != 1 || nextHistory.Items[0].ID == firstID || nextHistory.NextCursor != nil {
		t.Fatalf("second alert history page = %#v, want final distinct item", nextHistory)
	}
	var filteredHistory struct {
		Items []json.RawMessage `json:"items"`
	}
	from := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	to := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alerts/"+ruleID.String()+"/events?limit=10&from="+url.QueryEscape(from)+"&to="+url.QueryEscape(to), nil, http.StatusOK, &filteredHistory)
	if len(filteredHistory.Items) != 2 {
		t.Fatalf("filtered alert history items = %d, want 2", len(filteredHistory.Items))
	}
	var eventFrom, eventTo time.Time
	if err := pool.QueryRow(ctx, `
		SELECT min(occurred_at),max(occurred_at)
		FROM admin_alert_events WHERE rule_id_snapshot=$1`, ruleID).Scan(&eventFrom, &eventTo); err != nil {
		t.Fatal(err)
	}
	globalQuery := url.Values{}
	globalQuery.Set("limit", "1")
	globalQuery.Set("from", eventFrom.Add(-time.Second).Format(time.RFC3339Nano))
	globalQuery.Set("to", eventTo.Add(time.Second).Format(time.RFC3339Nano))
	var globalFirst struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alert-events?"+globalQuery.Encode(), nil, http.StatusOK, &globalFirst)
	if len(globalFirst.Items) != 1 || globalFirst.NextCursor == nil || *globalFirst.NextCursor == "" {
		t.Fatalf("first global alert history page = %#v, want one item and next cursor", globalFirst)
	}
	globalQuery.Set("cursor", *globalFirst.NextCursor)
	var globalSecond struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alert-events?"+globalQuery.Encode(), nil, http.StatusOK, &globalSecond)
	if len(globalSecond.Items) != 1 || globalSecond.Items[0].ID == globalFirst.Items[0].ID || globalSecond.NextCursor != nil {
		t.Fatalf("second global alert history page = %#v, want final distinct item", globalSecond)
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alerts/"+ruleID.String()+"/events?limit=10", nil, http.StatusOK, &history)
	if len(history.Items) != 2 {
		t.Fatalf("HTTP alert history items = %d, want 2", len(history.Items))
	}
	seenStates := map[string]bool{}
	for _, event := range history.Items {
		if event.RuleID != ruleID.String() || event.RuleName != "HTTP alert history" || event.RuleKind != "metric_threshold" || event.Severity != "critical" || event.SourceRuleExists {
			t.Fatalf("HTTP alert history event = %#v, want deleted-rule snapshot", event)
		}
		seenStates[event.State] = true
	}
	if !seenStates["firing"] || !seenStates["resolved"] {
		t.Fatalf("HTTP alert history states = %#v, want firing and resolved", seenStates)
	}
	var recent struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alert-events?limit=100", nil, http.StatusOK, &recent)
	foundRecent := false
	for _, event := range recent.Items {
		if event.ID == firingEventID.String() || event.ID == resolvedEventID.String() {
			foundRecent = true
			break
		}
	}
	if !foundRecent {
		t.Fatalf("global alert history did not include retained events %s/%s", firingEventID, resolvedEventID)
	}

	events, err := repo.ListAdminAlertEvents(ctx, ruleID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].RuleID != ruleID.String() || events[1].RuleID != ruleID.String() {
		t.Fatalf("history query = %#v, want retained events %s/%s", events, firingEventID, resolvedEventID)
	}
	var deliveries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_notification_deliveries WHERE alert_event_id IN ($1,$2) AND channel_id=$3`, firingEventID, resolvedEventID, channelID).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	var auditName, auditKind, auditSeverity string
	if err := pool.QueryRow(ctx, `
		SELECT metadata->>'name',metadata->>'kind',metadata->>'severity' FROM audit_events
		WHERE actor_account_id=$1 AND action='admin.alert.delete' AND target_id=$2`, accountID, ruleID).Scan(&auditName, &auditKind, &auditSeverity); err != nil {
		t.Fatal(err)
	}
	if deliveries != 2 || auditName != "HTTP alert history" || auditKind != "metric_threshold" || auditSeverity != "critical" {
		t.Fatalf("retained deliveries=%d delete audit=%q/%q/%q, want 2 and safe rule metadata", deliveries, auditName, auditKind, auditSeverity)
	}
}
