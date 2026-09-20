package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	var eventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM admin_alert_events WHERE rule_id=$1`, ruleID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	requestJSON(t, client, http.MethodDelete, server.URL+"/v1/admin/alerts/"+ruleID.String(), nil, http.StatusNoContent, nil)
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/admin/alerts/"+ruleID.String(), nil, http.StatusNotFound, nil)

	events, err := repo.ListAdminAlertEvents(ctx, ruleID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != eventID.String() || events[0].RuleID != ruleID.String() {
		t.Fatalf("history query = %#v, want retained event %s", events, eventID)
	}
	var deliveries, deleteAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_notification_deliveries WHERE alert_event_id=$1 AND channel_id=$2`, eventID, channelID).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE actor_account_id=$1 AND action='admin.alert.delete' AND target_id=$2`, accountID, ruleID).Scan(&deleteAudits); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || deleteAudits != 1 {
		t.Fatalf("retained deliveries=%d delete_audits=%d, want 1/1", deliveries, deleteAudits)
	}
}
