package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminAlertHistoryFixture struct {
	pool       *pgxpool.Pool
	repo       *Repository
	accountID  uuid.UUID
	ruleID     uuid.UUID
	channelIDs []uuid.UUID
}

func newAdminAlertHistoryFixture(t *testing.T, channels int) adminAlertHistoryFixture {
	t.Helper()
	if channels < 1 {
		t.Fatal("channels must be positive")
	}
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	fixture := adminAlertHistoryFixture{
		pool:      pool,
		repo:      New(pool),
		accountID: uuid.Must(uuid.NewV7()),
		ruleID:    uuid.Must(uuid.NewV7()),
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id,email,password_hash)
		VALUES ($1,$2,'test-password-hash')`, fixture.accountID, "alert-history-"+fixture.accountID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_admin')`, fixture.accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO admin_alert_rules (id,name,kind,condition,severity,created_by_account_id)
		VALUES ($1,'Delete history regression','metric_threshold','{"operator":"gte","threshold":90,"metric":"system.cpu.utilization"}'::jsonb,'critical',$2)`, fixture.ruleID, fixture.accountID); err != nil {
		t.Fatal(err)
	}
	for range channels {
		channelID := uuid.Must(uuid.NewV7())
		if _, err := pool.Exec(ctx, `
			INSERT INTO admin_notification_channels (id,name,kind,config_encrypted,created_by_account_id)
			VALUES ($1,'History channel','webhook','\\x01'::bytea,$2)`, channelID, fixture.accountID); err != nil {
			t.Fatal(err)
		}
		fixture.channelIDs = append(fixture.channelIDs, channelID)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, channelID := range fixture.channelIDs {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_notification_deliveries WHERE channel_id=$1`, channelID)
		}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_events WHERE rule_id=$1 OR rule_id_snapshot=$1`, fixture.ruleID)
		for _, channelID := range fixture.channelIDs {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_notification_channels WHERE id=$1`, channelID)
		}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_rules WHERE id=$1`, fixture.ruleID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id=$1`, fixture.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM instance_roles WHERE account_id=$1`, fixture.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, fixture.accountID)
	})
	return fixture
}

func (f adminAlertHistoryFixture) fire(t *testing.T) uuid.UUID {
	t.Helper()
	value := 91.0
	if err := f.repo.EvaluateAdminAlert(context.Background(), f.ruleID, true, &value, "Delete history regression is firing"); err != nil {
		t.Fatal(err)
	}
	var eventID uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `
		SELECT id FROM admin_alert_events
		WHERE rule_id=$1 AND state='firing'
		ORDER BY occurred_at DESC,id DESC LIMIT 1`, f.ruleID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func (f adminAlertHistoryFixture) resolve(t *testing.T) uuid.UUID {
	t.Helper()
	value := 10.0
	if err := f.repo.EvaluateAdminAlert(context.Background(), f.ruleID, false, &value, "Delete history regression is firing"); err != nil {
		t.Fatal(err)
	}
	var eventID uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `
		SELECT id FROM admin_alert_events
		WHERE rule_id=$1 AND state='resolved'
		ORDER BY occurred_at DESC,id DESC LIMIT 1`, f.ruleID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func (f adminAlertHistoryFixture) deleteRule(t *testing.T) {
	t.Helper()
	if err := f.repo.DeleteAdminAlertRule(context.Background(), f.accountID, f.ruleID); err != nil {
		t.Fatalf("DeleteAdminAlertRule() error = %v, want successful history-preserving deletion", err)
	}
}

func (f adminAlertHistoryFixture) assertRuleDeleted(t *testing.T) {
	t.Helper()
	var rules int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_alert_rules WHERE id=$1`, f.ruleID).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules != 0 {
		t.Fatalf("active alert rules = %d, want 0", rules)
	}
}

func (f adminAlertHistoryFixture) assertHistoricalEvent(t *testing.T, eventID uuid.UUID, wantState string) {
	t.Helper()
	var liveRuleID *uuid.UUID
	var ruleIDSnapshot uuid.UUID
	var name, kind, severity, state string
	var conditionSnapshotted bool
	if err := f.pool.QueryRow(context.Background(), `
		SELECT rule_id,rule_id_snapshot,rule_name_snapshot,rule_kind_snapshot,severity_snapshot,state,
		       condition_snapshot='{"operator":"gte","metric":"system.cpu.utilization","threshold":90}'::jsonb
		FROM admin_alert_events WHERE id=$1`, eventID).Scan(&liveRuleID, &ruleIDSnapshot, &name, &kind, &severity, &state, &conditionSnapshotted); err != nil {
		t.Fatal(err)
	}
	if liveRuleID != nil || ruleIDSnapshot != f.ruleID || name != "Delete history regression" || kind != "metric_threshold" || severity != "critical" || state != wantState || !conditionSnapshotted {
		t.Fatalf("event history snapshot does not match the deleted rule")
	}
}

