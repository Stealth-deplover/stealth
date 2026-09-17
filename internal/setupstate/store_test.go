package setupstate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
)

func testCipher(t *testing.T) *functionsecret.Cipher {
	t.Helper()
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x42}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestFileStoreEncryptsStateAndKeepsFilesPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "setup-state.enc")
	store, err := NewFileStore(path, testCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	state := NewState()
	state.SetSetupCredentials(SetupCredentials{
		DatabaseURL:        "postgres://setup-user:database-password@example.test/stealth",
		StorageS3AccessKey: "access-key",
		StorageS3SecretKey: "storage-secret",
	})
	state.Cloudflare.OAuthStateHash = "oauth-hash"
	state.SetSecret("github_private_key", "PRIVATE KEY MATERIAL")
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"database-password", "access-key", "storage-secret", "PRIVATE KEY MATERIAL", "oauth-hash"} {
		if bytes.Contains(contents, []byte(secret)) {
			t.Fatalf("encrypted state contains plaintext secret %q", secret)
		}
	}
	if mode := fileMode(t, path); mode != 0o660 {
		t.Fatalf("state file mode = %o, want 660 for cross-UID setup access", mode)
	}
	if mode := fileMode(t, filepath.Dir(path)); mode&0o077 != 0 {
		t.Fatalf("state directory mode = %o, want private", mode)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Secret("github_private_key") != "PRIVATE KEY MATERIAL" || loaded.SetupCredentials().DatabaseURL == "" {
		t.Fatalf("decrypted state lost secrets or draft: %#v", loaded)
	}
	public, err := json.Marshal(loaded.Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"database-password", "access-key", "storage-secret", "PRIVATE KEY MATERIAL", "oauth-hash", "secrets"} {
		if bytes.Contains(public, []byte(forbidden)) {
			t.Fatalf("public state contains sensitive value %q: %s", forbidden, public)
		}
	}
}

func TestFileStoreSupportsPreparedSharedStateDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(directory, "setup-state.enc")
	store, err := NewFileStore(path, testCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareShared(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), NewState()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetgid == 0 || info.Mode().Perm() != 0o770 {
		t.Fatalf("shared state directory mode = %o, want setgid 770", info.Mode())
	}
	if stateMode, lockMode := fileMode(t, path), fileMode(t, path+".lock"); stateMode != 0o660 || lockMode != 0o660 {
		t.Fatalf("shared state files have modes state=%o lock=%o, want 660", stateMode, lockMode)
	}
}

func TestInstallationRequestPhaseIsDurableAndSingleOwner(t *testing.T) {
	state := NewState()
	if InstallationLocked(state) || InstallationRequested(state) {
		t.Fatal("new setup state is already locked or requested")
	}
	if err := RequestInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseInstallRequested || state.InstallRunID != "run-1" || !InstallationLocked(state) || !InstallationRequested(state) {
		t.Fatalf("requested state = %#v", state)
	}
	if err := RequestInstallation(&state, "run-2"); err == nil {
		t.Fatal("second installation request was accepted")
	}
	if err := BeginInstallation(&state, "run-2"); err == nil {
		t.Fatal("different run claimed the installation")
	}
	if err := BeginInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseInstalling {
		t.Fatalf("begun state phase = %q, want %q", state.Phase, PhaseInstalling)
	}
	if err := BeginInstallation(&state, "run-1"); err != nil {
		t.Fatalf("idempotent begin failed: %v", err)
	}

	state.Phase = PhaseFailed
	if err := RequestInstallation(&state, "run-2"); err != nil {
		t.Fatalf("failed installation was not repairable: %v", err)
	}
	if state.Phase != PhaseInstallRequested || state.InstallRunID != "run-2" {
		t.Fatalf("repair request state = %#v", state)
	}
}

func TestInstallationPhasesRequireRunIdentifier(t *testing.T) {
	for _, phase := range []string{PhaseInstallRequested, PhaseInstalling, PhaseHandoff} {
		state := NewState()
		state.Phase = phase
		if err := ValidateState(state); err == nil || !strings.Contains(err.Error(), "run identifier") {
			t.Fatalf("ValidateState(%q) = %v, want missing run identifier error", phase, err)
		}
	}
}

