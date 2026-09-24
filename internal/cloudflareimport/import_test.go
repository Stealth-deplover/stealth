package cloudflareimport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

func importTestCipher(t *testing.T) *functionsecret.Cipher {
	t.Helper()
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x71}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestPreparationPublishesOnlyCloudflareConnectionSecrets(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "setup-state.enc")
	output := filepath.Join(directory, ".cloudflare-import", "cloudflare-import.enc")
	cipher := importTestCipher(t)
	store, err := setupstate.NewFileStore(source, cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Phase = setupstate.PhaseComplete
	state.Cloudflare.Mode = "api_token"
	state.Cloudflare.Connected = true
	state.Cloudflare.Binding = setupstate.CloudflareBinding{
		AccountID: "account-unique", ZoneID: "zone-unique", Hostname: "cloud.example.com",
		TunnelID: "tunnel-unique", TunnelName: "stealth-prod", RecordID: "console-record-unique",
	}
	state.SetSecret("cloudflare_access_token", "SECRET-CLOUDFLARE-API")
	state.SetSecret("cloudflare_tunnel_token", "SECRET-CLOUDFLARED-TUNNEL")
	state.SetSecret("github_client_secret", "SECRET-GITHUB-CLIENT")
	state.SetSecret("github_private_key", "SECRET-GITHUB-PRIVATE-KEY")
	state.SetSecret("github_webhook_secret", "SECRET-GITHUB-WEBHOOK")
	state.SetSecret("database_url", "postgres://user:SECRET-DATABASE@example.test/stealth")
	state.SetSecret("redis_url", "redis://:SECRET-REDIS@example.test/0")
	state.SetSecret("storage_s3_access_key", "SECRET-S3-ACCESS")
	state.SetSecret("storage_s3_secret_key", "SECRET-S3")
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	completeSnapshot, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{legacySetupSnapshotName, legacySetupTempName} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(output), name), completeSnapshot, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	outcome, err := Prepare(context.Background(), source, output, cipher, nil)
	if err != nil || outcome != OutcomeConnection {
		t.Fatalf("Prepare() = %q, %v; want connection artifact", outcome, err)
	}
	artifact, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fileMode(t, output); mode.Perm() != 0o600 {
		t.Fatalf("test artifact permissions = %o, want 600", mode.Perm())
	}
	for _, forbidden := range forbiddenSetupMarkers() {
		if bytes.Contains(artifact, []byte(forbidden)) {
			t.Fatal("encrypted worker artifact contains plaintext setup material")
		}
	}
	plaintext, err := cipher.Decrypt(artifact)
	if err != nil {
		t.Fatal("worker-visible artifact did not decrypt:", err)
	}
	if !bytes.Contains(plaintext, []byte("SECRET-CLOUDFLARE-API")) {
		t.Fatal("decrypted worker artifact does not contain the Cloudflare API token")
	}
	for _, forbidden := range forbiddenSetupMarkers()[1:] {
		if bytes.Contains(plaintext, []byte(forbidden)) {
			t.Fatal("decrypted worker artifact contains unrelated setup material")
		}
	}
	envelope, err := Read(output, cipher)
	if err != nil {
		t.Fatal("Read(worker artifact):", err)
	}
	if envelope.Version != Version || envelope.State != StateConnection || envelope.AccountID != "account-unique" || envelope.ConsoleZoneID != "zone-unique" || envelope.ConsoleHostname != "cloud.example.com" || envelope.TunnelID != "tunnel-unique" || envelope.TunnelName != "stealth-prod" || envelope.ConsoleRecordID != "console-record-unique" || envelope.APIToken != "SECRET-CLOUDFLARE-API" {
		t.Fatal("worker envelope does not contain the expected Cloudflare connection")
	}
	entries, err := os.ReadDir(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(output) {
		t.Fatalf("worker-visible directory contains full setup copies or unexpected files: %#v", entries)
	}
}

