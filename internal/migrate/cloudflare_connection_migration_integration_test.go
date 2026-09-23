package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCloudflareConnectionMigrationOnPopulatedNonCloudflareInstanceIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Cloudflare migration integration tests")
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
	schema := "cloudflare_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsBefore(ctx, conn, files, "000054_cloudflare_connections.up.sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain='apps.example.net' WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	up, err := files.ReadFile("migrations/000054_cloudflare_connections.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	for _, migrationName := range []string{"000055_cloudflare_edge_tls.up.sql", "000056_cloudflare_console_origin.up.sql"} {
		migration, err := files.ReadFile("migrations/" + migrationName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply populated upgrade migration %s: %v", migrationName, err)
		}
	}
	var status string
	var tokenCiphertext []byte
	if err := conn.QueryRow(ctx, `SELECT status,api_token_ciphertext FROM cloudflare_connections WHERE id=TRUE`).Scan(&status, &tokenCiphertext); err != nil {
		t.Fatal(err)
	}
	if status != "unconfigured" || tokenCiphertext != nil {
		t.Fatalf("migration fabricated provider state: status=%q ciphertext=%x", status, tokenCiphertext)
	}
	var desired, observed, originStatus string
	if err := conn.QueryRow(ctx, `SELECT console_origin_desired,console_origin_observed,console_origin_status FROM cloudflare_connections WHERE id=TRUE`).Scan(&desired, &observed, &originStatus); err != nil {
		t.Fatal(err)
	}
	if desired != "proxy" || observed != "unknown" || originStatus != "pending" {
		t.Fatalf("migration origin state = desired:%q observed:%q status:%q; want safe proxy default", desired, observed, originStatus)
	}
	var workloadDomain string
	if err := conn.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&workloadDomain); err != nil || workloadDomain != "apps.example.net" {
		t.Fatalf("populated workload domain after migration=%q err=%v", workloadDomain, err)
	}
	for _, migrationName := range []string{"000056_cloudflare_console_origin.down.sql", "000055_cloudflare_edge_tls.down.sql", "000054_cloudflare_connections.down.sql"} {
		down, err := files.ReadFile("migrations/" + migrationName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(down)); err != nil {
			t.Fatalf("apply down migration %s: %v", migrationName, err)
		}
	}
	var remaining int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name IN ('cloudflare_connections','cloudflare_retiring_wildcard_dns')`, schema).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal(fmt.Sprintf("down migration left %d Cloudflare tables", remaining))
	}
}
