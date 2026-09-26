package migrate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appsecret"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAppEnvironmentValueSizeMigrationBackfillsAndRollsBackIntegration(t *testing.T) {
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

	for _, populated := range []bool{false, true} {
		name := "clean"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			schema := fmt.Sprintf("app_env_value_size_%s_%d", name, time.Now().UnixNano())
			if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
			if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
				t.Fatal(err)
			}
			if err := applyMigrationsBefore(ctx, conn, files, "000064_app_environment_value_size.up.sql"); err != nil {
				t.Fatal(err)
			}

			var appID, projectID uuid.UUID
			var configuredCiphertext []byte
			if populated {
				appID, projectID = insertAppEnvironmentMigrationFixture(t, ctx, conn)
				cipher, err := appsecret.New(bytes.Repeat([]byte{0x4b}, appsecret.KeySize))
				if err != nil {
					t.Fatal(err)
				}
				valueID := uuid.Must(uuid.NewV7())
				configuredCiphertext, err = cipher.Encrypt(projectID, appID, valueID, []byte("migration keeps this ciphertext exact"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.Exec(ctx, `INSERT INTO app_environment_variables (id,app_id,project_id,key,is_secret,value_ciphertext) VALUES ($1,$2,$3,'TOKEN',true,$4)`, valueID, appID, projectID, configuredCiphertext); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.Exec(ctx, `INSERT INTO app_environment_variables (id,app_id,project_id,key) VALUES ($1,$2,$3,'EMPTY')`, uuid.Must(uuid.NewV7()), appID, projectID); err != nil {
					t.Fatal(err)
				}
			}

			upSQL, err := files.ReadFile("migrations/000064_app_environment_value_size.up.sql")
			if err != nil {
				t.Fatal(err)
			}
			downSQL, err := files.ReadFile("migrations/000064_app_environment_value_size.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
				t.Fatalf("apply App environment value-size migration: %v", err)
			}
			var nullable, columnDefault string
			if err := conn.QueryRow(ctx, `SELECT is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='app_environment_variables' AND column_name='value_plaintext_bytes'`).Scan(&nullable, &columnDefault); err != nil {
				t.Fatal(err)
			}
			if nullable != "NO" || columnDefault != "0" {
				t.Fatalf("value size column nullable=%q default=%q, want NO/0", nullable, columnDefault)
			}
			if populated {
				var plaintextBytes int
				var afterCiphertext []byte
				if err := conn.QueryRow(ctx, `SELECT value_plaintext_bytes,value_ciphertext FROM app_environment_variables WHERE key='TOKEN'`).Scan(&plaintextBytes, &afterCiphertext); err != nil {
					t.Fatal(err)
				}
				if plaintextBytes != len("migration keeps this ciphertext exact") || !bytes.Equal(afterCiphertext, configuredCiphertext) {
					t.Fatalf("backfill bytes=%d ciphertextPreserved=%t", plaintextBytes, bytes.Equal(afterCiphertext, configuredCiphertext))
				}
				var emptyBytes int
				if err := conn.QueryRow(ctx, `SELECT value_plaintext_bytes FROM app_environment_variables WHERE key='EMPTY'`).Scan(&emptyBytes); err != nil || emptyBytes != 0 {
					t.Fatalf("metadata-only variable size=%d err=%v, want zero", emptyBytes, err)
				}
			}
			var constraints int
			if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema() AND r.relname='app_environment_variables' AND c.conname='app_environment_variables_plaintext_size_consistent'`).Scan(&constraints); err != nil || constraints != 1 {
				t.Fatalf("plaintext-size constraint count=%d err=%v", constraints, err)
			}
			if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
				t.Fatalf("rollback App environment value-size migration: %v", err)
			}
			var remaining int
			if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='app_environment_variables' AND column_name='value_plaintext_bytes'`).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("rollback left value size column count=%d err=%v", remaining, err)
			}
			if populated {
				var afterCiphertext []byte
				if err := conn.QueryRow(ctx, `SELECT value_ciphertext FROM app_environment_variables WHERE key='TOKEN'`).Scan(&afterCiphertext); err != nil || !bytes.Equal(afterCiphertext, configuredCiphertext) {
					t.Fatalf("rollback changed existing ciphertext: equal=%t err=%v", bytes.Equal(afterCiphertext, configuredCiphertext), err)
				}
			}
		})
	}
}

func insertAppEnvironmentMigrationFixture(t *testing.T, ctx context.Context, conn *pgxpool.Conn) (uuid.UUID, uuid.UUID) {
	t.Helper()
	organizationID, projectID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	appID := uuid.Must(uuid.NewV7())
	name := "app-env-size-" + appID.String()[:8]
	if _, err := conn.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)`, organizationID, name, name); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3)`, projectID, organizationID, name); err != nil {
		t.Fatal(err)
	}
	spec, err := workloadspec.MarshalCanonical(workloadspec.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO project_apps (id,project_id,name,platform_label,enabled,workload_spec,workload_spec_sha256,desired_generation,observed_generation,runtime_status) VALUES ($1,$2,'migration-app',$3,true,$4::jsonb,repeat('a',64),1,0,'not_deployed')`, appID, projectID, name, spec); err != nil {
		t.Fatal(err)
	}
	return appID, projectID
}
