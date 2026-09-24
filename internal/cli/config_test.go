package cli

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/buildinfo"
	"github.com/Stealth-deplover/stealth/internal/installengine"
)

const testGitHubAppClientID = "Iv1.test-client-id"

func testProductionComposeAsset() string {
	return `services:
  buildkit-worker-credentials-init:
    network_mode: none
    restart: "no"
    cap_drop: [ALL]
    cap_add: [CHOWN, DAC_OVERRIDE]
    command: ["sh", "-ec", "for stale in /output/* /output/.[!.]* /output/..?*; do [ ! -e \"$$stale\" ]"]
    volumes:
      - ./state/buildkit-mtls/ca-cert.pem:/input/ca.pem:ro
      - ./state/buildkit-mtls/worker/cert.pem:/input/client-cert.pem:ro
      - ./state/buildkit-mtls/worker/key.pem:/input/client-key.pem:ro
      - buildkit_worker_credentials:/output
  buildkit-server-credentials-init:
    network_mode: none
    restart: "no"
    cap_drop: [ALL]
    cap_add: [CHOWN, DAC_OVERRIDE]
    command: ["sh", "-ec", "for stale in /output/* /output/.[!.]* /output/..?*; do [ ! -e \"$$stale\" ]"]
    volumes:
      - ./state/buildkit-mtls/ca-cert.pem:/input/ca.pem:ro
      - ./state/buildkit-mtls/server/cert.pem:/input/server-cert.pem:ro
      - ./state/buildkit-mtls/server/key.pem:/input/server-key.pem:ro
      - ./state/buildkit-mtls/health/cert.pem:/input/health-client-cert.pem:ro
      - ./state/buildkit-mtls/health/key.pem:/input/health-client-key.pem:ro
      - buildkit_server_credentials:/output
  worker:
    volumes:
      - buildkit_worker_credentials:/run/secrets/stealth-buildkit:ro
  buildkit:
    image: moby/buildkit:v0.33.0-rootless@sha256:80b15f0735e87bab7bf59ec4d695dfb4a7cfb25521cf56dc75d6f256285b63ef
    user: "1000:1000"
    read_only: true
    command: ["--addr", "tcp://0.0.0.0:1234", "--config", "/etc/buildkit/buildkitd.toml"]
    healthcheck:
      test: ["CMD", "buildctl", "--addr", "tcp://buildkit:1234", "--tlscacert", "/run/secrets/stealth-buildkit/ca.pem", "--tlscert", "/run/secrets/stealth-buildkit/health-client-cert.pem", "--tlskey", "/run/secrets/stealth-buildkit/health-client-key.pem", "debug", "workers"]
    security_opt:
      - seccomp=unconfined
      - apparmor=${APPS_BUILDKIT_APPARMOR_PROFILE:-unconfined}
      - systempaths=unconfined
    volumes:
      - buildkit_state:/home/user/.local/share/buildkit
      - buildkit_server_credentials:/run/secrets/stealth-buildkit:ro
      - ./buildkit/buildkitd.toml:/etc/buildkit/buildkitd.toml:ro
    networks: [app_build]
  ingress-control:
  traefik:
  traefik-state-init:
  cloudflare-state-init:
  otel-collector:
  telemetry-host:
  telemetry-docker-logs:
  telemetry-docker:
  telemetry-docker-proxy:
networks:
  telemetry_ingest:
  app_build:
volumes:
  buildkit_worker_credentials:
  buildkit_server_credentials:
`
}

func testBuildKitConfigAsset() string {
	return `[worker.oci]
enabled = true
rootless = true
noProcessSandbox = false
gc = true
maxUsedSpace = "10GB"
max-parallelism = 2

[frontend."dockerfile.v0"]
enabled = true

[grpc.tls]
cert = "/run/secrets/stealth-buildkit/server-cert.pem"
key = "/run/secrets/stealth-buildkit/server-key.pem"
ca = "/run/secrets/stealth-buildkit/ca.pem"
`
}

