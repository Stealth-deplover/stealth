package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCloudflareConnectionPersistenceAndReconciliationIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Cloudflare PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rootPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rootPool.Close)
	schema := "cloudflare_repo_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_, _ = rootPool.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE")
	})
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	ownerID, adminID, regularID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, account := range []struct {
		id   uuid.UUID
		role string
	}{
		{ownerID, "instance_owner"}, {adminID, "instance_admin"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,'test-hash')`, account.id, account.id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,$2)`, account.id, account.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,'test-hash')`, regularID, regularID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}

	cipher, err := functionsecret.New(bytes.Repeat([]byte("c"), functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewWithDependencies(pool, Dependencies{CloudflareCipher: cipher})
	input := CloudflareConnectionInput{
		AccountID: "account-1", ConsoleZoneID: "console-zone", ConsoleHostname: "cloud.example.com",
		TunnelID: "tunnel-1", TunnelName: "stealth-prod", ConsoleRecordID: "console-record",
		APIToken: "cloudflare-api-token-never-in-plaintext",
	}

	imported, err := repo.ImportCloudflareConnectionOnce(ctx, input, "")
	if err != nil || !imported {
		t.Fatalf("first setup-state import = %v, %v", imported, err)
	}
	secondImport := input
	secondImport.APIToken = "newer-owner-token"
	imported, err = repo.ImportCloudflareConnectionOnce(ctx, secondImport, "")
	if err != nil || imported {
		t.Fatalf("repeated setup-state import = %v, %v; want no overwrite", imported, err)
	}

	var ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT api_token_ciphertext FROM cloudflare_connections WHERE id=TRUE`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(input.APIToken)) {
		t.Fatal("Cloudflare API token was stored in plaintext")
	}
	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil || string(plaintext) != input.APIToken {
		t.Fatalf("decrypt stored Cloudflare token = %q, %v", plaintext, err)
	}

	if err := repo.SetCloudflareConnectionByOwner(ctx, adminID, secondImport); err != ErrForbidden {
		t.Fatalf("Instance Admin token replacement error = %v, want forbidden", err)
	}
	if err := repo.SetCloudflareConnectionByOwner(ctx, regularID, secondImport); err != ErrForbidden {
		t.Fatalf("regular-account token replacement error = %v, want forbidden", err)
	}
	if err := repo.SetCloudflareConnectionByOwner(ctx, ownerID, secondImport); err != nil {
		t.Fatalf("Instance Owner token replacement: %v", err)
	}
	snapshot, err := repo.CloudflareReconcileSnapshot(ctx)
	if err != nil || snapshot.APIToken != secondImport.APIToken || snapshot.Status != "pending" {
		t.Fatalf("decrypted replacement snapshot = %#v, %v", snapshot, err)
	}
	var replacementCiphertext []byte
	if err := pool.QueryRow(ctx, `SELECT api_token_ciphertext FROM cloudflare_connections WHERE id=TRUE`).Scan(&replacementCiphertext); err != nil {
		t.Fatal(err)
	}
	status, err := repo.CloudflareRoutingStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{input.APIToken, secondImport.APIToken, "api_token_ciphertext", "tunnel_token"} {
		if bytes.Contains(encodedStatus, []byte(forbidden)) {
			t.Fatalf("public status contains %q: %s", forbidden, encodedStatus)
		}
	}

	firstDomain := "apps.example.net"
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", &firstDomain); err != nil {
		t.Fatal(err)
	}
	if completed, err := repo.CompleteCloudflareReconcile(ctx, domain.CloudflareRoutingUpdate{
		ExpectedWorkloadBaseDomain: &firstDomain, WorkloadZoneID: "workload-zone", WorkloadZoneName: "example.net",
		WildcardHostname: "*.apps.example.net", WildcardRecordID: "wildcard-1",
	}); err != nil || !completed {
		t.Fatalf("first reconcile completion = %v, %v", completed, err)
	}
	status, err = repo.CloudflareRoutingStatus(ctx)
	if err != nil || status.Status != "ready" || status.WorkloadHostname == nil || *status.WorkloadHostname != "*.apps.example.net" {
		t.Fatalf("ready Cloudflare status = %#v, %v", status, err)
	}

	newDomain := "deploy.example.co.uk"
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", &newDomain); err != nil {
		t.Fatal(err)
	}
	status, err = repo.CloudflareRoutingStatus(ctx)
	if err != nil || status.Status != "pending" {
		t.Fatalf("domain change status = %#v, %v; want pending", status, err)
	}
	if err := repo.RecordCloudflareReconcileFailure(ctx, "Cloudflare request timed out"); err != nil {
		t.Fatal(err)
	}
	status, err = repo.CloudflareRoutingStatus(ctx)
	if err != nil || status.Status != "error" {
		t.Fatalf("provider failure status = %#v, %v", status, err)
	}
	var observedWildcard string
	if err := pool.QueryRow(ctx, `SELECT wildcard_record_id FROM cloudflare_connections WHERE id=TRUE`).Scan(&observedWildcard); err != nil || observedWildcard != "wildcard-1" {
		t.Fatalf("provider failure replaced last-known wildcard ID %q: %v", observedWildcard, err)
	}

	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", nil); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.CompleteCloudflareReconcile(ctx, domain.CloudflareRoutingUpdate{})
	if err != nil || !completed {
		t.Fatalf("clear reconcile completion = %v, %v", completed, err)
	}
	retiring, err := repo.ListRetiringCloudflareWildcardDNS(ctx)
	if err != nil || len(retiring) != 1 || retiring[0].RecordID != "wildcard-1" || retiring[0].Hostname != "*.apps.example.net" {
		t.Fatalf("queued prior wildcard cleanup = %#v, %v", retiring, err)
	}
	var stillEncrypted []byte
	if err := pool.QueryRow(ctx, `SELECT api_token_ciphertext FROM cloudflare_connections WHERE id=TRUE`).Scan(&stillEncrypted); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stillEncrypted, replacementCiphertext) || len(stillEncrypted) == 0 {
		t.Fatal("clearing the workload domain changed or removed the Cloudflare connection")
	}

	var auditMetadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE actor_account_id=$1 AND action='admin.cloudflare.connection.update' ORDER BY created_at DESC LIMIT 1`, ownerID).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditMetadata, input.APIToken) || strings.Contains(auditMetadata, secondImport.APIToken) {
		t.Fatalf("Cloudflare token appeared in audit metadata: %s", auditMetadata)
	}

	release, acquired, err := repo.TryCloudflareReconcileLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("first advisory lock acquired=%v err=%v", acquired, err)
	}
	defer release()
	_, secondAcquired, err := repo.TryCloudflareReconcileLock(ctx)
	if err != nil || secondAcquired {
		t.Fatalf("second worker advisory lock acquired=%v err=%v; want skip", secondAcquired, err)
	}
}