func TestPublishLegacySetupSnapshotBeforeCloudflarePreparation(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "state")
	inputDir := filepath.Join(root, "cloudflare-setup-state-input")
	source := filepath.Join(sourceDir, legacySetupSnapshotName)
	output := filepath.Join(root, "worker-import", "cloudflare-import.enc")
	if err := os.Mkdir(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cipher := importTestCipher(t)
	store, err := setupstate.NewFileStore(source, cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Phase = setupstate.PhaseComplete
	state.Cloudflare.Mode = "api_token"
	state.Cloudflare.Connected = true
	state.Cloudflare.Binding = setupstate.CloudflareBinding{
		AccountID: "account-unique", ZoneID: "zone-unique", Hostname: "cloud.example.com",
		TunnelID: "tunnel-unique", TunnelName: "stealth-prod", RecordID: "console-record-unique",
	}
	state.SetSecret("cloudflare_access_token", "SECRET-CLOUDFLARE-API")
	state.SetSecret("github_private_key", "SECRET-GITHUB-PRIVATE-KEY")
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	published, err := PublishLegacySetupSnapshot(context.Background(), source, inputDir)
	if err != nil || !published {
		t.Fatalf("PublishLegacySetupSnapshot(valid) = %v, %v", published, err)
	}
	inputPath := filepath.Join(inputDir, legacySetupSnapshotName)
	if mode := fileMode(t, inputPath).Perm(); mode != 0o400 {
		t.Fatalf("handoff source mode = %o; want 400", mode)
	}
	if copied, err := os.ReadFile(inputPath); err != nil {
		t.Fatal(err)
	} else if original, err := os.ReadFile(source); err != nil || !bytes.Equal(copied, original) {
		t.Fatalf("handoff changed the encrypted setup snapshot: read error=%v", err)
	}

	outcome, err := Prepare(context.Background(), inputPath, output, cipher, nil)
	if err != nil || outcome != OutcomeConnection {
		t.Fatalf("Prepare(handoff) = %q, %v; want connection", outcome, err)
	}
	envelope, err := Read(output, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.State != StateConnection || envelope.AccountID != "account-unique" || envelope.APIToken != "SECRET-CLOUDFLARE-API" {
		t.Fatalf("narrow handoff envelope = %#v", envelope)
	}
	artifact, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plaintext, []byte("SECRET-GITHUB-PRIVATE-KEY")) || bytes.Contains(plaintext, []byte("setup-state.enc")) {
		t.Fatal("narrow import artifact contains unrelated setup state")
	}
}

func TestPublishLegacySetupSnapshotMissingAndUnsafeSources(t *testing.T) {
	t.Run("missing source clears stale handoff and remains no import", func(t *testing.T) {
		root := t.TempDir()
		sourceDir := filepath.Join(root, "state")
		if err := os.MkdirAll(sourceDir, 0o700); err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(sourceDir, legacySetupSnapshotName)
		inputDir := filepath.Join(root, "input")
		if err := os.MkdirAll(inputDir, 0o700); err != nil {
			t.Fatal(err)
		}
		stale := filepath.Join(inputDir, legacySetupSnapshotName)
		if err := os.WriteFile(stale, []byte("stale"), 0o400); err != nil {
			t.Fatal(err)
		}
		published, err := PublishLegacySetupSnapshot(context.Background(), source, inputDir)
		if err != nil || published {
			t.Fatalf("PublishLegacySetupSnapshot(missing) = %v, %v", published, err)
		}
		if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing source left stale handoff: %v", err)
		}
		output := filepath.Join(root, "output", "cloudflare-import.enc")
		outcome, err := Prepare(context.Background(), stale, output, importTestCipher(t), nil)
		if err != nil || outcome != OutcomeNoImport {
			t.Fatalf("Prepare(missing handoff) = %q, %v", outcome, err)
		}
		if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing handoff produced a Cloudflare artifact: %v", err)
		}
	})

	tests := map[string]func(string) error{
		"symlink": func(path string) error {
			return os.Symlink(filepath.Join(filepath.Dir(path), "outside.enc"), path)
		},
		"directory": func(path string) error { return os.Mkdir(path, 0o700) },
		"oversized": func(path string) error { return os.WriteFile(path, make([]byte, maxSetupSnapshotBytes+1), 0o600) },
	}
	for name, create := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sourceDir := filepath.Join(root, "state")
			inputDir := filepath.Join(root, "input")
			if err := os.Mkdir(sourceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(inputDir, 0o700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(sourceDir, legacySetupSnapshotName)
			if err := create(source); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(inputDir, legacySetupSnapshotName), []byte("stale"), 0o400); err != nil {
				t.Fatal(err)
			}
			if _, err := PublishLegacySetupSnapshot(context.Background(), source, inputDir); err == nil {
				t.Fatal("unsafe legacy setup source was accepted")
			}
			if _, err := os.Lstat(filepath.Join(inputDir, legacySetupSnapshotName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsafe source left a stale handoff file: %v", err)
			}
		})
	}
}

