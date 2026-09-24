package cloudflareimport

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

// TestComposeSmokeLegacyHandoff is an opt-in helper invoked by the real
// Compose smoke. It creates and verifies an encrypted legacy setup snapshot
// without adding a second implementation of setup-state encryption.
func TestComposeSmokeLegacyHandoff(t *testing.T) {
	action := strings.TrimSpace(os.Getenv("STEALTH_CLOUDFLARE_SMOKE_ACTION"))
	if action == "" {
		t.Skip("production Compose smoke helper")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("STEALTH_CLOUDFLARE_SMOKE_KEY"))
	if err != nil || len(key) != functionsecret.KeySize {
		t.Fatal("smoke encryption key is invalid")
	}
	cipher, err := functionsecret.New(key)
	if err != nil {
		t.Fatal(err)
	}
	source := os.Getenv("STEALTH_CLOUDFLARE_SMOKE_SOURCE")
	artifact := os.Getenv("STEALTH_CLOUDFLARE_SMOKE_ARTIFACT")
	if source == "" || artifact == "" || !filepath.IsAbs(source) || !filepath.IsAbs(artifact) {
		t.Fatal("smoke setup-state paths must be absolute")
	}
	switch action {
	case "write":
		store, err := setupstate.NewFileStore(source, cipher)
		if err != nil {
			t.Fatal(err)
		}
		state := setupstate.NewState()
		state.Phase = setupstate.PhaseComplete
		state.Cloudflare.Mode = "api_token"
		state.Cloudflare.Connected = true
		state.Cloudflare.Binding = setupstate.CloudflareBinding{
			AccountID: "smoke-account", ZoneID: "smoke-zone", Hostname: "smoke.example.test",
			TunnelID: "smoke-tunnel", TunnelName: "stealth-smoke", RecordID: "smoke-record",
		}
		state.SetSecret("cloudflare_access_token", "smoke-cloudflare-api-token")
		state.SetSecret("github_private_key", "must-not-cross-cloudflare-handoff")
		if err := store.Save(context.Background(), state); err != nil {
			t.Fatal(err)
		}
	case "verify":
		envelope, err := Read(artifact, cipher)
		if err != nil {
			t.Fatalf("read Cloudflare import artifact: %v", err)
		}
		if envelope.State != StateConnection || envelope.AccountID != "smoke-account" || envelope.ConsoleZoneID != "smoke-zone" ||
			envelope.ConsoleHostname != "smoke.example.test" || envelope.TunnelID != "smoke-tunnel" ||
			envelope.TunnelName != "stealth-smoke" || envelope.ConsoleRecordID != "smoke-record" ||
			envelope.APIToken != "smoke-cloudflare-api-token" {
			t.Fatalf("Cloudflare smoke envelope mismatch: %#v", envelope)
		}
		ciphertext, err := os.ReadFile(artifact)
		if err != nil {
			t.Fatal(err)
		}
		plaintext, err := cipher.Decrypt(ciphertext)
		if err != nil || strings.Contains(string(plaintext), "must-not-cross-cloudflare-handoff") || strings.Contains(string(plaintext), "github_private_key") {
			t.Fatal("Cloudflare artifact contains unrelated setup data")
		}
	case "verify-absent":
		if _, err := os.Lstat(artifact); !os.IsNotExist(err) {
			t.Fatalf("missing legacy setup source produced an artifact: %v", err)
		}
	default:
		t.Fatalf("unknown production smoke action %q", action)
	}
}
