package installengine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/buildkitpki"
)

type recordedCommand struct {
	name  string
	args  []string
	stdin []byte
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []recordedCommand
	err   error
}

func (r *fakeRunner) Run(_ context.Context, _ string, _, _ io.Writer, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	return r.err
}

func (r *fakeRunner) RunInput(_ context.Context, _ string, stdin io.Reader, _, _ io.Writer, name string, args ...string) error {
	contents, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...), stdin: contents})
	return r.err
}

func (r *fakeRunner) Output(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	return nil, r.err
}

func (r *fakeRunner) snapshot() []recordedCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]recordedCommand, len(r.calls))
	copy(result, r.calls)
	return result
}

func writeEngineFixture(t *testing.T, setup bool) Layout {
	t.Helper()
	layout, err := NewLayout(filepath.Join(t.TempDir(), "stealth"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(layout.ProxyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(layout.EnvFile, "PUBLIC_APP_URL=http://127.0.0.1:8080\nAPPS_BUILDKIT_APPARMOR_PROFILE=unconfined\n"); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.ComposeFile, []byte("services:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if setup {
		if err := WriteAtomic(layout.SetupComposeFile, []byte("services:\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteAtomic(layout.ProxyFile, []byte("server {\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"otel-collector.yaml": "receivers:\n",
		"host-metrics.yaml":   "hostmetrics:\n",
		"docker-logs.yaml":    "file_log/docker:\n",
		"docker-stats.yaml":   "docker_stats:\n",
	} {
		if err := WriteAtomic(filepath.Join(layout.TelemetryDir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for path, contents := range map[string]string{
		filepath.Join(layout.Root, "buildkit", "buildkitd.toml"):                     testBuildKitConfigAsset(),
		filepath.Join(layout.Root, "buildkit", "stealth-buildkit-rootless.apparmor"): testBuildKitAppArmorProfileAsset(),
		layout.TraefikStatic: testTraefikStaticAsset(),
		layout.TraefikCore:   strings.ReplaceAll(testTraefikCoreAsset(), "__STEALTH_PUBLIC_HOST__", "127.0.0.1"),
		filepath.Join(layout.TraefikGenerated, ".gitkeep"): "# Stealth route reconciler\n",
	} {
		if err := WriteAtomic(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return layout
}

func testProductionComposeAsset() string {
	return "services:\n" +
		"  buildkit-worker-credentials-init:\n    network_mode: none\n    restart: \"no\"\n    cap_drop: [ALL]\n    cap_add: [CHOWN, DAC_OVERRIDE]\n    command: [\"sh\", \"-ec\", \"for stale in /output/* /output/.[!.]* /output/..?*; do [ ! -e \\\"$$stale\\\" ]\"]\n    volumes:\n      - ./state/buildkit-mtls/ca-cert.pem:/input/ca.pem:ro\n      - ./state/buildkit-mtls/worker/cert.pem:/input/client-cert.pem:ro\n      - ./state/buildkit-mtls/worker/key.pem:/input/client-key.pem:ro\n      - buildkit_worker_credentials:/output\n" +
		"  buildkit-server-credentials-init:\n    network_mode: none\n    restart: \"no\"\n    cap_drop: [ALL]\n    cap_add: [CHOWN, DAC_OVERRIDE]\n    command: [\"sh\", \"-ec\", \"for stale in /output/* /output/.[!.]* /output/..?*; do [ ! -e \\\"$$stale\\\" ]\"]\n    volumes:\n      - ./state/buildkit-mtls/ca-cert.pem:/input/ca.pem:ro\n      - ./state/buildkit-mtls/server/cert.pem:/input/server-cert.pem:ro\n      - ./state/buildkit-mtls/server/key.pem:/input/server-key.pem:ro\n      - ./state/buildkit-mtls/health/cert.pem:/input/health-client-cert.pem:ro\n      - ./state/buildkit-mtls/health/key.pem:/input/health-client-key.pem:ro\n      - buildkit_server_credentials:/output\n" +
		"  worker:\n    volumes:\n      - buildkit_worker_credentials:/run/secrets/stealth-buildkit:ro\n" +
		"  buildkit:\n    image: " + defaultBuildKitImage + "\n    user: \"1000:1000\"\n    read_only: true\n    command: [\"--addr\", \"tcp://0.0.0.0:1234\", \"--config\", \"/etc/buildkit/buildkitd.toml\"]\n    healthcheck:\n      test: [\"CMD\", \"buildctl\", \"--addr\", \"tcp://buildkit:1234\", \"--tlscacert\", \"/run/secrets/stealth-buildkit/ca.pem\", \"--tlscert\", \"/run/secrets/stealth-buildkit/health-client-cert.pem\", \"--tlskey\", \"/run/secrets/stealth-buildkit/health-client-key.pem\", \"debug\", \"workers\"]\n    security_opt:\n      - seccomp=unconfined\n      - apparmor=${APPS_BUILDKIT_APPARMOR_PROFILE:-unconfined}\n      - systempaths=unconfined\n    volumes:\n      - buildkit_state:/home/user/.local/share/buildkit\n      - buildkit_server_credentials:/run/secrets/stealth-buildkit:ro\n      - ./buildkit/buildkitd.toml:/etc/buildkit/buildkitd.toml:ro\n    networks: [app_build]\n" +
		"  ingress-control:\n  traefik:\n  traefik-state-init:\n  cloudflare-state-init:\n  otel-collector:\n  telemetry-host:\n  telemetry-docker-logs:\n  telemetry-docker:\n  telemetry-docker-proxy:\nnetworks:\n  telemetry_ingest:\n  app_build:\nvolumes:\n  buildkit_worker_credentials:\n  buildkit_server_credentials:\n"
}

func testBuildKitConfigAsset() string {
	return "[worker.oci]\nrootless = true\nnoProcessSandbox = false\ngc = true\nreservedSpace = \"1GB\"\nmaxUsedSpace = \"10GB\"\nminFreeSpace = \"5GB\"\nmax-parallelism = 2\n\n[frontend.\"dockerfile.v0\"]\nenabled = true\n\n[grpc.tls]\ncert = \"/run/secrets/stealth-buildkit/server-cert.pem\"\nkey = \"/run/secrets/stealth-buildkit/server-key.pem\"\nca = \"/run/secrets/stealth-buildkit/ca.pem\"\n"
}

func testBuildKitAppArmorProfileAsset() string {
	return buildKitAppArmorProfile
}

func expectedAppArmorParserCommand(args ...string) (string, []string) {
	if os.Geteuid() != 0 {
		return "sudo", append([]string{"apparmor_parser"}, args...)
	}
	return "apparmor_parser", args
}

func testMainCollectorAsset() string {
	return "receivers:\n  otlp:\nexporters:\n  clickhouse:\n"
}

func testTraefikStaticAsset() string {
	return "entryPoints:\n  web:\n    address: \":8080\"\n    forwardedHeaders:\n      trustedIPs:\n        - \"__STEALTH_CLOUDFLARED_TRUSTED_CIDR__\"\n  health:\n    address: \":8081\"\nproviders:\n  file:\n    directory: /etc/traefik/dynamic\napi:\n  dashboard: false\n  insecure: false\nping:\n  entryPoint: health\nlog:\n  format: json\naccessLog:\n  format: json\n  fields:\n    headers:\n      names:\n        Authorization: drop\n        Cookie: drop\n"
}

func testTraefikCoreAssetBase() string {
	return "http:\n  middlewares:\n    stealth-security-headers:\n      headers:\n        customResponseHeaders:\n          X-Content-Type-Options: \"nosniff\"\n          Referrer-Policy: \"strict-origin-when-cross-origin\"\n          Permissions-Policy: \"camera=(), microphone=(), geolocation=(), payment=()\"\n          X-Frame-Options: \"DENY\"\n          Content-Security-Policy: \"default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';\"\n  routers:\n    stealth-api:\n      entryPoints: [web]\n      rule: \"Host(`__STEALTH_PUBLIC_HOST__`) && PathPrefix(`/v1/`)\"\n      middlewares: [stealth-security-headers]\n      service: stealth-api\n    stealth-console:\n      entryPoints: [web]\n      rule: \"Host(`__STEALTH_PUBLIC_HOST__`) && PathPrefix(`/`)\"\n      middlewares: [stealth-security-headers]\n      service: stealth-console\n  services:\n    stealth-api:\n      loadBalancer:\n        passHostHeader: true\n        servers:\n          - url: http://api:8080\n    stealth-console:\n      loadBalancer:\n        passHostHeader: true\n        servers:\n          - url: http://console:3000\n"
}

func testTraefikCoreAsset() string {
	contents := testTraefikCoreAssetBase()
	streaming := "    stealth-admin-realtime:\n      entryPoints: [web]\n      rule: \"Host(`__STEALTH_PUBLIC_HOST__`) && Path(`/v1/admin/realtime`)\"\n      middlewares: [stealth-security-headers]\n      service: stealth-api\n    stealth-project-realtime:\n      entryPoints: [web]\n      rule: \"Host(`__STEALTH_PUBLIC_HOST__`) && PathRegexp(`^/v1/projects/[0-9a-fA-F-]{36}/realtime$`)\"\n      middlewares: [stealth-security-headers]\n      service: stealth-api\n"
	return strings.Replace(contents, "    stealth-console:\n", streaming+"    stealth-console:\n", 1)
}

func newEngineAssetServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	assets := map[string]string{
		"compose.production.yaml":                     testProductionComposeAsset(),
		"buildkit/buildkitd.toml":                     testBuildKitConfigAsset(),
		"buildkit/stealth-buildkit-rootless.apparmor": testBuildKitAppArmorProfileAsset(),
		"compose.setup.yaml":                          "services:\n  setup:\n",
		"telemetry/otel-collector.yaml":               testMainCollectorAsset(),
		"telemetry/host-metrics.yaml":                 "receivers:\n  hostmetrics:\n",
		"telemetry/docker-logs.yaml":                  "receivers:\n  file_log/docker:\n",
		"telemetry/docker-stats.yaml":                 "receivers:\n  docker_stats:\n",
		"console/deploy/nginx.conf":                   "server {\n}",
		"traefik/traefik.yaml":                        testTraefikStaticAsset(),
		"traefik/dynamic/core.yaml":                   testTraefikCoreAsset(),
		"traefik/dynamic/generated/.gitkeep":          "# Stealth route reconciler\n",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/"+version+"/")
		contents, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, contents)
	}))
}

func TestRunStepSetupUsesSetupComposeAndOnlySetupServices(t *testing.T) {
	layout := writeEngineFixture(t, true)
	runner := &fakeRunner{}
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	engine := New(Options{Runner: runner, AssetBaseURL: assetServer.URL, PollAttempts: 1})
	plan := Plan{Layout: layout, Version: "v1.2.3", Existing: true, Setup: true}

	for _, step := range []Step{StepConfiguration, StepPull, StepDependencies, StepMigration, StepServices} {
		if err := engine.RunStep(context.Background(), plan, step); err != nil {
			t.Fatalf("RunStep(%d): %v", step, err)
		}
	}
	calls := runner.snapshot()
	if len(calls) != 5 {
		t.Fatalf("recorded calls = %#v, want 5", calls)
	}
	for _, call := range calls {
		if call.name != "docker" || !containsPair(call.args, "-f", layout.SetupComposeFile) {
			t.Fatalf("setup call = %#v, want setup Compose file", call)
		}
		if contains(call.args, "worker") {
			t.Fatalf("setup call unexpectedly mentions worker: %#v", call)
		}
	}
	if !equalArgs(calls[0].args[len(calls[0].args)-2:], []string{"config", "--quiet"}) {
		t.Fatalf("setup Compose validation command = %#v", calls[0])
	}
	if got := calls[4].args; !equalArgs(got[len(got)-3:], []string{"setup", "setup-console", "setup-proxy"}) {
		t.Fatalf("setup services = %#v", got)
	}
}

func TestExistingConfigurationInitializesTraefikStateBeforeAssetActivation(t *testing.T) {
	layout := writeEngineFixture(t, false)
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner, AssetBaseURL: assetServer.URL})
	plan := Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", DockerGID: uint32(os.Getgid()), Existing: true}

	if err := engine.RunStep(context.Background(), plan, StepConfiguration); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	if len(calls) != 3 {
		t.Fatalf("recorded calls = %#v, want network probe, staged state init, and Compose validation", calls)
	}
	if !contains(calls[1].args, "traefik-state-init") || !contains(calls[1].args, "--project-directory") {
		t.Fatalf("pre-activation state init command = %#v", calls[1])
	}
	if !equalArgs(calls[2].args[len(calls[2].args)-2:], []string{"config", "--quiet"}) {
		t.Fatalf("configuration validation command = %#v", calls[2])
	}
}

func TestGenerateConfigAcceptsReleaseCandidateVersion(t *testing.T) {
	config, err := GenerateConfig(ConfigOptions{
		Version:           "v0.3.0-rc.1",
		PublicURL:         "https://console.example.test",
		GitHubAppClientID: "Iv1.test-client-id",
		DockerGID:         42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, "STEALTH_API_IMAGE=ghcr.io/stealth-deplover/stealth-api:v0.3.0-rc.1") {
		t.Fatal("generated config did not retain the RC version in image tags")
	}
	if !strings.Contains(config, "STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE=ghcr.io/stealth-deplover/stealth-telemetry-docker-proxy:v0.3.0-rc.1") {
		t.Fatal("generated config did not include the versioned telemetry Docker proxy image")
	}
	for _, expected := range []string{
		"OTEL_COLLECTOR_IMAGE=ghcr.io/stealth-deplover/stealth-otel-collector:v0.3.0-rc.1",
		"OTEL_HOST_COLLECTOR_IMAGE=ghcr.io/stealth-deplover/stealth-otel-collector:v0.3.0-rc.1",
		"OTEL_DOCKER_COLLECTOR_IMAGE=ghcr.io/stealth-deplover/stealth-otel-collector:v0.3.0-rc.1",
		"OTEL_DOCKER_LOGS_COLLECTOR_IMAGE=ghcr.io/stealth-deplover/stealth-otel-docker-logs:v0.3.0-rc.1",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("generated config did not include %q", expected)
		}
	}
}

func TestReleaseVersionValidationKeepsAutomaticUpdatesStableOnly(t *testing.T) {
	for _, version := range []string{"v1.2.3", "v0.3.0-rc.1"} {
		if err := ValidateReleaseVersion(version); err != nil {
			t.Fatalf("ValidateReleaseVersion(%q): %v", version, err)
		}
	}
	if err := ValidateStableReleaseVersion("v0.3.0-rc.1"); err == nil {
		t.Fatal("stable release validation accepted an RC")
	}
	for _, version := range []string{"v1.2.3-beta.1", "v1.2", "1.2.3"} {
		if err := ValidateReleaseVersion(version); err == nil {
			t.Fatalf("ValidateReleaseVersion(%q) accepted an invalid version", version)
		}
	}
}

func TestRunStepCloudflareStartsNamedTunnelProfile(t *testing.T) {
	layout := writeEngineFixture(t, false)
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner})
	plan := Plan{Layout: layout, Version: "v1.2.3", Cloudflare: true}

	if err := engine.RunStep(context.Background(), plan, StepServices); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	if len(calls) != 1 {
		t.Fatalf("recorded calls = %#v, want one service call", calls)
	}
	if !containsPair(calls[0].args, "--profile", "cloudflare") {
		t.Fatalf("Cloudflare profile was not enabled: %#v", calls[0])
	}
	if got := calls[0].args[len(calls[0].args)-12:]; !equalArgs(got, []string{"api", "worker", "console", "proxy", "traefik", "buildkit", "otel-collector", "telemetry-host", "telemetry-docker-logs", "telemetry-docker-proxy", "telemetry-docker", "cloudflared"}) {
		t.Fatalf("Cloudflare service command = %#v", calls[0])
	}
}

func TestRunStepStartsBuildKitWithoutWorkerDependency(t *testing.T) {
	layout := writeEngineFixture(t, false)
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner})
	plan := Plan{Layout: layout, Version: "v1.2.3"}
	if err := engine.RunStep(context.Background(), plan, StepServices); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	if len(calls) != 1 || calls[0].name != "docker" || !contains(calls[0].args, "buildkit") {
		t.Fatalf("service startup calls = %#v, want BuildKit started in the production service step", calls)
	}
	if containsPair(calls[0].args, "--no-deps", "buildkit") {
		t.Fatalf("BuildKit unexpectedly couples worker startup to a BuildKit health dependency: %#v", calls[0])
	}
}

func TestEnsureBuildKitAppArmorProfileLoadsManagedUserNSOnlyProfile(t *testing.T) {
	layout := writeEngineFixture(t, false)
	if err := WritePrivateFile(layout.EnvFile, "APPS_BUILDKIT_APPARMOR_PROFILE="+BuildKitAppArmorProfileName+"\n"); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(t.TempDir(), "apparmor.d")
	if err := os.Mkdir(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDir, BuildKitAppArmorProfileName)
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner, BuildKitAppArmorProfilePath: profilePath})
	if err := engine.ensureBuildKitAppArmorProfile(context.Background(), Plan{Layout: layout}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(profilePath)
	if err != nil || string(contents) != testBuildKitAppArmorProfileAsset() {
		t.Fatalf("installed profile = %q, %v", contents, err)
	}
	calls := runner.snapshot()
	wantName, wantArgs := expectedAppArmorParserCommand("-r", "-W", profilePath)
	if len(calls) != 1 || calls[0].name != wantName || !equalArgs(calls[0].args, wantArgs) {
		t.Fatalf("profile load call = %#v", calls)
	}
}