func TestMalformedEncryptedSetupSnapshotRemainsSafeNoImport(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "state")
	inputDir := filepath.Join(root, "input")
	output := filepath.Join(root, "output", "cloudflare-import.enc")
	if err := os.Mkdir(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(inputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, legacySetupSnapshotName)
	if err := os.WriteFile(source, []byte("not-an-encrypted-setup-snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	published, err := PublishLegacySetupSnapshot(context.Background(), source, inputDir)
	if err != nil || !published {
		t.Fatalf("bounded opaque snapshot handoff = %v, %v", published, err)
	}
	if outcome, err := Prepare(context.Background(), filepath.Join(inputDir, legacySetupSnapshotName), output, importTestCipher(t), nil); err == nil || outcome != "" {
		t.Fatalf("malformed encrypted snapshot Prepare() = %q, %v; expected safe recovery failure", outcome, err)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malformed setup snapshot produced a Cloudflare artifact: %v", err)
	}
}

func TestPreparationFailsClosedOnUnexpectedWorkerVisibleFile(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "setup-state.enc")
	outputDirectory := filepath.Join(directory, ".cloudflare-import")
	output := filepath.Join(outputDirectory, "cloudflare-import.enc")
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDirectory, "unexpected.enc"), []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Prepare(context.Background(), source, output, importTestCipher(t), nil)
	if !errors.Is(err, ErrWorkerBoundaryUnsafe) {
		t.Fatalf("Prepare with unexpected worker-visible file = %v, want unsafe boundary", err)
	}
}

func TestPreparationMissingOrNonCloudflareStateProducesNoArtifact(t *testing.T) {
	directory := t.TempDir()
	cipher := importTestCipher(t)
	output := filepath.Join(directory, "out", "cloudflare-import.enc")
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("stale narrow ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "missing", "setup-state.enc")
	outcome, err := Prepare(context.Background(), missing, output, cipher, nil)
	if err != nil || outcome != OutcomeNoImport {
		t.Fatalf("Prepare(missing) = %q, %v; want no-op", outcome, err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("missing source left derived artifact in place: stat err = %v", err)
	}

	source := filepath.Join(directory, "non-cloudflare.enc")
	store, err := setupstate.NewFileStore(source, cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Draft.NetworkMode = "reverse_proxy"
	state.Draft.Hostname = "cloud.example.com"
	state.SetSecret("database_url", "postgres://user:SECRET-DATABASE@example.test/stealth")
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	outcome, err = Prepare(context.Background(), source, output, cipher, nil)
	if err != nil || outcome != OutcomeNoImport {
		t.Fatalf("Prepare(non-Cloudflare) = %q, %v; want no-op", outcome, err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("non-Cloudflare setup produced an artifact: stat err = %v", err)
	}
}

func TestIncompleteCloudflareStateCreatesReconnectMarkerWithoutCredential(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "setup-state.enc")
	output := filepath.Join(directory, "out", "cloudflare-import.enc")
	cipher := importTestCipher(t)
	store, err := setupstate.NewFileStore(source, cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Cloudflare.Mode = "api_token"
	state.SetSecret("cloudflare_access_token", "SECRET-CLOUDFLARE-API")
	state.SetSecret("cloudflare_tunnel_token", "SECRET-CLOUDFLARED-TUNNEL")
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	outcome, err := Prepare(context.Background(), source, output, cipher, nil)
	if err != nil || outcome != OutcomeReconnectRequired {
		t.Fatalf("Prepare(incomplete) = %q, %v; want reconnect-required artifact", outcome, err)
	}
	artifact, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range append(forbiddenSetupMarkers(), "SECRET-CLOUDFLARE-API") {
		if bytes.Contains(plaintext, []byte(marker)) {
			t.Fatal("reconnect marker contains setup credential material")
		}
	}
	envelope, err := Read(output, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.State != StateReconnectRequired || envelope.APIToken != "" || envelope.TunnelID != "" {
		t.Fatalf("incomplete import envelope = %#v", envelope)
	}
}

func TestReadRejectsUnknownFieldsAndDecryptFailuresWithoutLeakingPayload(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "cloudflare-import.enc")
	cipher := importTestCipher(t)
	unknown, err := cipher.Encrypt([]byte(`{"version":1,"state":"reconnect_required","unrelated":"SECRET-GITHUB-CLIENT"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, cipher); err == nil || strings.Contains(err.Error(), "SECRET-GITHUB-CLIENT") {
		t.Fatal("unknown artifact field was accepted or leaked into an error")
	}
	if err := os.WriteFile(path, []byte("SECRET-CLOUDFLARE-API"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, cipher); err == nil || strings.Contains(err.Error(), "SECRET-CLOUDFLARE-API") {
		t.Fatal("decrypt failure was accepted or leaked into an error")
	}
}

func forbiddenSetupMarkers() []string {
	return []string{
		"SECRET-CLOUDFLARE-API",
		"SECRET-CLOUDFLARED-TUNNEL",
		"SECRET-GITHUB-CLIENT",
		"SECRET-GITHUB-PRIVATE-KEY",
		"SECRET-GITHUB-WEBHOOK",
		"SECRET-DATABASE",
		"SECRET-REDIS",
		"SECRET-S3-ACCESS",
		"SECRET-S3",
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