func (f adminAlertHistoryFixture) assertHistoricalDeliveries(t *testing.T, eventID uuid.UUID, want int, status string) {
	t.Helper()
	var deliveries int
	for _, channelID := range f.channelIDs {
		var count int
		if err := f.pool.QueryRow(context.Background(), `
			SELECT count(*) FROM admin_notification_deliveries
			WHERE alert_event_id=$1 AND channel_id=$2 AND status=$3`, eventID, channelID, status).Scan(&count); err != nil {
			t.Fatal(err)
		}
		deliveries += count
	}
	if deliveries != want {
		t.Fatalf("fixture historical deliveries for %s with status %q = %d, want %d", eventID, status, deliveries, want)
	}
}

func (f adminAlertHistoryFixture) claimFixtureDelivery(t *testing.T, eventID uuid.UUID) AdminNotificationDeliveryJob {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE admin_notification_deliveries
		SET available_at='1970-01-01 00:00:00+00'
		WHERE alert_event_id=$1 AND channel_id=$2 AND status='pending'`, eventID, f.channelIDs[0]); err != nil {
		t.Fatal(err)
	}
	job, err := f.repo.ClaimNextAdminNotificationDelivery(context.Background(), "alert-history-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if job.ChannelID != f.channelIDs[0] || job.AlertEventID == nil || *job.AlertEventID != eventID {
		t.Fatal("claimed a delivery outside the alert-history fixture")
	}
	return job
}

func (f adminAlertHistoryFixture) assertDeleteAudit(t *testing.T) {
	t.Helper()
	var name, kind, severity string
	if err := f.pool.QueryRow(context.Background(), `
		SELECT metadata->>'name',metadata->>'kind',metadata->>'severity' FROM audit_events
		WHERE actor_account_id=$1
		  AND action='admin.alert.delete'
		  AND target_type='admin_alert_rule'
		  AND target_id=$2`, f.accountID, f.ruleID).Scan(&name, &kind, &severity); err != nil {
		t.Fatal(err)
	}
	if name != "Delete history regression" || kind != "metric_threshold" || severity != "critical" {
		t.Fatalf("delete audit metadata = %q/%q/%q, want safe rule metadata", name, kind, severity)
	}
}

// TestDeleteAlertRuleAfterNotificationDeliveryIntegration protects the alert
// history lifecycle: a live rule can be deleted after it has created a normal
// alert notification, without deleting the event or its delivery history.
func TestDeleteAlertRuleAfterNotificationDeliveryIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 1)
	eventID := fixture.fire(t)
	fixture.assertHistoricalDeliveries(t, eventID, 1, "pending")
	fixture.deleteRule(t)
	fixture.assertRuleDeleted(t)
	fixture.assertDeleteAudit(t)
	fixture.assertHistoricalEvent(t, eventID, "firing")
	fixture.assertHistoricalDeliveries(t, eventID, 1, "pending")

	events, err := fixture.repo.ListAdminAlertEvents(context.Background(), fixture.ruleID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != eventID.String() || events[0].RuleID != fixture.ruleID.String() || events[0].RuleName != "Delete history regression" || events[0].RuleKind != "metric_threshold" || events[0].Severity != "critical" || events[0].SourceRuleExists {
		t.Fatalf("historical event query = %#v, want retained event for %s", events, fixture.ruleID)
	}
}

func TestDeleteAlertRuleAfterResolveRetainsAllHistoryIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 1)
	firingID := fixture.fire(t)
	resolvedID := fixture.resolve(t)
	fixture.deleteRule(t)
	fixture.assertRuleDeleted(t)
	fixture.assertHistoricalEvent(t, firingID, "firing")
	fixture.assertHistoricalEvent(t, resolvedID, "resolved")
	fixture.assertHistoricalDeliveries(t, firingID, 1, "pending")
	fixture.assertHistoricalDeliveries(t, resolvedID, 1, "pending")
}

func TestDeleteAlertRuleNeverFiredIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 1)
	fixture.deleteRule(t)
	fixture.assertRuleDeleted(t)
	var events int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_alert_events WHERE rule_id_snapshot=$1`, fixture.ruleID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("historical events = %d, want 0", events)
	}
}