func TestConfigurationStepLoadsRestrictedHostProfileBeforeServiceSteps(t *testing.T) {
	layout := writeEngineFixture(t, false)
	if err := WritePrivateFile(layout.EnvFile, "PUBLIC_APP_URL=http://127.0.0.1:8080\nAPPS_BUILDKIT_APPARMOR_PROFILE="+BuildKitAppArmorProfileName+"\n"); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(t.TempDir(), "apparmor.d")
	if err := os.Mkdir(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDir, BuildKitAppArmorProfileName)
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	runner := &fakeRunner{}
	engine := New(Options{
		Runner: runner, AssetBaseURL: assetServer.URL,
		BuildKitAppArmorProfilePath: profilePath,
	})
	if err := engine.RunStep(context.Background(), Plan{Layout: layout, Version: "v1.2.3", DockerGID: uint32(os.Getgid()), Existing: true}, StepConfiguration); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	wantName, _ := expectedAppArmorParserCommand("-r", "-W", profilePath)
	if len(calls) < 3 || calls[len(calls)-1].name != wantName {
		t.Fatalf("configuration commands = %#v; AppArmor profile must be loaded after managed assets and Compose validation", calls)
	}
	if calls[len(calls)-2].name != "docker" || !contains(calls[len(calls)-2].args, "config") {
		t.Fatalf("AppArmor profile load did not follow Compose validation: %#v", calls)
	}
}

