package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminAlertHistoryMigrationBackfillsAndPreservesDeliveriesIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)

	schema := fmt.Sprintf("aud04_history_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBeforeAlertHistory(ctx, conn, files); err != nil {
		t.Fatal(err)
	}

	const (
		ruleID     = "00000000-0000-7000-8000-000000000031"
		eventID    = "00000000-0000-7000-8000-000000000032"
		channelID  = "00000000-0000-7000-8000-000000000033"
		deliveryID = "00000000-0000-7000-8000-000000000034"
	)
	for _, statement := range []string{
		`INSERT INTO admin_alert_rules (id,name,kind,condition,severity) VALUES ('` + ruleID + `','Legacy alert history','metric_threshold','{"operator":"gte","threshold":90}','critical')`,
		`INSERT INTO admin_alert_events (id,rule_id,state,message) VALUES ('` + eventID + `','` + ruleID + `','firing','Legacy alert history is firing')`,
		`INSERT INTO admin_notification_channels (id,name,kind,config_encrypted) VALUES ('` + channelID + `','Legacy history channel','webhook','\x01'::bytea)`,
		`INSERT INTO admin_notification_deliveries (id,channel_id,alert_event_id) VALUES ('` + deliveryID + `','` + channelID + `','` + eventID + `')`,
	} {
		if _, err := conn.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	migrationSQL, err := files.ReadFile("migrations/000047_admin_alert_history.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(migrationSQL)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM admin_alert_rules WHERE id='`+ruleID+`'`); err != nil {
		t.Fatalf("delete migrated alert rule: %v", err)
	}

	var liveRuleCleared, idSnapshotted, nameSnapshotted, conditionSnapshotted, indexCreated, deliveryRetained bool
	if err := conn.QueryRow(ctx, `
		SELECT rule_id IS NULL,
		       rule_id_snapshot::text='`+ruleID+`',
		       rule_name_snapshot='Legacy alert history',
		       condition_snapshot='{"operator":"gte","threshold":90}'::jsonb,
		       EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='admin_alert_events_rule_snapshot_time_idx'),
		       EXISTS (SELECT 1 FROM admin_notification_deliveries WHERE id='`+deliveryID+`' AND alert_event_id='`+eventID+`')
		FROM admin_alert_events WHERE id='`+eventID+`'`).Scan(&liveRuleCleared, &idSnapshotted, &nameSnapshotted, &conditionSnapshotted, &indexCreated, &deliveryRetained); err != nil {
		t.Fatal(err)
	}
	if !liveRuleCleared || !idSnapshotted || !nameSnapshotted || !conditionSnapshotted || !indexCreated || !deliveryRetained {
		t.Fatalf("migrated history snapshot or delivery was not preserved")
	}
	if _, err := conn.Exec(ctx, `UPDATE admin_notification_deliveries SET alert_event_id=NULL WHERE id='`+deliveryID+`'`); err == nil {
		t.Fatal("normal alert delivery accepted a missing event source")
	}
}

func applyMigrationsBeforeAlertHistory(ctx context.Context, conn *pgxpool.Conn, embedded fs.FS) error {
	entries, err := fs.ReadDir(embedded, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") && entry.Name() < "000047_admin_alert_history.up.sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		sql, err := fs.ReadFile(embedded, "migrations/"+name)
		if err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}