func TestDeleteAlertRuleRetainsCompletedAndRetryableDeliveriesIntegration(t *testing.T) {
	t.Run("successful delivery", func(t *testing.T) {
		fixture := newAdminAlertHistoryFixture(t, 1)
		eventID := fixture.fire(t)
		job := fixture.claimFixtureDelivery(t, eventID)
		if err := fixture.repo.FinishAdminNotificationDelivery(context.Background(), job.DeliveryID, "alert-history-worker", true, "", nil); err != nil {
			t.Fatal(err)
		}
		fixture.deleteRule(t)
		fixture.assertHistoricalEvent(t, eventID, "firing")
		fixture.assertHistoricalDeliveries(t, eventID, 1, "delivered")
	})

	t.Run("failed delivery remains", func(t *testing.T) {
		fixture := newAdminAlertHistoryFixture(t, 1)
		eventID := fixture.fire(t)
		job := fixture.claimFixtureDelivery(t, eventID)
		if err := fixture.repo.FinishAdminNotificationDelivery(context.Background(), job.DeliveryID, "alert-history-worker", false, "provider rejected request", nil); err != nil {
			t.Fatal(err)
		}
		fixture.deleteRule(t)
		fixture.assertHistoricalEvent(t, eventID, "firing")
		fixture.assertHistoricalDeliveries(t, eventID, 1, "failed")
	})

	t.Run("failed delivery retries after deletion", func(t *testing.T) {
		fixture := newAdminAlertHistoryFixture(t, 1)
		eventID := fixture.fire(t)
		fixture.deleteRule(t)
		job := fixture.claimFixtureDelivery(t, eventID)
		if job.RuleName != "Delete history regression" || job.Severity != "critical" || job.AlertEventID == nil || *job.AlertEventID != eventID {
			t.Fatalf("delivery job after deletion did not retain the event snapshot")
		}
		retryAt := time.Unix(0, 0).UTC()
		if err := fixture.repo.FinishAdminNotificationDelivery(context.Background(), job.DeliveryID, "alert-history-worker", false, "temporary provider failure", &retryAt); err != nil {
			t.Fatal(err)
		}
		fixture.assertHistoricalDeliveries(t, eventID, 1, "pending")
		retryJob := fixture.claimFixtureDelivery(t, eventID)
		if retryJob.DeliveryID != job.DeliveryID || retryJob.RuleName != "Delete history regression" || retryJob.Severity != "critical" {
			t.Fatal("retried delivery did not preserve its historical alert snapshot")
		}
		if err := fixture.repo.FinishAdminNotificationDelivery(context.Background(), retryJob.DeliveryID, "alert-history-worker", true, "", nil); err != nil {
			t.Fatal(err)
		}
		fixture.assertHistoricalEvent(t, eventID, "firing")
		fixture.assertHistoricalDeliveries(t, eventID, 1, "delivered")
	})
}

func TestDeleteAlertRuleRetainsMultipleNotificationDeliveriesIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 2)
	eventID := fixture.fire(t)
	fixture.deleteRule(t)
	fixture.assertRuleDeleted(t)
	fixture.assertHistoricalEvent(t, eventID, "firing")
	fixture.assertHistoricalDeliveries(t, eventID, 2, "pending")
}

func TestEvaluateAndDeleteAdminAlertRuleRaceProducesValidHistoryIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lockTx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(context.Background())
	var lockedID uuid.UUID
	if err := lockTx.QueryRow(ctx, `SELECT id FROM admin_alert_rules WHERE id=$1 FOR UPDATE`, fixture.ruleID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	evaluateErr := make(chan error, 1)
	deleteErr := make(chan error, 1)
	go func() {
		value := 91.0
		evaluateErr <- fixture.repo.EvaluateAdminAlert(ctx, fixture.ruleID, true, &value, "Delete history regression is firing")
	}()
	go func() {
		deleteErr <- fixture.repo.DeleteAdminAlertRule(ctx, fixture.accountID, fixture.ruleID)
	}()
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-deleteErr; err != nil {
		t.Fatalf("DeleteAdminAlertRule() race error = %v", err)
	}
	if err := <-evaluateErr; err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatalf("EvaluateAdminAlert() race error = %v, want nil or ErrNotFound", err)
	}
	fixture.assertRuleDeleted(t)

	var invalidEvents int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM admin_alert_events
		WHERE rule_id_snapshot=$1 AND rule_id IS NOT NULL`, fixture.ruleID).Scan(&invalidEvents); err != nil {
		t.Fatal(err)
	}
	if invalidEvents != 0 {
		t.Fatalf("events retaining a deleted live rule = %d, want 0", invalidEvents)
	}
}
