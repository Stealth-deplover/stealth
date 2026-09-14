package setupconfig

import (
	"errors"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

func TestApplyKeepsStoredCredentialsWhenRequestOmitsSecrets(t *testing.T) {
	state := setupstate.NewState()
	state.SetSetupCredentials(setupstate.SetupCredentials{
		DatabaseURL:        "postgres://user:password@example.test/stealth",
		RedisURL:           "rediss://:password@example.test/0",
		StorageS3AccessKey: "access-key",
		StorageS3SecretKey: "secret-key",
	})

	err := Apply(&state, Request{
		InstanceName:    "  Production  ",
		PublicURL:       "https://console.example.test",
		NetworkMode:     "public_ip",
		DatabaseMode:    "external",
		RedisMode:       "external",
		StorageMode:     "local",
		StorageS3UseSSL: boolPointer(false),
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if state.Draft.InstanceName != "Production" || state.Draft.NetworkMode != "public_ip" {
		t.Fatalf("normalized draft = %#v", state.Draft)
	}
	credentials := state.SetupCredentials()
	if credentials.DatabaseURL == "" || credentials.RedisURL == "" || credentials.StorageS3AccessKey == "" || credentials.StorageS3SecretKey == "" {
		t.Fatalf("stored credentials were lost: %#v", credentials)
	}
}

func TestApplyInvalidatesOnlyChangedDependencyChecks(t *testing.T) {
	state := setupstate.NewState()
	state.Draft.DatabaseMode = "external"
	state.Draft.RedisMode = "external"
	state.Draft.DatabaseTested = true
	state.Draft.RedisTested = true
	state.SetSetupCredentials(setupstate.SetupCredentials{
		DatabaseURL: "postgres://old:password@example.test/stealth",
		RedisURL:    "redis://:password@example.test/0",
	})

	err := Apply(&state, Request{
		PublicURL:    "http://localhost:8081",
		NetworkMode:  "local_only",
		DatabaseMode: "external",
		DatabaseURL:  "postgres://new:password@example.test/stealth",
		RedisMode:    "external",
		StorageMode:  "local",
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if state.Draft.DatabaseTested {
		t.Fatal("database check remained tested after URL change")
	}
	if !state.Draft.RedisTested {
		t.Fatal("Redis check was invalidated without a Redis change")
	}
}

func TestApplyRejectsCompletedState(t *testing.T) {
	state := setupstate.NewState()
	state.Phase = setupstate.PhaseComplete
	if err := Apply(&state, Request{}); err == nil || !strings.Contains(err.Error(), "already in progress or complete") {
		t.Fatalf("completed state error = %v", err)
	}
}

func TestApplyRejectsCloudflareHostnameChangeAfterBinding(t *testing.T) {
	state := setupstate.NewState()
	state.Draft.PublicURL = "https://stealth.old.example.test"
	state.Draft.NetworkMode = "cloudflare_tunnel"
	state.Draft.Hostname = "stealth.old.example.test"
	state.Cloudflare.Binding = setupstate.CloudflareBinding{
		AccountID: "account-a", ZoneID: "zone-a", Hostname: "stealth.old.example.test",
		TunnelName: "stealth-prod", TunnelID: "tunnel-a", RecordID: "record-a",
	}

	err := Apply(&state, Request{
		InstanceName: "Stealth",
		PublicURL:    "https://stealth.new.example.test",
		NetworkMode:  "cloudflare_tunnel",
		Hostname:     "stealth.new.example.test",
		DatabaseMode: "bundled",
		RedisMode:    "bundled",
		StorageMode:  "local",
	})
	var bindingConflict *setupstate.CloudflareBindingConflict
	if !errors.As(err, &bindingConflict) || !strings.Contains(err.Error(), "stealth.old.example.test") {
		t.Fatalf("hostname mutation error = %v, want binding conflict for old hostname", err)
	}
	if state.Draft.Hostname != "stealth.old.example.test" || state.Cloudflare.Binding.TunnelID != "tunnel-a" {
		t.Fatalf("rejected mutation changed state: draft=%#v binding=%#v", state.Draft, state.Cloudflare.Binding)
	}
}

func TestValidateInstallableCloudflareSetupRequiresVerifiedAPIToken(t *testing.T) {
	state := setupstate.NewState()
	state.GitHub.Connected = true
	state.GitHub.ClientID = "Iv1.setup-client"
	state.Draft.PublicURL = "https://console.example.test"
	state.Draft.NetworkMode = "cloudflare_tunnel"
	state.Draft.Hostname = "console.example.test"
	state.Cloudflare.Binding = setupstate.CloudflareBinding{
		AccountID: "account-1", ZoneID: "zone-1", Hostname: "console.example.test",
		TunnelName: "stealth-test", TunnelID: "tunnel-1", RecordID: "record-1",
	}
	state.SetSecret("cloudflare_access_token", "api-token")
	state.SetSecret("cloudflare_tunnel_token", "tunnel-token")

	if err := ValidateInstallableSetup(state); err == nil || !strings.Contains(err.Error(), "scoped Cloudflare API token") {
		t.Fatalf("unverified Cloudflare setup error = %v", err)
	}
	state.Cloudflare.Mode = "api_token"
	state.Cloudflare.Connected = true
	state.Cloudflare.TokenValid = true
	if err := ValidateInstallableSetup(state); err != nil {
		t.Fatalf("verified Cloudflare setup error = %v", err)
	}
}

func TestValidateInstallableSetupRejectsStaleCloudflareBinding(t *testing.T) {
	state := setupstate.NewState()
	state.GitHub.Connected = true
	state.GitHub.ClientID = "Iv1.setup-client"
	state.Draft.PublicURL = "https://stealth.new.example.test"
	state.Draft.NetworkMode = "cloudflare_tunnel"
	state.Draft.Hostname = "stealth.new.example.test"
	state.Cloudflare.Mode = "api_token"
	state.Cloudflare.Connected = true
	state.Cloudflare.TokenValid = true
	state.Cloudflare.Binding = setupstate.CloudflareBinding{
		AccountID: "account-a", ZoneID: "zone-a", Hostname: "stealth.old.example.test",
		TunnelName: "stealth-prod", TunnelID: "tunnel-a", RecordID: "record-a",
	}
	state.SetSecret("cloudflare_access_token", "api-token")
	state.SetSecret("cloudflare_tunnel_token", "tunnel-token")

	if err := ValidateInstallableSetup(state); err == nil || !strings.Contains(err.Error(), "stealth.old.example.test") {
		t.Fatalf("stale Cloudflare binding error = %v", err)
	}
}

func TestCredentialValidators(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
		check func(string) bool
	}{
		{name: "postgres", value: "postgresql://user:password@example.test/stealth", valid: true, check: ValidDatabaseURL},
		{name: "wrong database scheme", value: "mysql://user:password@example.test/stealth", valid: false, check: ValidDatabaseURL},
		{name: "redis", value: "rediss://:password@example.test/0", valid: true, check: ValidRedisURL},
		{name: "wrong redis scheme", value: "http://example.test", valid: false, check: ValidRedisURL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.check(test.value); got != test.valid {
				t.Fatalf("validator(%q) = %v, want %v", test.value, got, test.valid)
			}
		})
	}
	if !ValidS3Settings("https://s3.example.test", "us-east-1", "stealth", "access", "secret") {
		t.Fatal("valid S3 settings were rejected")
	}
	if ValidS3Settings("https://s3.example.test/path", "us-east-1", "stealth", "access", "secret") {
		t.Fatal("S3 endpoint path was accepted")
	}
}

func boolPointer(value bool) *bool {
	return &value
}