func TestEnsureBuildKitAppArmorProfileRejectsUnmanagedCollision(t *testing.T) {
	layout := writeEngineFixture(t, false)
	if err := WritePrivateFile(layout.EnvFile, "APPS_BUILDKIT_APPARMOR_PROFILE="+BuildKitAppArmorProfileName+"\n"); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(t.TempDir(), "apparmor.d")
	if err := os.Mkdir(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDir, BuildKitAppArmorProfileName)
	if err := os.WriteFile(profilePath, []byte("profile operator-custom { }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner, BuildKitAppArmorProfilePath: profilePath})
	if err := engine.ensureBuildKitAppArmorProfile(context.Background(), Plan{Layout: layout}); err == nil {
		t.Fatal("installer overwrote an unmanaged profile")
	}
	if calls := runner.snapshot(); len(calls) != 0 {
		t.Fatalf("AppArmor parser ran after unmanaged profile collision: %#v", calls)
	}
}

func TestEnsureBuildKitAppArmorProfileUsesSudoForSystemDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("sudo installation path is for an unprivileged host installer")
	}
	layout := writeEngineFixture(t, false)
	if err := WritePrivateFile(layout.EnvFile, "APPS_BUILDKIT_APPARMOR_PROFILE="+BuildKitAppArmorProfileName+"\n"); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(t.TempDir(), "apparmor.d")
	if err := os.Mkdir(profileDir, 0o500); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDir, BuildKitAppArmorProfileName)
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner, BuildKitAppArmorProfilePath: profilePath})
	if err := engine.ensureBuildKitAppArmorProfile(context.Background(), Plan{Layout: layout}); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	stagingPath := buildKitAppArmorStagingPath(profilePath)
	if len(calls) != 4 || calls[0].name != "sudo" || !equalArgs(calls[0].args, []string{"tee", stagingPath}) || string(calls[0].stdin) != testBuildKitAppArmorProfileAsset() {
		t.Fatalf("profile installation escalation = %#v", calls)
	}
	if calls[1].name != "sudo" || !equalArgs(calls[1].args, []string{"chmod", "0644", stagingPath}) {
		t.Fatalf("profile permission escalation = %#v", calls[1])
	}
	if calls[2].name != "sudo" || !equalArgs(calls[2].args, []string{"mv", "--", stagingPath, profilePath}) {
		t.Fatalf("profile publication escalation = %#v", calls[2])
	}
	if calls[3].name != "sudo" || !equalArgs(calls[3].args, []string{"apparmor_parser", "-r", "-W", profilePath}) {
		t.Fatalf("profile load escalation = %#v", calls[3])
	}
}