func TestHostLifecycleRejectsStaleWriterAndPreservesHandoffOrder(t *testing.T) {
	state := NewState()
	if err := RequestInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := BeginInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateInstallationProgress(&state, "run-2", "Release images"); err == nil {
		t.Fatal("stale run updated installation progress")
	}
	if err := UpdateInstallationProgress(&state, "run-1", "Release images"); err != nil {
		t.Fatal(err)
	}
	if err := MarkInstallationHandoff(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseHandoff || state.Step != "Handoff" {
		t.Fatalf("handoff state = %#v", state)
	}
	if err := CompleteInstallation(&state, "run-2"); err == nil {
		t.Fatal("stale run completed installation")
	}
	if err := CompleteInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseComplete || state.SetupSessionID != "" || state.SetupCodeHash != "" {
		t.Fatalf("complete state = %#v", state)
	}
}

func TestHostPreflightProjectionIsValidatedAsDurableState(t *testing.T) {
	state := NewState()
	state.HostPreflight = []HostPreflightCheck{{Name: "Docker", Detail: "host daemon ready", OK: true, Required: true}}
	if err := ValidateState(state); err != nil {
		t.Fatal(err)
	}
	state.HostPreflight[0].Detail = strings.Repeat("x", 241)
	if err := ValidateState(state); err == nil {
		t.Fatal("oversized host preflight detail was accepted")
	}
}

func TestFileStoreUpdateIsAtomicAcrossConcurrentWriters(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "state.enc"), testCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	const writers = 24
	var group sync.WaitGroup
	group.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer group.Done()
			if _, err := store.Update(context.Background(), func(state *State) error {
				state.LastEventID++
				return nil
			}); err != nil {
				t.Errorf("Update(): %v", err)
			}
		}()
	}
	group.Wait()
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.LastEventID != writers {
		t.Fatalf("LastEventID = %d, want %d", state.LastEventID, writers)
	}
}

func TestFileStoreUpdateIsAtomicAcrossStoreInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	first, err := NewFileStore(path, testCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(path, testCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save(context.Background(), NewState()); err != nil {
		t.Fatal(err)
	}

	const writers = 24
	var group sync.WaitGroup
	group.Add(writers)
	for i := 0; i < writers; i++ {
		store := first
		if i%2 == 1 {
			store = second
		}
		go func(store *FileStore) {
			defer group.Done()
			if _, err := store.Update(context.Background(), func(state *State) error {
				// Keep the transaction open long enough for independent
				// FileStore instances to contend on the OS lock.
				time.Sleep(time.Millisecond)
				state.LastEventID++
				return nil
			}); err != nil {
				t.Errorf("Update(): %v", err)
			}
		}(store)
	}
	group.Wait()

	state, err := first.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.LastEventID != writers {
		t.Fatalf("LastEventID = %d, want %d", state.LastEventID, writers)
	}
}

func TestFileStoreMigratesLegacyDraftCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	cipher := testCipher(t)
	store, err := NewFileStore(path, cipher)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"version":1,"phase":"collecting","draft":{"database_url":"postgres://legacy-user:legacy-password@example.test/stealth","redis_url":"redis://:legacy-redis-password@example.test/0","storage_s3_access_key":"legacy-access","storage_s3_secret_key":"legacy-secret"}}`)
	ciphertext, err := cipher.Encrypt(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != stateVersion {
		t.Fatalf("migrated state version = %d, want %d", state.Version, stateVersion)
	}
	credentials := state.SetupCredentials()
	if credentials.DatabaseURL != "postgres://legacy-user:legacy-password@example.test/stealth" || credentials.RedisURL != "redis://:legacy-redis-password@example.test/0" || credentials.StorageS3AccessKey != "legacy-access" || credentials.StorageS3SecretKey != "legacy-secret" {
		t.Fatalf("migrated credentials = %#v", credentials)
	}
	if _, err := store.Update(context.Background(), func(*State) error { return nil }); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(contents)
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Version int                        `json:"version"`
		Draft   map[string]json.RawMessage `json:"draft"`
		Secrets map[string]string          `json:"secrets"`
	}
	if err := json.Unmarshal(plaintext, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Version != stateVersion || persisted.Secrets["database_url"] != credentials.DatabaseURL || persisted.Secrets["redis_url"] != credentials.RedisURL || persisted.Secrets["storage_s3_access_key"] != credentials.StorageS3AccessKey || persisted.Secrets["storage_s3_secret_key"] != credentials.StorageS3SecretKey {
		t.Fatalf("persisted migrated state = %#v", persisted)
	}
	for _, name := range []string{"database_url", "redis_url", "storage_s3_access_key", "storage_s3_secret_key"} {
		if _, ok := persisted.Draft[name]; ok {
			t.Fatalf("legacy credential %q remained in draft", name)
		}
	}
}

func TestFileStoreMigratesLegacyCloudflareBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	cipher := testCipher(t)
	store, err := NewFileStore(path, cipher)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"version":2,"phase":"collecting","draft":{"hostname":"stealth.example.test","cloudflare_account_id":"account-a","cloudflare_zone_id":"zone-a","cloudflare_tunnel_id":"tunnel-a","cloudflare_tunnel_name":"stealth-prod","cloudflare_record_id":"record-a"}}`)
	ciphertext, err := cipher.Encrypt(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := CloudflareBinding{AccountID: "account-a", ZoneID: "zone-a", Hostname: "stealth.example.test", TunnelName: "stealth-prod", TunnelID: "tunnel-a", RecordID: "record-a"}
	if state.Cloudflare.Binding != want {
		t.Fatalf("migrated Cloudflare binding = %#v, want %#v", state.Cloudflare.Binding, want)
	}
	if public := state.Public(); public.Draft.CloudflareTunnelID != "tunnel-a" || public.Draft.CloudflareRecordID != "record-a" {
		t.Fatalf("migrated public Cloudflare projection = %#v", public.Draft)
	}
}