func testBuildKitAppArmorProfileAsset() string {
	return "# Managed by Stealth. Changes will be replaced by the installer.\n" +
		"abi <abi/4.0>,\ninclude <tunables/global>\n\n" +
		"profile stealth-buildkit-rootless flags=(unconfined) {\n" +
		"  # Ubuntu 24.04+ requires this permission for rootlesskit user namespaces.\n" +
		"  userns,\n}\n"
}

func isolatedBuildKitAppArmorProfilePath(t *testing.T) string {
	t.Helper()
	profileDir := filepath.Join(t.TempDir(), "apparmor.d")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(profileDir, installengine.BuildKitAppArmorProfileName)
}

func testManagedTraefikStaticAsset() string {
	return `entryPoints:
  web:
    address: :8080
    forwardedHeaders:
      trustedIPs:
        - "__STEALTH_CLOUDFLARED_TRUSTED_CIDR__"
  health:
    address: :8081
providers:
  file:
    directory: /etc/traefik/dynamic
api:
  dashboard: false
  insecure: false
ping:
  entryPoint: health
log:
  format: json
accessLog:
  format: json
  fields:
    headers:
      names:
        Authorization: drop
        Cookie: drop
`
}

func testManagedTraefikCoreAsset() string {
	asset := `http:
  middlewares:
    stealth-security-headers:
      headers:
        customResponseHeaders:
          X-Content-Type-Options: "nosniff"
          Referrer-Policy: "strict-origin-when-cross-origin"
          Permissions-Policy: "camera=(), microphone=(), geolocation=(), payment=()"
          X-Frame-Options: "DENY"
          Content-Security-Policy: "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';"
  routers:
    stealth-admin-realtime:
      entryPoints: [web]
      rule: "Host(STEALTH_BACKTICK__STEALTH_PUBLIC_HOST__STEALTH_BACKTICK) && Path(STEALTH_BACKTICK/v1/admin/realtimeSTEALTH_BACKTICK)"
      priority: 200
      middlewares: [stealth-security-headers]
      service: stealth-api
    stealth-project-realtime:
      entryPoints: [web]
      rule: "Host(STEALTH_BACKTICK__STEALTH_PUBLIC_HOST__STEALTH_BACKTICK) && PathRegexp(STEALTH_BACKTICK^/v1/projects/[0-9a-fA-F-]{36}/realtime$STEALTH_BACKTICK)"
      priority: 200
      middlewares: [stealth-security-headers]
      service: stealth-api
    stealth-api:
      entryPoints: [web]
      rule: "Host(STEALTH_BACKTICK__STEALTH_PUBLIC_HOST__STEALTH_BACKTICK) && PathPrefix(STEALTH_BACKTICK/v1/STEALTH_BACKTICK)"
      middlewares: [stealth-security-headers]
      service: stealth-api
    stealth-console:
      entryPoints: [web]
      rule: "Host(STEALTH_BACKTICK__STEALTH_PUBLIC_HOST__STEALTH_BACKTICK) && PathPrefix(STEALTH_BACKTICK/STEALTH_BACKTICK)"
      middlewares: [stealth-security-headers]
      service: stealth-console
  services:
    stealth-api:
      loadBalancer:
        passHostHeader: true
        servers:
          - url: http://api:8080
    stealth-console:
      loadBalancer:
        passHostHeader: true
        servers:
          - url: http://console:3000

`
	return strings.ReplaceAll(asset, "STEALTH_BACKTICK", "`")
}

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
	plan := InstallPlan{Version: "v1.2.3", PublicURL: "https://console.example.test", GitHubAppClientID: testGitHubAppClientID, DockerGID: 123}
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
	if values["GITHUB_APP_CLIENT_ID"] != testGitHubAppClientID {
		t.Fatalf("GITHUB_APP_CLIENT_ID = %q, want installer value", values["GITHUB_APP_CLIENT_ID"])
	}
	if values["STEALTH_TELEMETRY_INGEST_NETWORK_NAME"] != "stealth_telemetry_ingest" {
		t.Fatalf("STEALTH_TELEMETRY_INGEST_NETWORK_NAME = %q, want private ingest network", values["STEALTH_TELEMETRY_INGEST_NETWORK_NAME"])
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
			_, _ = io.WriteString(writer, testProductionComposeAsset())
			return
		}
		if strings.HasSuffix(request.URL.Path, "buildkit/buildkitd.toml") {
			_, _ = io.WriteString(writer, testBuildKitConfigAsset())
			return
		}
		if strings.HasSuffix(request.URL.Path, "buildkit/stealth-buildkit-rootless.apparmor") {
			_, _ = io.WriteString(writer, testBuildKitAppArmorProfileAsset())
			return
		}
		if strings.HasSuffix(request.URL.Path, "traefik/traefik.yaml") {
			_, _ = writer.Write([]byte(testManagedTraefikStaticAsset()))
			return
		}
		if strings.HasSuffix(request.URL.Path, "traefik/dynamic/core.yaml") {
			_, _ = writer.Write([]byte(testManagedTraefikCoreAsset()))
			return
		}
		if strings.HasSuffix(request.URL.Path, "traefik/dynamic/generated/.gitkeep") {
			_, _ = writer.Write([]byte("# Stealth route reconciler\n"))
			return
		}
		if strings.Contains(request.URL.Path, "/telemetry/") {
			marker := "receivers:\n"
			if strings.HasSuffix(request.URL.Path, "otel-collector.yaml") {
				marker = "receivers:\n  otlp:\nexporters:\n  clickhouse:\n"
			}
			switch {
			case strings.HasSuffix(request.URL.Path, "host-metrics.yaml"):
				marker = "hostmetrics:\n"
			case strings.HasSuffix(request.URL.Path, "docker-logs.yaml"):
				marker = "file_log/docker:\n"
			case strings.HasSuffix(request.URL.Path, "docker-stats.yaml"):
				marker = "docker_stats:\n"
			}
			_, _ = writer.Write([]byte(marker))
			return
		}
		_, _ = writer.Write([]byte("server {\n}"))
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	layout := newInstallLayout(filepath.Join(root, ".stealth"))
	app := NewApp(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	app.assetBase = server.URL
	app.runner = &setupRunner{}
	app.buildKitAppArmorProfilePath = isolatedBuildKitAppArmorProfilePath(t)
	plan := InstallPlan{Layout: layout, Version: "v1.2.3", PublicURL: "http://localhost:8080", GitHubAppClientID: testGitHubAppClientID, DockerGID: 42}
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
	config, err := generateConfig(InstallPlan{Version: "v1.1.0", PublicURL: "https://console.example.test", GitHubAppClientID: testGitHubAppClientID, DockerGID: 42})
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
	config, err := generateConfig(InstallPlan{Version: version, PublicURL: "https://console.example.test", GitHubAppClientID: testGitHubAppClientID, DockerGID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(layout.EnvFile, config); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestRetargetRepairPlanUsesReleasedCurrentCLIWithoutDowngrade(t *testing.T) {
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.currentVersion = func() string { return "v1.2.3" }
	plan := &InstallPlan{Version: "v1.2.2", InstalledVersion: "v1.2.2", Existing: true}
	if err := app.retargetRepairPlan(plan); err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v1.2.3" || plan.InstalledVersion != "v1.2.2" {
		t.Fatalf("retargeted repair plan = %#v", plan)
	}
	newer := &InstallPlan{Version: "v1.2.4", InstalledVersion: "v1.2.4", Existing: true}
	if err := app.retargetRepairPlan(newer); err == nil || !strings.Contains(err.Error(), "newer than this CLI") {
		t.Fatalf("downgrade repair error = %v", err)
	}
	app.currentVersion = func() string { return "dev" }
	local := &InstallPlan{Version: "v1.2.2", InstalledVersion: "v1.2.2", Existing: true}
	if err := app.retargetRepairPlan(local); err != nil || local.Version != "v1.2.2" {
		t.Fatalf("development repair plan = %#v, %v", local, err)
	}
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
