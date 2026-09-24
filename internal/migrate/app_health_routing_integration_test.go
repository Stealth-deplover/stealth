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

func TestAppHealthMigrationKeepsExistingRuntimeNonRoutableIntegration(t *testing.T) {
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

	schema := fmt.Sprintf("app_health_migration_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000060_app_health_routing.up.sql"); err != nil {
		t.Fatal(err)
	}

	organizationID, projectID, appID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := conn.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Existing App health migration','app-health-migration')`, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,'existing-health-app')`, projectID, organizationID); err != nil {
		t.Fatal(err)
	}
	spec := workloadspec.Default()
	canonical, err := workloadspec.MarshalCanonical(spec)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workloadspec.Digest(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO project_apps (id,project_id,name,platform_label,enabled,workload_spec,workload_spec_sha256,desired_generation,observed_generation,runtime_status)
		VALUES ($1,$2,'existing-health-app','existing-health-app',true,$3::jsonb,$4,1,1,'running')`, appID, projectID, canonical, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO app_runtime_state (app_id,project_id,container_id,container_name,applied_workload_spec_sha256,applied_generation)
		VALUES ($1,$2,$3,$4,$5,1)`, appID, projectID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "stealth-app-"+appID.String(), digest); err != nil {
		t.Fatal(err)
	}
	upSQL, err := files.ReadFile("migrations/000060_app_health_routing.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatal(err)
	}
	var healthStatus string
	var healthGeneration *int64
	var healthDeploymentID, healthContainerID, address *string
	if err := conn.QueryRow(ctx, `
		SELECT health_status,health_generation,health_deployment_id::text,health_container_id,container_address::text
		FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&healthStatus, &healthGeneration, &healthDeploymentID, &healthContainerID, &address); err != nil {
		t.Fatal(err)
	}
	if healthStatus != "pending" || healthGeneration != nil || healthDeploymentID != nil || healthContainerID != nil || address != nil {
		t.Fatalf("existing runtime was implicitly marked routable: status=%q generation=%v deployment=%v container=%v address=%v", healthStatus, healthGeneration, healthDeploymentID, healthContainerID, address)
	}
}