func TestPublicProjectionOmitsRawCredentialFields(t *testing.T) {
	state := NewState()
	state.SetSetupCredentials(SetupCredentials{
		DatabaseURL:        "postgres://user:password@example.test/db",
		RedisURL:           "redis://:redis-password@example.test/0",
		StorageS3AccessKey: "access",
		StorageS3SecretKey: "secret",
	})
	state.SetSecret("cloudflare_access_token", "cf-token")
	state.GitHub.ManifestStateHash = "manifest-hash"
	state.Cloudflare.OAuthStateHash = "oauth-hash"
	public := state.Public()
	if public.Draft.DatabaseMode == "" || public.Draft.StorageMode == "" {
		t.Fatal("public projection lost non-secret setup choices")
	}
	contents, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, forbidden := range []string{"password", "redis-password", "access", "secret", "cf-token", "manifest-hash", "oauth-hash"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public projection contains %q: %s", forbidden, text)
		}
	}
}

func TestManifestStateIsRandomHashedAndExpires(t *testing.T) {
	plain, hash, expiresAt, err := NewManifestState()
	if err != nil {
		t.Fatal(err)
	}
	if plain == "" || hash == "" || !expiresAt.After(time.Now().UTC()) {
		t.Fatalf("manifest state = %q, %q, %s", plain, hash, expiresAt)
	}
	if hash != HashManifestState(plain) || hash == plain {
		t.Fatalf("manifest hash = %q, want digest of %q", hash, plain)
	}
	other, _, _, err := NewManifestState()
	if err != nil {
		t.Fatal(err)
	}
	if other == plain {
		t.Fatal("manifest state was not random")
	}
}

func TestValidationRejectsUnsafeSetupInputs(t *testing.T) {
	for _, value := range []string{"", "example.test", "https://user:pass@example.test", "https://example.test/?token=1"} {
		if _, err := ValidatePublicURL(value); err == nil {
			t.Fatalf("ValidatePublicURL(%q) accepted unsafe input", value)
		}
	}
	for _, value := range []string{"localhost", "127.0.0.1", "*.example.test", "-bad.example.test", "bad-.example.test"} {
		if _, err := ValidateHostname(value); err == nil {
			t.Fatalf("ValidateHostname(%q) accepted unsafe input", value)
		}
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
