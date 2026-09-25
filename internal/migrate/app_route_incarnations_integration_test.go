package migrate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAppRouteIncarnationMigrationDownRestoresPriorSchemaIntegration(t *testing.T) {
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
	t.Cleanup(pool.Close)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)

	schema := fmt.Sprintf("app_route_incarnation_rollback_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000061_app_route_incarnations.up.sql"); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeContainerNameNullable(t, ctx, conn, "YES")

	upSQL, err := files.ReadFile("migrations/000061_app_route_incarnations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeContainerNameNullable(t, ctx, conn, "NO")
	assertAppRouteIncarnationSchema(t, ctx, conn, true)

	downSQL, err := files.ReadFile("migrations/000061_app_route_incarnations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeContainerNameNullable(t, ctx, conn, "YES")
	assertAppRouteIncarnationSchema(t, ctx, conn, false)
}

func assertAppRuntimeContainerNameNullable(t *testing.T, ctx context.Context, conn *pgxpool.Conn, want string) {
	t.Helper()
	var got string
	if err := conn.QueryRow(ctx, `
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='app_runtime_state' AND column_name='container_name'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("app_runtime_state.container_name is_nullable = %q, want %q", got, want)
	}
}

func assertAppRouteIncarnationSchema(t *testing.T, ctx context.Context, conn *pgxpool.Conn, wantPresent bool) {
	t.Helper()
	var columns, constraints, indexes int
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='app_runtime_state'
		  AND column_name IN ('route_identity','health_route_identity')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_constraint constraint_row
		JOIN pg_class relation ON relation.oid=constraint_row.conrelid
		JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		WHERE namespace.nspname=current_schema() AND relation.relname='app_runtime_state'
		  AND constraint_row.conname IN (
		    'app_runtime_state_route_identity_valid',
		    'app_runtime_state_route_target_matches_identity',
		    'app_runtime_state_health_route_identity_consistent'
		  )`).Scan(&constraints); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_indexes
		WHERE schemaname=current_schema() AND indexname IN (
		  'app_runtime_state_route_identity_idx','app_runtime_state_container_name_idx'
		)`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	wantColumns, wantConstraints, wantIndexes := 0, 0, 0
	if wantPresent {
		wantColumns, wantConstraints, wantIndexes = 2, 3, 2
	}
	if columns != wantColumns || constraints != wantConstraints || indexes != wantIndexes {
		t.Fatalf("route-incarnation schema objects = columns:%d constraints:%d indexes:%d, want columns:%d constraints:%d indexes:%d", columns, constraints, indexes, wantColumns, wantConstraints, wantIndexes)
	}
}
