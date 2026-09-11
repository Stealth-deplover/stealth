package cli

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/buildinfo"
)

func TestValidatePublicURL(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "https", value: "https://console.example.test/", valid: true},
		{name: "local http", value: "http://localhost:8080", valid: true},
		{name: "credentials", value: "https://user:pass@example.test", valid: false},
		{name: "query", value: "https://example.test/?token=x", valid: false},
		{name: "relative", value: "example.test", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validatePublicURL(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("validatePublicURL(%q) error=%v, valid=%v", test.value, err, test.valid)
			}
		})
	}
}

func TestGenerateConfigGeneratesUsedStrongSecrets(t *testing.T) {
	plan := InstallPlan{Version: "v1.2.3", PublicURL: "https://console.example.test", DockerGID: 123}
	first, err := generateConfig(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateConfig(plan)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("secret generation returned identical configurations")
	}
	values, err := parseEnvContents(t, first)
	if err != nil {
		t.Fatal(err)
	}
	if values["COOKIE_SECURE"] != "true" || values["DOCKER_GID"] != "123" {
		t.Fatalf("unexpected derived config: %#v", values)
	}
	key, err := base64.StdEncoding.DecodeString(values["FUNCTIONS_SECRET_KEY"])
	if err != nil || len(key) != 32 {
		t.Fatalf("FUNCTIONS_SECRET_KEY is not a 32-byte base64 secret: %v", err)
	}
	bootstrapKey, err := base64.StdEncoding.DecodeString(values["BOOTSTRAP_CLI_KEY"])
	if err != nil || len(bootstrapKey) != 32 {
		t.Fatalf("BOOTSTRAP_CLI_KEY is not a 32-byte base64 secret: %v", err)
	}
	if strings.Contains(first, "CHANGE_ME") || strings.Contains(first, "admin@example") {
		t.Fatal("generated config contains placeholder or owner data")
	}
}

func TestPrepareInstallationPreservesExistingConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "compose.production.yaml") {
			_, _ = writer.Write([]byte("services:\n  api:\n    image: test\n"))
			return
		}
		_, _ = writer.Write([]byte("server {\n}"))
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	layout := newInstallLayout(filepath.Join(root, ".stealth"))
	app := NewApp(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	app.assetBase = server.URL
	plan := InstallPlan{Layout: layout, Version: "v1.2.3", PublicURL: "http://localhost:8080", DockerGID: 42}
	if err := app.prepareInstallation(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if mode := mustFileMode(t, layout.EnvFile); mode != 0600 {
		t.Fatalf("config mode = %o, want 600", mode)
	}
	plan.Existing = true
	if err := app.prepareInstallation(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("repair preparation replaced the existing config")
	}
}

func TestLoadExistingPlanUsesVersionFile(t *testing.T) {
	layout := writeExistingConfig(t, "v1.0.0")
	if err := os.WriteFile(layout.VersionFile, []byte("v1.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}

	app := NewApp(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	plan, err := app.loadExistingPlan(layout)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v1.0.0" || !plan.Existing {
		t.Fatalf("existing plan = %#v", plan)
	}
}

func TestLoadExistingPlanInfersVersionFromConfiguredImage(t *testing.T) {
	layout := writeExistingConfig(t, "v1.0.0")

	originalVersion := buildinfo.Version
	buildinfo.Version = "v1.1.0"
	t.Cleanup(func() { buildinfo.Version = originalVersion })

	app := NewApp(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	plan, err := app.loadExistingPlan(layout)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v1.0.0" {
		t.Fatalf("repair inferred version = %q, want v1.0.0", plan.Version)
	}
}

func TestLoadExistingPlanRejectsMalformedVersionState(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		apiImage   string
		wantDetail string
	}{
		{name: "malformed version file", version: "v1.0", apiImage: imageName("stealth-api", "v1.0.0"), wantDetail: "VERSION"},
		{name: "missing image tag", apiImage: "ghcr.io/stealth-deplover/stealth-api", wantDetail: "STEALTH_API_IMAGE"},
		{name: "invalid image tag", apiImage: imageName("stealth-api", "latest"), wantDetail: "STEALTH_API_IMAGE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := writeExistingConfig(t, "v1.0.0")
			values, err := readEnvFile(layout.EnvFile)
			if err != nil {
				t.Fatal(err)
			}
			values["STEALTH_API_IMAGE"] = test.apiImage
			if err := writePrivateFile(layout.EnvFile, formatEnvFile(values)); err != nil {
				t.Fatal(err)
			}
			if test.version != "" {
				if err := os.WriteFile(layout.VersionFile, []byte(test.version+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}

			app := NewApp(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
			if _, err := app.loadExistingPlan(layout); err == nil || !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("loadExistingPlan error = %v, want detail containing %q", err, test.wantDetail)
			}
		})
	}
}

func TestFreshInstallConfigKeepsRequestedVersion(t *testing.T) {
	config, err := generateConfig(InstallPlan{Version: "v1.1.0", PublicURL: "https://console.example.test", DockerGID: 42})
	if err != nil {
		t.Fatal(err)
	}
	values, err := parseEnvContents(t, config)
	if err != nil {
		t.Fatal(err)
	}
	if values["STEALTH_API_IMAGE"] != imageName("stealth-api", "v1.1.0") {
		t.Fatalf("fresh install API image = %q", values["STEALTH_API_IMAGE"])
	}
}

func TestPartialInstallationDetectionRequiresRecoveryPath(t *testing.T) {
	layout := newInstallLayout(filepath.Join(t.TempDir(), ".stealth"))
	if partialInstallationExists(layout) {
		t.Fatal("empty installation directory was treated as partial")
	}
	if err := os.MkdirAll(filepath.Dir(layout.ComposeFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.ComposeFile, []byte("services:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !partialInstallationExists(layout) {
		t.Fatal("Compose-only installation was not detected as partial")
	}
}

func writeExistingConfig(t *testing.T, version string) InstallLayout {
	t.Helper()
	layout := newInstallLayout(filepath.Join(t.TempDir(), ".stealth"))
	config, err := generateConfig(InstallPlan{Version: version, PublicURL: "https://console.example.test", DockerGID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(layout.EnvFile, config); err != nil {
		t.Fatal(err)
	}
	return layout
}

func parseEnvContents(t *testing.T, contents string) (map[string]string, error) {
	path := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		return nil, err
	}
	return readEnvFile(path)
}

func mustFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
