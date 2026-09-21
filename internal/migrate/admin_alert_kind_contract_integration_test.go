package migrate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminAlertKindContractMigrationRetiresUnsupportedKindsIntegration(t *testing.T) {
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
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("aud16_kind_contract_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000049_admin_alert_kind_contract.up.sql"); err != nil {
		t.Fatal(err)
	}

	for _, values := range []struct {
		ruleID  string
		eventID string
		kind    string
	}{
		{"00000000-0000-7000-8000-000000000051", "00000000-0000-7000-8000-000000000052", "backup_failure"},
		{"00000000-0000-7000-8000-000000000053", "00000000-0000-7000-8000-000000000054", "job_failure"},
	} {
		if _, err := conn.Exec(ctx, `
			INSERT INTO admin_alert_rules (id,name,kind,condition,severity)
			VALUES ($1,$2,$3,'{"kind":"legacy"}'::jsonb,'warning')`, values.ruleID, "Legacy "+values.kind, values.kind); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `
			INSERT INTO admin_alert_events (
				id,rule_id,rule_id_snapshot,rule_name_snapshot,rule_kind_snapshot,severity_snapshot,condition_snapshot,
				state,message
			) VALUES ($1,$2,$2,$3,$4,'warning','{"kind":"legacy"}'::jsonb,'firing',$5)`,
			values.eventID, values.ruleID, "Legacy "+values.kind, values.kind, values.kind+" history"); err != nil {
			t.Fatal(err)
		}
	}

	migrationSQL, err := files.ReadFile("migrations/000049_admin_alert_kind_contract.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(migrationSQL)); err != nil {
		t.Fatal(err)
	}

	var retired, active, detached int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM admin_alert_rule_retired WHERE kind IN ('backup_failure','job_failure')`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM admin_alert_rules WHERE kind IN ('backup_failure','job_failure')`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM admin_alert_events WHERE rule_id IS NULL AND rule_kind_snapshot IN ('backup_failure','job_failure')`).Scan(&detached); err != nil {
		t.Fatal(err)
	}
	if retired != 2 || active != 0 || detached != 2 {
		t.Fatalf("retired=%d active=%d detached_history=%d, want 2/0/2", retired, active, detached)
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO admin_alert_rules (id,name,kind,condition,severity)
		VALUES ('00000000-0000-7000-8000-000000000055','unsupported','backup_failure','{}'::jsonb,'warning')`); err == nil {
		t.Fatal("active alert-rule schema still accepts retired backup_failure kind")
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO admin_alert_rules (id,name,kind,condition,severity)
		VALUES ('00000000-0000-7000-8000-000000000056','supported','metric_threshold','{"operator":"gte","threshold":1,"metric":"system.cpu.utilization"}'::jsonb,'warning')`); err != nil {
		t.Fatalf("supported alert kind rejected after migration: %v", err)
	}
}
