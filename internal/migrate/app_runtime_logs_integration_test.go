package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAppRuntimeLogSourcesMigrationBackfillConstraintsAndDownIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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

	schema := fmt.Sprintf("app_runtime_logs_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000062_app_runtime_logs.up.sql"); err != nil {
		t.Fatal(err)
	}

	upSQL, err := files.ReadFile("migrations/000062_app_runtime_logs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := files.ReadFile("migrations/000062_app_runtime_logs.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("clean migration apply: %v", err)
	}
	assertAppRuntimeLogSourcesSchema(t, ctx, conn, true)
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("clean migration down: %v", err)
	}
	assertAppRuntimeLogSourcesSchema(t, ctx, conn, false)

	orgID, projectID, appID, secondAppID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := conn.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Runtime log migration','runtime-log-migration')`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,'runtime-log-migration')`, projectID, orgID); err != nil {
		t.Fatal(err)
	}
	spec, err := workloadspec.MarshalCanonical(workloadspec.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, appID := range []uuid.UUID{appID, secondAppID} {
		if _, err := conn.Exec(ctx, `
			INSERT INTO project_apps (id,project_id,name,platform_label,enabled,workload_spec,workload_spec_sha256,desired_generation,observed_generation,runtime_status)
			VALUES ($1,$2,$3,$4,true,$5::jsonb,repeat('a',64),1,0,'pending')`, appID, projectID, "app-"+strings.ReplaceAll(appID.String(), "-", "")[:8], "app-"+strings.ReplaceAll(appID.String(), "-", "")[:8], spec); err != nil {
			t.Fatal(err)
		}
	}
	containerID := strings.Repeat("a", 64)
	routeIdentity := uuid.Must(uuid.NewV7())
	containerName := "st-" + strings.ReplaceAll(appID.String(), "-", "") + "-" + strings.ReplaceAll(routeIdentity.String(), "-", "")[:24]
	if _, err := conn.Exec(ctx, `INSERT INTO app_runtime_state (app_id,project_id,container_id,container_name,route_identity) VALUES ($1,$2,$3,$4,$5)`, appID, projectID, containerID, containerName, routeIdentity); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("populated migration apply: %v", err)
	}
	assertAppRuntimeLogSourcesSchema(t, ctx, conn, true)
	var backfilled int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_runtime_log_sources WHERE project_id=$1 AND app_id=$2 AND container_id=$3`, projectID, appID, containerID).Scan(&backfilled); err != nil || backfilled != 1 {
		t.Fatalf("backfilled source count=%d err=%v", backfilled, err)
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO app_runtime_log_sources (app_id,project_id,container_id)
		VALUES ($1,$2,$3)
		ON CONFLICT (app_id,container_id) DO UPDATE SET last_seen_at=now(),updated_at=now()`, appID, projectID, containerID); err != nil {
		t.Fatalf("same source upsert: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_runtime_log_sources WHERE app_id=$1 AND container_id=$2`, appID, containerID).Scan(&backfilled); err != nil || backfilled != 1 {
		t.Fatalf("idempotent source count=%d err=%v", backfilled, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO app_runtime_log_sources (app_id,project_id,container_id) VALUES ($1,$2,$3)`, secondAppID, projectID, containerID); err == nil {
		t.Fatal("one Docker container ID was accepted for two Apps")
	}
	if _, err := conn.Exec(ctx, `DELETE FROM project_apps WHERE id=$1 AND project_id=$2`, appID, projectID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_runtime_log_sources WHERE app_id=$1`, appID).Scan(&backfilled); err != nil || backfilled != 0 {
		t.Fatalf("App deletion left %d runtime log sources (err=%v)", backfilled, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO app_runtime_log_sources (app_id,project_id,container_id) VALUES ($1,$2,$3)`, secondAppID, projectID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_runtime_log_sources`).Scan(&backfilled); err != nil || backfilled != 0 {
		t.Fatalf("project deletion left %d runtime log sources (err=%v)", backfilled, err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("populated migration down: %v", err)
	}
	assertAppRuntimeLogSourcesSchema(t, ctx, conn, false)
}

func assertAppRuntimeLogSourcesSchema(t *testing.T, ctx context.Context, conn *pgxpool.Conn, wantPresent bool) {
	t.Helper()
	var table, index, constraints int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='app_runtime_log_sources'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='app_runtime_log_sources_project_app_idx'`).Scan(&index); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class r ON r.oid=c.conrelid
		JOIN pg_namespace n ON n.oid=r.relnamespace
		WHERE n.nspname=current_schema() AND r.relname='app_runtime_log_sources'`).Scan(&constraints); err != nil {
		t.Fatal(err)
	}
	wantTable, wantIndex, wantConstraints := 0, 0, 0
	if wantPresent {
		wantTable, wantIndex, wantConstraints = 1, 1, 5
	}
	if table != wantTable || index != wantIndex || constraints != wantConstraints {
		t.Fatalf("App runtime log schema objects table:%d index:%d constraints:%d, want %d/%d/%d", table, index, constraints, wantTable, wantIndex, wantConstraints)
	}
}
