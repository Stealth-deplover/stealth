package migrate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/platformhostname"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAppsPlatformHostnameMigrationPreservesExistingStateIntegration(t *testing.T) {
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

	schema := fmt.Sprintf("apps_platform_namespace_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000057_apps_workload_spec.up.sql"); err != nil {
		t.Fatal(err)
	}

	organizationID := uuid.Must(uuid.NewV7())
	projectID := uuid.Must(uuid.NewV7())
	firstSiteID := uuid.Must(uuid.NewV7())
	secondSiteID := uuid.Must(uuid.NewV7())
	legacyKeyID := uuid.Must(uuid.NewV7())
	newKeyID := uuid.Must(uuid.NewV7())
	if _, err := conn.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Apps migration test','apps-migration-test')`, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,'apps-migration-project')`, projectID, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain='apps.example.com' WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	for _, site := range []struct {
		id    uuid.UUID
		name  string
		label string
	}{
		{id: firstSiteID, name: "portfolio", label: "portfolio"},
		{id: secondSiteID, name: "status-page", label: "status-page-018f0d5e7c197abc8d1e123456789001"},
	} {
		if _, err := conn.Exec(ctx, `INSERT INTO project_sites (id,project_id,name,platform_label) VALUES ($1,$2,$3,$4)`, site.id, projectID, site.name, site.label); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO project_service_layouts (project_id,resource_type,resource_id,x,y) VALUES ($1,'site',$2,64,128)`, projectID, firstSiteID); err != nil {
		t.Fatal(err)
	}
	legacyScopes := []string{"sites.read", "users.read"}
	legacyHash := bytesOf(42, 32)
	if _, err := conn.Exec(ctx, `
		INSERT INTO project_api_keys (id,project_id,name,prefix,secret_hash,scopes)
		VALUES ($1,$2,'Legacy key','stl_key_12345678',$3,$4)`, legacyKeyID, projectID, legacyHash, legacyScopes); err != nil {
		t.Fatal(err)
	}

	labelsBefore := map[uuid.UUID]string{}
	for id := range map[uuid.UUID]struct{}{firstSiteID: {}, secondSiteID: {}} {
		var label string
		if err := conn.QueryRow(ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, id).Scan(&label); err != nil {
			t.Fatal(err)
		}
		labelsBefore[id] = label
	}
	upSQL, err := files.ReadFile("migrations/000057_apps_workload_spec.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatal(err)
	}

	var backfilled int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM platform_hostname_claims WHERE resource_type='site' AND project_id=$1`, projectID).Scan(&backfilled); err != nil {
		t.Fatal(err)
	}
	if backfilled != 2 {
		t.Fatalf("backfilled Site claims = %d, want 2", backfilled)
	}
	for id, want := range labelsBefore {
		var label string
		if err := conn.QueryRow(ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, id).Scan(&label); err != nil {
			t.Fatal(err)
		}
		if label != want {
			t.Fatalf("Site %s label changed from %q to %q", id, want, label)
		}
		beforeHostname, err := platformhostname.Hostname(want, "apps.example.com")
		if err != nil {
			t.Fatal(err)
		}
		afterHostname, err := platformhostname.Hostname(label, "apps.example.com")
		if err != nil || afterHostname != beforeHostname {
			t.Fatalf("Site %s hostname changed from %q to %q (error %v)", id, beforeHostname, afterHostname, err)
		}
		var claimLabel string
		if err := conn.QueryRow(ctx, `SELECT label FROM platform_hostname_claims WHERE resource_type='site' AND resource_id=$1`, id).Scan(&claimLabel); err != nil {
			t.Fatal(err)
		}
		if claimLabel != want {
			t.Fatalf("backfilled Site claim = %q, want %q", claimLabel, want)
		}
	}
	var layoutCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM project_service_layouts WHERE project_id=$1 AND resource_type='site' AND resource_id=$2 AND x=64 AND y=128`, projectID, firstSiteID).Scan(&layoutCount); err != nil {
		t.Fatal(err)
	}
	if layoutCount != 1 {
		t.Fatalf("preexisting Site layout rows = %d, want 1", layoutCount)
	}
	var gotLegacyScopes []string
	if err := conn.QueryRow(ctx, `SELECT scopes FROM project_api_keys WHERE id=$1`, legacyKeyID).Scan(&gotLegacyScopes); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gotLegacyScopes) != fmt.Sprint(legacyScopes) {
		t.Fatalf("existing key scopes changed from %v to %v", legacyScopes, gotLegacyScopes)
	}
	var gotLegacyHash []byte
	if err := conn.QueryRow(ctx, `SELECT secret_hash FROM project_api_keys WHERE id=$1`, legacyKeyID).Scan(&gotLegacyHash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotLegacyHash, legacyHash) {
		t.Fatal("migration changed an existing API-key secret hash")
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO project_api_keys (id,project_id,name,prefix,secret_hash,scopes)
		VALUES ($1,$2,'Apps key','stl_key_abcdefgh',$3,$4)`, newKeyID, projectID, bytesOf(1, 32), []string{"apps.read", "apps.write"}); err != nil {
		t.Fatalf("new Apps API-key scopes were rejected: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO platform_hostname_claims (label,resource_type,resource_id,project_id)
		VALUES ($1,'app',$2,$3)`, labelsBefore[firstSiteID], uuid.Must(uuid.NewV7()), projectID); err == nil {
		t.Fatal("shared label primary key accepted an App/Site hostname collision")
	}

	if _, err := conn.Exec(ctx, `DELETE FROM project_api_keys WHERE id=$1`, newKeyID); err != nil {
		t.Fatal(err)
	}
	downSQL, err := files.ReadFile("migrations/000057_apps_workload_spec.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatal(err)
	}
	for id, want := range labelsBefore {
		var label string
		if err := conn.QueryRow(ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, id).Scan(&label); err != nil {
			t.Fatal(err)
		}
		if label != want {
			t.Fatalf("down migration changed Site label from %q to %q", want, label)
		}
	}
	if err := conn.QueryRow(ctx, `SELECT scopes FROM project_api_keys WHERE id=$1`, legacyKeyID).Scan(&gotLegacyScopes); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gotLegacyScopes) != fmt.Sprint(legacyScopes) {
		t.Fatalf("down migration changed existing API-key scopes from %v to %v", legacyScopes, gotLegacyScopes)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM project_service_layouts WHERE project_id=$1 AND resource_type='site' AND resource_id=$2`, projectID, firstSiteID).Scan(&layoutCount); err != nil {
		t.Fatal(err)
	}
	if layoutCount != 1 {
		t.Fatalf("down migration changed Site layout rows: %d", layoutCount)
	}
}

