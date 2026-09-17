package setupinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

func TestBuildPlanRendersReviewedCredentialsAndKeepsExistingValues(t *testing.T) {
	root := t.TempDir()
	layout, err := installengine.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, "STEALTH_API_IMAGE=ghcr.io/stealth/api:v1.2.3\nOLD_VALUE=kept\n"); err != nil {
		t.Fatal(err)
	}

	state := setupstate.NewState()
	state.Draft.NetworkMode = "local_only"
	state.Draft.PublicURL = "https://console.example.test"
	state.Draft.DatabaseMode = "external"
	state.Draft.RedisMode = "external"
	state.Draft.StorageMode = "s3"
	state.Draft.StorageS3Endpoint = "https://s3.example.test"
	state.Draft.StorageS3Region = "us-east-1"
	state.Draft.StorageS3Bucket = "stealth"
	state.Draft.StorageS3UseSSL = true
	state.Draft.StorageS3PathStyle = true
	state.Draft.StorageS3Prefix = "artifacts"
	state.GitHub.ClientID = "Iv1.setup-client"
	state.SetSetupCredentials(setupstate.SetupCredentials{
		DatabaseURL:        "postgres://user:password@example.test/stealth",
		RedisURL:           "rediss://:password@example.test/0",
		StorageS3AccessKey: "access-key",
		StorageS3SecretKey: "secret-key",
	})

	plan, err := BuildPlan(state, root)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if plan.Version != "v1.2.3" || !plan.Existing || !plan.ExternalDatabase || !plan.ExternalRedis || plan.Cloudflare {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.InternalAPIURL != "http://127.0.0.1:18080" || plan.InternalConsoleURL != "http://127.0.0.1:13000" || plan.InternalProxyURL != "http://127.0.0.1:8080" {
		t.Fatalf("host health-check URLs = %q, %q, %q", plan.InternalAPIURL, plan.InternalConsoleURL, plan.InternalProxyURL)
	}
	values, err := installengine.ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"OLD_VALUE":             "kept",
		"SETUP_MODE":            "false",
		"DATABASE_URL":          "postgres://user:password@example.test/stealth",
		"REDIS_URL":             "rediss://:password@example.test/0",
		"STORAGE_S3_SECRET_KEY": "secret-key",
		"STORAGE_S3_ACCESS_KEY": "access-key",
		"STORAGE_S3_PREFIX":     "artifacts",
		"PROXY_HTTP_BIND":       "127.0.0.1",
		"COOKIE_SECURE":         "true",
	} {
		if values[key] != want {
			t.Fatalf("env %s = %q, want %q", key, values[key], want)
		}
	}
}

func TestBuildPlanWritesCloudflareTokenAsPrivateFile(t *testing.T) {
	root := t.TempDir()
	layout, err := installengine.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, "STEALTH_API_IMAGE=api:v9.9.9\n"); err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Draft.PublicURL = "https://console.example.test"
	state.Draft.NetworkMode = "cloudflare_tunnel"
	state.SetSecret("cloudflare_tunnel_token", "tunnel-secret")

	if _, err := BuildPlan(state, root); err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	tokenPath := filepath.Join(root, "state", "cloudflare-tunnel-token")
	contents, err := os.ReadFile(tokenPath)
	if err != nil || string(contents) != "tunnel-secret\n" {
		t.Fatalf("Cloudflare token = %q, %v", contents, err)
	}
	if mode := fileMode(t, tokenPath); mode != 0o600 {
		t.Fatalf("Cloudflare token mode = %o, want 600", mode)
	}
	values, err := installengine.ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["CLOUDFLARE_TUNNEL_TOKEN_FILE"] != "./state/cloudflare-tunnel-token" {
		t.Fatalf("token file env = %q", values["CLOUDFLARE_TUNNEL_TOKEN_FILE"])
	}
}

func TestBaseVersionFallsBackWhenImageHasNoTag(t *testing.T) {
	if got := BaseVersion(map[string]string{"STEALTH_API_IMAGE": "ghcr.io/stealth/api"}); got != "v0.0.0" {
		t.Fatalf("BaseVersion() = %q", got)
	}
	if got := BaseVersion(map[string]string{"STEALTH_API_IMAGE": "ghcr.io/stealth/api:v1.2.3"}); got != "v1.2.3" {
		t.Fatalf("BaseVersion() = %q", got)
	}
	if strings.TrimSpace(BaseVersion(nil)) == "" {
		t.Fatal("BaseVersion(nil) returned empty value")
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