func TestRemoveManagedBuildKitAppArmorProfileUnloadsBeforeRemovingFile(t *testing.T) {
	directory := t.TempDir()
	profilePath := filepath.Join(directory, BuildKitAppArmorProfileName)
	if err := os.WriteFile(profilePath, []byte(testBuildKitAppArmorProfileAsset()), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	if err := removeManagedBuildKitAppArmorProfileAt(context.Background(), runner, io.Discard, io.Discard, profilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profilePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed profile remains after removal: %v", err)
	}
	calls := runner.snapshot()
	wantName, wantArgs := expectedAppArmorParserCommand("-R", profilePath)
	if len(calls) != 1 || calls[0].name != wantName || !equalArgs(calls[0].args, wantArgs) {
		t.Fatalf("profile unload call = %#v", calls)
	}
}

func TestRemoveManagedBuildKitAppArmorProfileUsesSudoForSystemDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("sudo removal path is for an unprivileged host installer")
	}
	directory := t.TempDir()
	profilePath := filepath.Join(directory, BuildKitAppArmorProfileName)
	if err := os.WriteFile(profilePath, []byte(testBuildKitAppArmorProfileAsset()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	runner := &fakeRunner{}
	if err := removeManagedBuildKitAppArmorProfileAt(context.Background(), runner, io.Discard, io.Discard, profilePath); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	if len(calls) != 2 || calls[0].name != "sudo" || !equalArgs(calls[0].args, []string{"apparmor_parser", "-R", profilePath}) {
		t.Fatalf("profile unload escalation = %#v", calls)
	}
	if calls[1].name != "sudo" || !equalArgs(calls[1].args, []string{"rm", "--", profilePath}) {
		t.Fatalf("profile file removal escalation = %#v", calls[1])
	}
}

func TestBuildKitAppArmorManagedAssetMatchesValidatedPolicy(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "buildkit", "stealth-buildkit-rootless.apparmor"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBuildKitAppArmorProfileAsset(contents); err != nil {
		t.Fatalf("release AppArmor asset: %v", err)
	}
}

func TestExternalDependenciesNeverStartBundledServices(t *testing.T) {
	layout := writeEngineFixture(t, false)
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner})
	plan := Plan{Layout: layout, Version: "v1.2.3", Existing: true, ExternalDatabase: true, ExternalRedis: true}

	if err := engine.RunStep(context.Background(), plan, StepDependencies); err != nil {
		t.Fatal(err)
	}
	if err := engine.RunStep(context.Background(), plan, StepMigration); err != nil {
		t.Fatal(err)
	}
	if err := engine.RunStep(context.Background(), plan, StepServices); err != nil {
		t.Fatal(err)
	}
	calls := runner.snapshot()
	if len(calls) != 10 {
		t.Fatalf("recorded calls = %#v, want six state inits, telemetry dependency, migration, and services", calls)
	}
	if !equalArgs(calls[0].args[len(calls[0].args)-4:], []string{"run", "--rm", "--no-deps", "otelcol-state-init"}) {
		t.Fatalf("Collector state init command = %#v", calls[0])
	}
	if !equalArgs(calls[1].args[len(calls[1].args)-4:], []string{"run", "--rm", "--no-deps", "telemetry-docker-logs-state-init"}) {
		t.Fatalf("Docker log Collector state init command = %#v", calls[1])
	}
	if len(calls[2].args) < 2 || !strings.HasPrefix(calls[2].args[len(calls[2].args)-2], "STEALTH_TRAEFIK_HOST_UID=") || calls[2].args[len(calls[2].args)-1] != "traefik-state-init" {
		t.Fatalf("Traefik state init command = %#v", calls[2])
	}
	if !equalArgs(calls[3].args[len(calls[3].args)-4:], []string{"run", "--rm", "--no-deps", "cloudflare-state-init"}) {
		t.Fatalf("Cloudflare state init command = %#v", calls[3])
	}
	if !equalArgs(calls[4].args[len(calls[4].args)-4:], []string{"run", "--rm", "--no-deps", "buildkit-worker-credentials-init"}) {
		t.Fatalf("worker BuildKit credentials init command = %#v", calls[4])
	}
	if !equalArgs(calls[5].args[len(calls[5].args)-4:], []string{"run", "--rm", "--no-deps", "buildkit-server-credentials-init"}) {
		t.Fatalf("BuildKit credentials init command = %#v", calls[5])
	}
	if !equalArgs(calls[6].args[len(calls[6].args)-3:], []string{"up", "-d", "clickhouse"}) {
		t.Fatalf("telemetry dependency command = %#v", calls[6])
	}
	if !equalArgs(calls[7].args[len(calls[7].args)-4:], []string{"run", "--rm", "--no-deps", "migrate"}) {
		t.Fatalf("external migration command = %#v", calls[7])
	}
	if !equalArgs(calls[8].args[len(calls[8].args)-6:], []string{"up", "-d", "--no-deps", "--force-recreate", "buildkit", "worker"}) {
		t.Fatalf("credential-refresh restart command = %#v", calls[8])
	}
	if !contains(calls[9].args, "--no-deps") || contains(calls[9].args, "postgres") || contains(calls[9].args, "redis") {
		t.Fatalf("external service command = %#v", calls[9])
	}
}

func TestProductionLifecyclesPrepareCloudflareImportBeforeWorkers(t *testing.T) {
	cases := []struct {
		name          string
		existing      bool
		externalDB    bool
		externalRedis bool
	}{
		{name: "fresh install"},
		{name: "repair", existing: true},
		{name: "upgrade", existing: true},
		{name: "external database", existing: true, externalDB: true},
		{name: "external redis", existing: true, externalRedis: true},
		{name: "external database and redis", existing: true, externalDB: true, externalRedis: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			layout := writeEngineFixture(t, false)
			runner := &fakeRunner{}
			engine := New(Options{Runner: runner})
			plan := Plan{Layout: layout, Version: "v1.2.3", Existing: testCase.existing, ExternalDatabase: testCase.externalDB, ExternalRedis: testCase.externalRedis}
			if err := engine.RunStep(context.Background(), plan, StepDependencies); err != nil {
				t.Fatal(err)
			}
			if err := engine.RunStep(context.Background(), plan, StepServices); err != nil {
				t.Fatal(err)
			}
			calls := runner.snapshot()
			if len(calls) < 6 {
				t.Fatalf("recorded calls = %#v, want state preparation and production services", calls)
			}
			cloudflareInit := calls[3].args
			if !equalArgs(cloudflareInit[len(cloudflareInit)-4:], []string{"run", "--rm", "--no-deps", "cloudflare-state-init"}) {
				t.Fatalf("Cloudflare state initializer did not run before services: %#v", cloudflareInit)
			}
			serviceArgs := calls[len(calls)-1].args
			if !contains(serviceArgs, "api") || !contains(serviceArgs, "worker") {
				t.Fatalf("production service startup command = %#v", serviceArgs)
			}
			if testCase.externalDB || testCase.externalRedis {
				if !contains(serviceArgs, "--no-deps") || contains(serviceArgs, "postgres") || contains(serviceArgs, "redis") {
					t.Fatalf("external dependency startup command = %#v", serviceArgs)
				}
			}
		})
	}
}