func TestAppDeploymentMigrationPreservesExistingAppsIntegration(t *testing.T) {
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

	schema := fmt.Sprintf("app_deployment_migration_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000058_app_build_deployments.up.sql"); err != nil {
		t.Fatal(err)
	}

	organizationID, projectID, appID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := conn.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Existing App migration','existing-app-migration')`, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,'existing-app-project')`, projectID, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO platform_hostname_claims (label,resource_type,resource_id,project_id) VALUES ('existing-app','app',$1,$2)`, appID, projectID); err != nil {
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
	if _, err := conn.Exec(ctx, `INSERT INTO project_apps (id,project_id,name,platform_label,enabled,workload_spec,workload_spec_sha256,desired_generation,observed_generation,runtime_status) VALUES ($1,$2,'existing-app','existing-app',true,$3::jsonb,$4,7,0,'not_deployed')`, appID, projectID, canonical, digest); err != nil {
		t.Fatal(err)
	}

	upSQL, err := files.ReadFile("migrations/000058_app_build_deployments.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatal(err)
	}
	var gotSpec []byte
	var gotDigest, runtimeStatus string
	var desiredGeneration, observedGeneration, quota, used, reserved, nextVersion int64
	var selected *uuid.UUID
	if err := conn.QueryRow(ctx, `SELECT workload_spec,workload_spec_sha256,desired_generation,observed_generation,runtime_status,desired_deployment_id,artifact_quota_bytes,artifact_used_bytes,artifact_reserved_bytes,next_deployment_version FROM project_apps WHERE id=$1`, appID).Scan(&gotSpec, &gotDigest, &desiredGeneration, &observedGeneration, &runtimeStatus, &selected, &quota, &used, &reserved, &nextVersion); err != nil {
		t.Fatal(err)
	}
	decoded, err := workloadspec.Decode(gotSpec)
	if err != nil || !workloadspec.Equal(decoded, spec) || gotDigest != digest || desiredGeneration != 7 || observedGeneration != 0 || runtimeStatus != "not_deployed" {
		t.Fatalf("migration changed preexisting App desired state: spec=%s hash=%s generation=%d/%d runtime=%s", gotSpec, gotDigest, desiredGeneration, observedGeneration, runtimeStatus)
	}
	if selected != nil || quota != 5<<30 || used != 0 || reserved != 0 || nextVersion != 1 {
		t.Fatalf("migration defaults = selected %v quota %d used %d reserved %d next version %d", selected, quota, used, reserved, nextVersion)
	}
	var deploymentCount, claimCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM app_deployments WHERE project_id=$1`, projectID).Scan(&deploymentCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM platform_hostname_claims WHERE resource_type='app' AND resource_id=$1 AND label='existing-app'`, appID).Scan(&claimCount); err != nil {
		t.Fatal(err)
	}
	if deploymentCount != 0 || claimCount != 1 {
		t.Fatalf("migration synthesized deployment or changed hostname claim: deployments=%d claims=%d", deploymentCount, claimCount)
	}
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
