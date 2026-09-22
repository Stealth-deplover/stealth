package migrate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlatformSiteHostnameMigrationBackfillsStableGlobalLabelsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	schema := fmt.Sprintf("platform_site_hostnames_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000053_platform_site_hostnames.up.sql"); err != nil {
		t.Fatal(err)
	}

	organizationID := "018f0d5e-7c19-7abc-8d1e-123456789001"
	projectOneID := "018f0d5e-7c19-7abc-8d1e-123456789002"
	projectTwoID := "018f0d5e-7c19-7abc-8d1e-123456789003"
	if _, err := conn.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Platform migration test','platform-migration-test')`, organizationID); err != nil {
		t.Fatal(err)
	}
	for _, project := range []struct {
		id   string
		name string
	}{
		{id: projectOneID, name: "first"},
		{id: projectTwoID, name: "second"},
	} {
		if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3)`, project.id, organizationID, project.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, site := range []struct {
		id        string
		projectID string
		name      string
		createdAt string
	}{
		{id: "018f0d5e-7c19-7abc-8d1e-123456789011", projectID: projectOneID, name: "portfolio", createdAt: "2026-01-01T00:00:00Z"},
		{id: "018f0d5e-7c19-7abc-8d1e-123456789012", projectID: projectTwoID, name: "portfolio", createdAt: "2026-01-02T00:00:00Z"},
		{id: "018f0d5e-7c19-7abc-8d1e-123456789013", projectID: projectOneID, name: "api", createdAt: "2026-01-03T00:00:00Z"},
		{id: "018f0d5e-7c19-7abc-8d1e-123456789014", projectID: projectTwoID, name: "a-site-", createdAt: "2026-01-04T00:00:00Z"},
	} {
		if _, err := conn.Exec(ctx, `INSERT INTO project_sites (id,project_id,name,created_at,updated_at) VALUES ($1,$2,$3,$4,$4)`, site.id, site.projectID, site.name, site.createdAt); err != nil {
			t.Fatal(err)
		}
	}

	migrationSQL, err := files.ReadFile("migrations/000053_platform_site_hostnames.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(migrationSQL)); err != nil {
		t.Fatal(err)
	}

	var count, distinct int
	if err := conn.QueryRow(ctx, `SELECT count(*),count(DISTINCT platform_label) FROM project_sites`).Scan(&count, &distinct); err != nil {
		t.Fatal(err)
	}
	if count != 4 || distinct != count {
		t.Fatalf("backfilled platform labels count=%d distinct=%d", count, distinct)
	}
	var firstLabel, secondLabel, reservedLabel, trailingLabel string
	for query, target := range map[string]*string{
		`SELECT platform_label FROM project_sites WHERE id='018f0d5e-7c19-7abc-8d1e-123456789011'`: &firstLabel,
		`SELECT platform_label FROM project_sites WHERE id='018f0d5e-7c19-7abc-8d1e-123456789012'`: &secondLabel,
		`SELECT platform_label FROM project_sites WHERE id='018f0d5e-7c19-7abc-8d1e-123456789013'`: &reservedLabel,
		`SELECT platform_label FROM project_sites WHERE id='018f0d5e-7c19-7abc-8d1e-123456789014'`: &trailingLabel,
	} {
		if err := conn.QueryRow(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if firstLabel != "portfolio" || secondLabel != "portfolio-018f0d5e7c197abc8d1e123456789012" || reservedLabel != "api-018f0d5e7c197abc8d1e123456789013" || trailingLabel != "a-site-018f0d5e7c197abc8d1e123456789014" {
		t.Fatalf("unexpected backfill labels: first=%q second=%q reserved=%q trailing=%q", firstLabel, secondLabel, reservedLabel, trailingLabel)
	}
	if len(reservedLabel) > 63 || len(trailingLabel) > 63 {
		t.Fatalf("backfill labels exceed DNS label limit: %q %q", reservedLabel, trailingLabel)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO project_sites (id,project_id,name,platform_label) VALUES ('018f0d5e-7c19-7abc-8d1e-123456789021',$1,'other','portfolio')`, projectOneID); err == nil {
		t.Fatal("global platform label uniqueness constraint accepted a duplicate")
	}
}