func TestInstallLockRejectsConcurrentOperation(t *testing.T) {
	layout := writeEngineFixture(t, false)
	lock, err := acquireLock(layout.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	engine := New(Options{Runner: &fakeRunner{}})
	err = engine.Install(context.Background(), Plan{Layout: layout, Existing: true}, nil)
	if err == nil || !errors.Is(err, ErrOperationInProgress) || !strings.Contains(err.Error(), "another installation operation") {
		t.Fatalf("concurrent Install error = %v", err)
	}
}

func TestProcessLockRejectsConcurrentSetupOrchestrator(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireProcessLock(stateDir, "setup-coordination.lock", "setup orchestration")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := AcquireProcessLock(stateDir, "setup-coordination.lock", "setup orchestration")
	if err == nil {
		second.Close()
		t.Fatal("second setup orchestrator acquired the lock")
	}
	if !strings.Contains(err.Error(), "another setup orchestration operation") {
		t.Fatalf("concurrent setup lock error = %v", err)
	}
}

func TestPrepareDownloadsVersionedSetupAssetsAtomically(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		switch r.URL.Path {
		case "/v1.2.3/compose.production.yaml":
			_, _ = io.WriteString(w, testProductionComposeAsset())
		case "/v1.2.3/buildkit/buildkitd.toml":
			_, _ = io.WriteString(w, testBuildKitConfigAsset())
		case "/v1.2.3/buildkit/stealth-buildkit-rootless.apparmor":
			_, _ = io.WriteString(w, testBuildKitAppArmorProfileAsset())
		case "/v1.2.3/compose.setup.yaml":
			_, _ = io.WriteString(w, "services:\n  setup:\n    image: example\n")
		case "/v1.2.3/telemetry/otel-collector.yaml":
			_, _ = io.WriteString(w, testMainCollectorAsset())
		case "/v1.2.3/telemetry/host-metrics.yaml":
			_, _ = io.WriteString(w, "hostmetrics:\n")
		case "/v1.2.3/telemetry/docker-logs.yaml":
			_, _ = io.WriteString(w, "file_log/docker:\n")
		case "/v1.2.3/telemetry/docker-stats.yaml":
			_, _ = io.WriteString(w, "docker_stats:\n")
		case "/v1.2.3/console/deploy/nginx.conf":
			_, _ = io.WriteString(w, "server {\n}")
		case "/v1.2.3/traefik/traefik.yaml":
			_, _ = io.WriteString(w, testTraefikStaticAsset())
		case "/v1.2.3/traefik/dynamic/core.yaml":
			_, _ = io.WriteString(w, testTraefikCoreAsset())
		case "/v1.2.3/traefik/dynamic/generated/.gitkeep":
			_, _ = io.WriteString(w, "# Stealth route reconciler\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	layout, err := NewLayout(filepath.Join(t.TempDir(), "stealth"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := GenerateConfig(ConfigOptions{Version: "v1.2.3", PublicURL: "http://127.0.0.1:8081", Setup: true, InstallRoot: layout.Root})
	if err != nil {
		t.Fatal(err)
	}
	engine := New(Options{Runner: &fakeRunner{}, AssetBaseURL: server.URL, PollAttempts: 1})
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: "v1.2.3", Setup: true, ConfigContents: config}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{layout.EnvFile, layout.ComposeFile, layout.SetupComposeFile, layout.TelemetryDir + "/otel-collector.yaml", layout.TelemetryDir + "/host-metrics.yaml", layout.TelemetryDir + "/docker-logs.yaml", layout.TelemetryDir + "/docker-stats.yaml", layout.ProxyFile, layout.VersionFile} {
		if !FileExists(path) {
			t.Fatalf("prepared asset %s is missing", path)
		}
	}
	if !FileIsPrivate(layout.EnvFile) {
		t.Fatal("generated setup config is not private")
	}
	stateInfo, err := os.Stat(layout.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if stateInfo.Mode()&os.ModeSetgid == 0 || stateInfo.Mode().Perm() != 0o770 {
		t.Fatalf("prepared state directory mode = %o, want setgid 770", stateInfo.Mode())
	}
	version, err := os.ReadFile(layout.VersionFile)
	if err != nil || string(version) != "v1.2.3\n" {
		t.Fatalf("VERSION = %q, %v", version, err)
	}
}

func TestPrepareMigratesExistingConfigWithTelemetryImages(t *testing.T) {
	layout := writeEngineFixture(t, false)
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	engine := New(Options{Runner: &fakeRunner{}, AssetBaseURL: assetServer.URL})
	plan := Plan{Layout: layout, Version: "v1.2.3", DockerGID: uint32(os.Getgid()), Existing: true}
	if err := engine.Prepare(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	values, err := ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE"]; got != ImageName("stealth-telemetry-docker-proxy", "v1.2.3") {
		t.Fatalf("migrated proxy image = %q", got)
	}
	for key, want := range map[string]string{
		"OTEL_COLLECTOR_IMAGE":             ImageName("stealth-otel-collector", "v1.2.3"),
		"OTEL_HOST_COLLECTOR_IMAGE":        ImageName("stealth-otel-collector", "v1.2.3"),
		"OTEL_DOCKER_COLLECTOR_IMAGE":      ImageName("stealth-otel-collector", "v1.2.3"),
		"OTEL_DOCKER_LOGS_COLLECTOR_IMAGE": ImageName("stealth-otel-docker-logs", "v1.2.3"),
	} {
		if got := values[key]; got != want {
			t.Fatalf("migrated %s = %q, want %q", key, got, want)
		}
	}
}

func TestPrepareMigratesPrePR83ManagedAssets(t *testing.T) {
	const targetVersion = "v0.2.3"
	targetAssets := map[string]string{
		"compose.production.yaml":                     testProductionComposeAsset(),
		"buildkit/buildkitd.toml":                     testBuildKitConfigAsset(),
		"buildkit/stealth-buildkit-rootless.apparmor": testBuildKitAppArmorProfileAsset(),
		"compose.setup.yaml":                          "services:\n  setup:\n",
		"telemetry/otel-collector.yaml":               "receivers:\n  otlp:\nexporters:\n  clickhouse:\n",
		"telemetry/host-metrics.yaml":                 "receivers:\n  hostmetrics:\n",
		"telemetry/docker-logs.yaml":                  "receivers:\n  file_log/docker:\n",
		"telemetry/docker-stats.yaml":                 "receivers:\n  docker_stats:\n",
		"console/deploy/nginx.conf":                   "server {\n  location / { proxy_pass http://console:3000; }\n}\n",
		"traefik/traefik.yaml":                        testTraefikStaticAsset(),
		"traefik/dynamic/core.yaml":                   testTraefikCoreAsset(),
		"traefik/dynamic/generated/.gitkeep":          "# Stealth route reconciler\n",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contents, ok := targetAssets[strings.TrimPrefix(r.URL.Path, "/"+targetVersion+"/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, contents)
	}))
	defer server.Close()

	root := filepath.Join(t.TempDir(), "stealth")
	layout, err := NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(layout.ProxyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(layout.EnvFile, "STEALTH_API_IMAGE=ghcr.io/stealth-deplover/stealth-api:v0.2.2\nPOSTGRES_PASSWORD=old-postgres-secret\nCLICKHOUSE_PASSWORD=old-clickhouse-secret\nPUBLIC_APP_URL=http://127.0.0.1:8080\nDOCKER_GID=999\n"); err != nil {
		t.Fatal(err)
	}
	oldCompose := "services:\n  otel-collector:\n    volumes:\n      - /:/hostfs:ro\n    cap_add:\n      - DAC_READ_SEARCH\nnetworks:\n  stealth:\n"
	oldCollector := "receivers:\n  filelog/docker:\n    include:\n      - /hostfs/var/lib/docker/containers/*/*-json.log\n"
	if err := WriteAtomic(layout.ComposeFile, []byte(oldCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.SetupComposeFile, []byte("services:\n  old-setup:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(filepath.Join(layout.TelemetryDir, "otel-collector.yaml"), []byte(oldCollector), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(filepath.Join(layout.TelemetryDir, "docker-stats.yaml"), []byte("receivers:\n  docker_stats:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.ProxyFile, []byte("server {\n  # pre-PR-83\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.VersionFile, []byte("v0.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(Options{Runner: &fakeRunner{}, AssetBaseURL: server.URL})
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: targetVersion, InstalledVersion: "v0.2.2", DockerGID: uint32(os.Getgid()), Existing: true}); err != nil {
		t.Fatal(err)
	}
	for asset, want := range targetAssets {
		if asset == "traefik/traefik.yaml" {
			want = strings.ReplaceAll(want, "__STEALTH_CLOUDFLARED_TRUSTED_CIDR__", "172.31.0.10/32")
		}
		if asset == "traefik/dynamic/core.yaml" {
			want = strings.ReplaceAll(want, "__STEALTH_PUBLIC_HOST__", "127.0.0.1")
		}
		path := filepath.Join(layout.Root, asset)
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read migrated asset %s: %v", asset, readErr)
		}
		if string(got) != want {
			t.Errorf("asset %s was not migrated:\n got %q\nwant %q", asset, got, want)
		}
	}
	version, err := os.ReadFile(layout.VersionFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(version) != targetVersion+"\n" {
		t.Fatalf("VERSION = %q, want %q", version, targetVersion+"\n")
	}
	values, err := ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["POSTGRES_PASSWORD"] != "old-postgres-secret" || values["CLICKHOUSE_PASSWORD"] != "old-clickhouse-secret" {
		t.Fatalf("migration replaced operator secrets: %#v", values)
	}
	if values["STEALTH_API_IMAGE"] != ImageName("stealth-api", targetVersion) {
		t.Fatalf("migration did not advance canonical API image: %q", values["STEALTH_API_IMAGE"])
	}
	if values["OTEL_DOCKER_LOGS_COLLECTOR_IMAGE"] != ImageName("stealth-otel-docker-logs", targetVersion) {
		t.Fatalf("migration did not add target Docker-log image: %#v", values)
	}
	for key, want := range map[string]string{
		"STEALTH_INGRESS_NETWORK_SUBNET": "172.31.0.0/24",
		"STEALTH_INGRESS_IP_RANGE":       "172.31.0.64/26",
		"STEALTH_TRAEFIK_INGRESS_IP":     "172.31.0.254",
		"STEALTH_CLOUDFLARED_INGRESS_IP": "172.31.0.10",
	} {
		if values[key] != want {
			t.Fatalf("migration did not add %s=%s: %#v", key, want, values)
		}
	}
	if !strings.Contains(values["TRUSTED_PROXY_CIDRS"], "172.31.0.254/32") {
		t.Fatalf("migration did not add the rendered Traefik peer: %q", values["TRUSTED_PROXY_CIDRS"])
	}
}

func TestPreparePreservesPersistedIngressNetwork(t *testing.T) {
	layout := writeEngineFixture(t, false)
	values, err := ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	values["STEALTH_INGRESS_NETWORK_NAME"] = "operator_ingress"
	values["STEALTH_INGRESS_NETWORK_SUBNET"] = "10.44.8.0/24"
	values["STEALTH_INGRESS_IP_RANGE"] = "10.44.8.64/26"
	values["STEALTH_TRAEFIK_INGRESS_IP"] = "10.44.8.254"
	values["STEALTH_CLOUDFLARED_INGRESS_IP"] = "10.44.8.10"
	values["TRUSTED_PROXY_CIDRS"] = "172.30.0.0/24,10.44.8.254/32"
	contents, err := MergeEnv(values, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(layout.EnvFile, contents); err != nil {
		t.Fatal(err)
	}
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	engine := New(Options{AssetBaseURL: assetServer.URL, Runner: &fakeRunner{}})
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", DockerGID: uint32(os.Getgid()), Existing: true}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME":   "operator_ingress",
		"STEALTH_INGRESS_NETWORK_SUBNET": "10.44.8.0/24",
		"STEALTH_INGRESS_IP_RANGE":       "10.44.8.64/26",
		"STEALTH_TRAEFIK_INGRESS_IP":     "10.44.8.254",
		"STEALTH_CLOUDFLARED_INGRESS_IP": "10.44.8.10",
	} {
		if got[key] != want {
			t.Fatalf("repair changed %s to %q, want %q", key, got[key], want)
		}
	}
	if got["TRUSTED_PROXY_CIDRS"] != "172.30.0.0/24,10.44.8.254/32" {
		t.Fatalf("repair changed trusted peers: %q", got["TRUSTED_PROXY_CIDRS"])
	}
}

func TestPreparePreservesBuildKitPKIOnUpgrade(t *testing.T) {
	layout := writeEngineFixture(t, false)
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	profilePath := filepath.Join(t.TempDir(), BuildKitAppArmorProfileName)
	engine := New(Options{AssetBaseURL: assetServer.URL, Runner: &fakeRunner{}, BuildKitAppArmorProfilePath: profilePath})
	plan := Plan{Layout: layout, Version: "v1.2.3", PublicURL: "https://console.example.test", GitHubAppClientID: "Iv1.test-client-id", DockerGID: uint32(os.Getgid()), IngressNetworkName: "stealth_ingress"}
	if err := engine.Prepare(context.Background(), plan); err != nil {
		t.Fatalf("fresh installation preparation: %v", err)
	}
	serverCertPath := filepath.Join(layout.StateDir, buildkitpki.DirectoryName, "server", "cert.pem")
	serverKeyPath := filepath.Join(layout.StateDir, buildkitpki.DirectoryName, "server", "key.pem")
	issuedServerCert, err := os.ReadFile(serverCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(serverKeyPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("host server key permissions = %v, %v; want 0600", info, err)
	}
	plan.Existing = true
	plan.InstalledVersion = "v1.2.3"
	if err := engine.Prepare(context.Background(), plan); err != nil {
		t.Fatalf("upgrade preparation: %v", err)
	}
	upgradedServerCert, err := os.ReadFile(serverCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(upgradedServerCert, issuedServerCert) {
		t.Fatal("routine upgrade rotated the valid BuildKit server identity")
	}
}

func TestPrepareRemovesDefaultPeerWhenAutoSelectingFreeSubnet(t *testing.T) {
	layout, err := NewLayout(filepath.Join(t.TempDir(), "stealth"))
	if err != nil {
		t.Fatal(err)
	}
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	profilePath := filepath.Join(t.TempDir(), "apparmor.d", BuildKitAppArmorProfileName)
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := New(Options{
		AssetBaseURL:                assetServer.URL,
		Runner:                      ingressNetworkTestRunner{networks: []testDockerNetwork{{name: "unrelated", subnet: defaultIngressSubnet}}},
		BuildKitAppArmorProfilePath: profilePath,
	})
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: "v1.2.3", PublicURL: "https://console.example.test", GitHubAppClientID: "Iv1.test-client-id", DockerGID: uint32(os.Getgid()), IngressNetworkName: "stealth_b_ingress"}); err != nil {
		t.Fatal(err)
	}
	values, err := ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["STEALTH_INGRESS_NETWORK_NAME"] != "stealth_b_ingress" {
		t.Fatalf("persisted network name = %q", values["STEALTH_INGRESS_NETWORK_NAME"])
	}
	if values["STEALTH_INGRESS_NETWORK_SUBNET"] != "172.31.1.0/24" {
		t.Fatalf("auto-selected subnet = %q", values["STEALTH_INGRESS_NETWORK_SUBNET"])
	}
	if strings.Contains(values["TRUSTED_PROXY_CIDRS"], "172.31.0.254/32") || !strings.Contains(values["TRUSTED_PROXY_CIDRS"], "172.31.1.254/32") {
		t.Fatalf("trusted peers did not follow auto-selected subnet: %q", values["TRUSTED_PROXY_CIDRS"])
	}
}

func TestPrepareSameVersionRepairRestoresMissingAndCorruptAssets(t *testing.T) {
	const version = "v1.2.3"
	layout := writeEngineFixture(t, false)
	values, err := ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	values["POSTGRES_PASSWORD"] = "operator-postgres-secret"
	values["STEALTH_API_IMAGE"] = "registry.example.test/stealth-api:operator-pin"
	config, err := MergeEnv(values, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(layout.EnvFile, config); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.VersionFile, []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.ComposeFile, []byte("services:\n  old-topology:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(layout.TelemetryDir, "host-metrics.yaml")); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(layout.TelemetryDir, "operator-extra.yaml")
	if err := WriteAtomic(unknown, []byte("operator-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknownRoot := filepath.Join(layout.Root, "operator-local.conf")
	if err := WriteAtomic(unknownRoot, []byte("operator-owned-root\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	assetServer := newEngineAssetServer(t, version)
	defer assetServer.Close()
	engine := New(Options{Runner: &fakeRunner{}, AssetBaseURL: assetServer.URL})
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: version, InstalledVersion: version, DockerGID: uint32(os.Getgid()), Existing: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(layout.ComposeFile); err != nil || string(got) != testProductionComposeAsset() {
		t.Fatalf("same-version repair Compose = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(layout.TelemetryDir, "host-metrics.yaml")); err != nil || !bytes.Contains(got, []byte("hostmetrics:")) {
		t.Fatalf("same-version repair host metrics = %q, %v", got, err)
	}
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "operator-owned\n" {
		t.Fatalf("unknown operator asset changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(unknownRoot); err != nil || string(got) != "operator-owned-root\n" {
		t.Fatalf("unknown root asset changed: %q, %v", got, err)
	}
	values, err = ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["POSTGRES_PASSWORD"] != "operator-postgres-secret" || values["STEALTH_API_IMAGE"] != "registry.example.test/stealth-api:operator-pin" {
		t.Fatalf("same-version repair changed operator values: %#v", values)
	}
	if !FileIsPrivate(layout.EnvFile) {
		t.Fatal("same-version repair loosened config.env permissions")
	}
}

func TestPrepareFailureLeavesExistingInstallationRecoverable(t *testing.T) {
	layout := writeEngineFixture(t, false)
	oldCompose := []byte("services:\n  old-topology:\n")
	if err := WriteAtomic(layout.ComposeFile, oldCompose, 0o644); err != nil {
		t.Fatal(err)
	}
	oldEnv, err := os.ReadFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.VersionFile, []byte("v1.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	engine := New(Options{Runner: &fakeRunner{}, AssetBaseURL: server.URL})
	err = engine.Prepare(context.Background(), Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", DockerGID: uint32(os.Getgid()), Existing: true})
	if err == nil || !strings.Contains(err.Error(), "download managed asset") {
		t.Fatalf("failed preparation error = %v", err)
	}
	if got, readErr := os.ReadFile(layout.ComposeFile); readErr != nil || !bytes.Equal(got, oldCompose) {
		t.Fatalf("failed preparation changed Compose: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(layout.EnvFile); readErr != nil || !bytes.Equal(got, oldEnv) {
		t.Fatalf("failed preparation changed config: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(layout.VersionFile); readErr != nil || string(got) != "v1.2.2\n" {
		t.Fatalf("failed preparation changed VERSION: %q, %v", got, readErr)
	}
}

type configFailureRunner struct{}

func (configFailureRunner) Run(_ context.Context, _ string, _, _ io.Writer, _ string, args ...string) error {
	if contains(args, "config") {
		return errors.New("compose config rejected target asset")
	}
	return nil
}

func (configFailureRunner) Output(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

func TestRunStepConfigurationRollsBackWhenComposeValidationFails(t *testing.T) {
	layout := writeEngineFixture(t, false)
	oldCompose := []byte("services:\n  old-topology:\n")
	if err := WriteAtomic(layout.ComposeFile, oldCompose, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(layout.VersionFile, []byte("v1.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldEnv, err := os.ReadFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	engine := New(Options{Runner: configFailureRunner{}, AssetBaseURL: assetServer.URL})
	err = engine.RunStep(context.Background(), Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", DockerGID: uint32(os.Getgid()), Existing: true}, StepConfiguration)
	if err == nil || !strings.Contains(err.Error(), "compose config rejected") {
		t.Fatalf("Compose validation error = %v", err)
	}
	if got, readErr := os.ReadFile(layout.ComposeFile); readErr != nil || !bytes.Equal(got, oldCompose) {
		t.Fatalf("Compose rollback = %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(layout.EnvFile); readErr != nil || !bytes.Equal(got, oldEnv) {
		t.Fatalf("config rollback = %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(layout.VersionFile); readErr != nil || string(got) != "v1.2.2\n" {
		t.Fatalf("VERSION rollback = %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(layout.StateDir, "managed-assets.previous")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rollback left recovery backup: %v", statErr)
	}
}

func TestManagedAssetRecoveryRestoresOnlyCoherentReleaseStates(t *testing.T) {
	targetVersion := "v1.2.3"
	checkpoints := []struct {
		name  string
		match func(MigrationEvent) bool
		want  string
	}{
		{name: "journal written", match: func(event MigrationEvent) bool { return event.Phase == migrationPhasePrepared }, want: "old"},
		{name: "first backup rename", match: func(event MigrationEvent) bool { return event.Phase == migrationEventBackupRenamed && event.Index == 1 }, want: "old"},
		{name: "last backup rename", match: func(event MigrationEvent) bool {
			return event.Phase == migrationEventBackupRenamed && event.Index == event.Total
		}, want: "old"},
		{name: "first target activation", match: func(event MigrationEvent) bool {
			return event.Phase == migrationEventAssetActivated && event.Index == 1
		}, want: "old"},
		{name: "last target activation", match: func(event MigrationEvent) bool {
			return event.Phase == migrationEventAssetActivated && event.Index == event.Total
		}, want: "old"},
		{name: "config activation", match: func(event MigrationEvent) bool { return event.Phase == migrationPhaseConfigActivated }, want: "old"},
		{name: "version activation", match: func(event MigrationEvent) bool { return event.Phase == migrationPhaseVersionActivated }, want: "old"},
		{name: "compose validation", match: func(event MigrationEvent) bool { return event.Phase == migrationPhaseComposeValidated }, want: "new"},
	}

	for _, checkpoint := range checkpoints {
		t.Run(checkpoint.name, func(t *testing.T) {
			layout := writeEngineFixture(t, false)
			if err := WriteAtomic(layout.ComposeFile, []byte("services:\n  old-topology:\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := WriteAtomic(layout.VersionFile, []byte("v1.2.2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := WritePrivateFile(layout.EnvFile, "PUBLIC_APP_URL=http://127.0.0.1:8080\nPOSTGRES_PASSWORD=crash-test-secret\n"); err != nil {
				t.Fatal(err)
			}
			old := captureManagedInstallationState(t, layout)
			assetServer := newEngineAssetServer(t, targetVersion)
			defer assetServer.Close()
			engine := New(Options{
				Runner:       &fakeRunner{},
				AssetBaseURL: assetServer.URL,
				MigrationHook: func(event MigrationEvent) error {
					if checkpoint.match(event) {
						return errMigrationProcessInterrupted
					}
					return nil
				},
			})
			plan := Plan{Layout: layout, Version: targetVersion, InstalledVersion: "v1.2.2", DockerGID: uint32(os.Getgid()), Existing: true}
			err := engine.RunStep(context.Background(), plan, StepConfiguration)
			if !errors.Is(err, errMigrationProcessInterrupted) {
				t.Fatalf("injected interruption error = %v", err)
			}

			// A new Engine models a reboot/SIGKILL: it has no in-memory state and
			// uses only the durable journal to choose rollback or completion.
			recovery := New(Options{Runner: &fakeRunner{}, AssetBaseURL: assetServer.URL})
			allowed := make(map[string]struct{})
			for _, asset := range recovery.managedAssetSpecs(plan) {
				allowed[asset.Path] = struct{}{}
			}
			if err := recoverInterruptedManagedAssetMigration(layout, allowed); err != nil {
				t.Fatalf("recoverInterruptedManagedAssetMigration() error = %v", err)
			}
			if checkpoint.want == "old" {
				assertManagedInstallationState(t, layout, old)
			} else {
				assertCurrentManagedInstallationState(t, layout, targetVersion)
			}
			if _, statErr := os.Stat(filepath.Join(layout.StateDir, managedAssetPendingFile)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("recovery left pending journal: %v", statErr)
			}
		})
	}
}

func TestTargetReleaseManifestCanAddFutureManagedAsset(t *testing.T) {
	const version = "v1.2.3"
	layout := writeEngineFixture(t, false)
	assets := map[string]string{
		"compose.production.yaml":                     testProductionComposeAsset(),
		"buildkit/buildkitd.toml":                     testBuildKitConfigAsset(),
		"buildkit/stealth-buildkit-rootless.apparmor": testBuildKitAppArmorProfileAsset(),
		"telemetry/otel-collector.yaml":               testMainCollectorAsset(),
		"telemetry/host-metrics.yaml":                 "receivers:\n  hostmetrics:\n",
		"telemetry/docker-logs.yaml":                  "receivers:\n  file_log/docker:\n",
		"telemetry/docker-stats.yaml":                 "receivers:\n  docker_stats:\n",
		"console/deploy/nginx.conf":                   "server {\n}",
		"traefik/traefik.yaml":                        testTraefikStaticAsset(),
		"traefik/dynamic/core.yaml":                   testTraefikCoreAsset(),
		"traefik/dynamic/generated/.gitkeep":          "# Stealth route reconciler\n",
		"telemetry/future.yaml":                       "future_receiver:\n",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contents, ok := assets[strings.TrimPrefix(r.URL.Path, "/"+version+"/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, contents)
	}))
	defer server.Close()

	oldManifest := DefaultManagedAssets()
	targetManifest := append(DefaultManagedAssets(), ManagedAsset{Path: "telemetry/future.yaml", RemotePath: "telemetry/future.yaml", Marker: "future_receiver:"})
	for _, asset := range oldManifest {
		if asset.Path == "telemetry/future.yaml" {
			t.Fatal("old release unexpectedly knows future asset")
		}
	}
	engine := New(Options{Runner: &fakeRunner{}, AssetBaseURL: server.URL, ManagedAssets: targetManifest})
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: version, InstalledVersion: "v1.2.2", DockerGID: uint32(os.Getgid()), Existing: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(layout.TelemetryDir, "future.yaml")); err != nil || string(got) != "future_receiver:\n" {
		t.Fatalf("target-owned future asset = %q, %v", got, err)
	}
}

func TestManagedAssetMigrationKeepsPreviousRecoverySetUntilTargetIsValidated(t *testing.T) {
	layout := writeEngineFixture(t, false)
	if err := WriteAtomic(layout.VersionFile, []byte("v1.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(layout.StateDir, "managed-assets.previous")
	if err := WriteAtomic(filepath.Join(previous, "sentinel"), []byte("older bounded recovery set\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assetServer := newEngineAssetServer(t, "v1.2.3")
	defer assetServer.Close()
	engine := New(Options{
		Runner:       &fakeRunner{},
		AssetBaseURL: assetServer.URL,
		MigrationHook: func(event MigrationEvent) error {
			if event.Phase == migrationPhaseVersionActivated {
				return errMigrationProcessInterrupted
			}
			return nil
		},
	})
	plan := Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", DockerGID: uint32(os.Getgid()), Existing: true}
	if err := engine.RunStep(context.Background(), plan, StepConfiguration); !errors.Is(err, errMigrationProcessInterrupted) {
		t.Fatalf("injected interruption error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(previous, "sentinel")); err != nil || string(got) != "older bounded recovery set\n" {
		t.Fatalf("previous recovery set was removed before validation: %q, %v", got, err)
	}
	allowed := make(map[string]struct{})
	for _, asset := range DefaultManagedAssets() {
		if !asset.setupOnly {
			allowed[asset.Path] = struct{}{}
		}
	}
	if err := recoverInterruptedManagedAssetMigration(layout, allowed); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(previous, "sentinel")); err != nil || string(got) != "older bounded recovery set\n" {
		t.Fatalf("rollback replaced previous recovery set: %q, %v", got, err)
	}

	engine = New(Options{Runner: &fakeRunner{}, AssetBaseURL: assetServer.URL})
	if err := engine.RunStep(context.Background(), plan, StepConfiguration); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(previous, "sentinel")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finalized recovery set retained stale sentinel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(previous, "config.env")); err != nil {
		t.Fatalf("finalized previous recovery set does not retain private config backup: %v", err)
	}
}

type managedInstallationState struct {
	assets  map[string][]byte
	config  []byte
	version []byte
}

func captureManagedInstallationState(t *testing.T, layout Layout) managedInstallationState {
	t.Helper()
	state := managedInstallationState{assets: make(map[string][]byte)}
	for _, asset := range DefaultManagedAssets() {
		if asset.setupOnly {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(layout.Root, asset.Path))
		if err != nil {
			t.Fatalf("read %s: %v", asset.Path, err)
		}
		state.assets[asset.Path] = contents
	}
	var err error
	if state.config, err = os.ReadFile(layout.EnvFile); err != nil {
		t.Fatal(err)
	}
	if state.version, err = os.ReadFile(layout.VersionFile); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertManagedInstallationState(t *testing.T, layout Layout, want managedInstallationState) {
	t.Helper()
	for path, contents := range want.assets {
		if got, err := os.ReadFile(filepath.Join(layout.Root, path)); err != nil || !bytes.Equal(got, contents) {
			t.Fatalf("recovered %s = %q, %v; want %q", path, got, err, contents)
		}
	}
	if got, err := os.ReadFile(layout.EnvFile); err != nil || !bytes.Equal(got, want.config) {
		t.Fatalf("recovered config.env = %q, %v; want %q", got, err, want.config)
	}
	if got, err := os.ReadFile(layout.VersionFile); err != nil || !bytes.Equal(got, want.version) {
		t.Fatalf("recovered VERSION = %q, %v; want %q", got, err, want.version)
	}
}

func assertCurrentManagedInstallationState(t *testing.T, layout Layout, version string) {
	t.Helper()
	if got, err := os.ReadFile(layout.ComposeFile); err != nil || string(got) != testProductionComposeAsset() {
		t.Fatalf("current Compose = %q, %v", got, err)
	}
	if got, err := os.ReadFile(layout.VersionFile); err != nil || string(got) != version+"\n" {
		t.Fatalf("current VERSION = %q, %v", got, err)
	}
	values, err := ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["POSTGRES_PASSWORD"] != "crash-test-secret" {
		t.Fatalf("current config did not preserve secret: %#v", values)
	}
}

func TestWaitChecksInternalAndPublicEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	layout := writeEngineFixture(t, false)
	engine := New(Options{HTTPClient: server.Client(), PollAttempts: 1})
	plan := Plan{Layout: layout, InternalAPIURL: server.URL, InternalConsoleURL: server.URL, InternalProxyURL: server.URL, PublicURL: server.URL, VerifyPublicURL: true}
	if err := engine.Wait(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsPair(values []string, first, second string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == first && values[index+1] == second {
			return true
		}
	}
	return false
}

func equalArgs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
