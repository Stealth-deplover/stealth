package migrate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAppEnvironmentVariablesMigrationPreservesExistingAppsAndDownIntegration(t *testing.T) {
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

	schema := fmt.Sprintf("app_environment_variables_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000063_app_environment_variables.up.sql"); err != nil {
		t.Fatal(err)
	}
	assertAppEnvironmentVariableSchema(t, ctx, conn, false)

	organizationID, projectID, cascadeProjectID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	appID, secondAppID, cascadeAppID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := conn.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'App env migration','app-env-migration')`, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,'app-env-migration')`, projectID, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,'app-env-cascade')`, cascadeProjectID, organizationID); err != nil {
		t.Fatal(err)
	}
	spec, err := workloadspec.MarshalCanonical(workloadspec.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []struct {
		id        uuid.UUID
		projectID uuid.UUID
		name      string
		label     string
	}{{appID, projectID, "existing-app", "existing-app"}, {secondAppID, projectID, "second-existing-app", "second-existing-app"}, {cascadeAppID, cascadeProjectID, "cascade-app", "cascade-app"}} {
		if _, err := conn.Exec(ctx, `
			INSERT INTO project_apps (id,project_id,name,platform_label,enabled,workload_spec,workload_spec_sha256,desired_generation,observed_generation,runtime_status)
			VALUES ($1,$2,$3,$4,true,$5::jsonb,repeat('a',64),1,0,'not_deployed')`, app.id, app.projectID, app.name, app.label, spec); err != nil {
			t.Fatal(err)
		}
	}

	upSQL, err := files.ReadFile("migrations/000063_app_environment_variables.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := files.ReadFile("migrations/000063_app_environment_variables.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply App environment migration: %v", err)
	}
	assertAppEnvironmentVariableSchema(t, ctx, conn, true)
	var existingApps, existingVariables int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM project_apps WHERE project_id=$1`, projectID).Scan(&existingApps); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_environment_variables WHERE app_id=$1`, appID).Scan(&existingVariables); err != nil {
		t.Fatal(err)
	}
	if existingApps != 2 || existingVariables != 0 {
		t.Fatalf("existing App migration state apps=%d variables=%d, want 2/0", existingApps, existingVariables)
	}
	ciphertext := make([]byte, 29)
	ciphertext[0] = 1
	firstVariableID, secondVariableID, cascadeVariableID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, variable := range []struct {
		id        uuid.UUID
		appID     uuid.UUID
		projectID uuid.UUID
	}{{firstVariableID, appID, projectID}, {secondVariableID, secondAppID, projectID}, {cascadeVariableID, cascadeAppID, cascadeProjectID}} {
		if _, err := conn.Exec(ctx, `INSERT INTO app_environment_variables (id,app_id,project_id,key,is_secret,value_ciphertext) VALUES ($1,$2,$3,'TOKEN',true,$4)`, variable.id, variable.appID, variable.projectID, ciphertext); err != nil {
			t.Fatalf("insert migrated App environment ciphertext: %v", err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO app_environment_variables (id,app_id,project_id,key) VALUES ($1,$2,$3,'TOKEN')`, uuid.Must(uuid.NewV7()), appID, projectID); err == nil {
		t.Fatal("duplicate App variable key was accepted")
	}
	if _, err := conn.Exec(ctx, `DELETE FROM project_apps WHERE id=$1 AND project_id=$2`, appID, projectID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_environment_variables WHERE app_id=$1`, appID).Scan(&existingVariables); err != nil || existingVariables != 0 {
		t.Fatalf("App deletion left %d environment variables (err=%v)", existingVariables, err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM projects WHERE id=$1`, cascadeProjectID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_environment_variables WHERE app_id=$1`, cascadeAppID).Scan(&existingVariables); err != nil || existingVariables != 0 {
		t.Fatalf("project deletion left %d environment variables (err=%v)", existingVariables, err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("rollback App environment migration: %v", err)
	}
	assertAppEnvironmentVariableSchema(t, ctx, conn, false)
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM project_apps WHERE id=$1`, secondAppID).Scan(&existingApps); err != nil || existingApps != 1 {
		t.Fatalf("migration rollback changed surviving App count=%d err=%v", existingApps, err)
	}
}

func assertAppEnvironmentVariableSchema(t *testing.T, ctx context.Context, conn *pgxpool.Conn, wantPresent bool) {
	t.Helper()
	var table, index, constraints int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='app_environment_variables'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='app_environment_variables_project_app_key_idx'`).Scan(&index); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
		WHERE n.nspname=current_schema() AND r.relname='app_environment_variables'`).Scan(&constraints); err != nil {
		t.Fatal(err)
	}
	wantTable, wantIndex, wantConstraints := 0, 0, 0
	if wantPresent {
		wantTable, wantIndex, wantConstraints = 1, 1, 7
	}
	if table != wantTable || index != wantIndex || constraints != wantConstraints {
		t.Fatalf("App environment schema objects table:%d index:%d constraints:%d, want %d/%d/%d", table, index, constraints, wantTable, wantIndex, wantConstraints)
	}
}
