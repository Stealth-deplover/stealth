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
)

type recordedCommand struct {
	name string
	args []string
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
	if err := WritePrivateFile(layout.EnvFile, "PUBLIC_APP_URL=http://127.0.0.1:8080\n"); err != nil {
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
	return layout
}

func testProductionComposeAsset() string {
	return "services:\n  otel-collector:\n  telemetry-host:\n  telemetry-docker-logs:\n  telemetry-docker:\n  telemetry-docker-proxy:\nnetworks:\n  telemetry_ingest:\n"
}

func testMainCollectorAsset() string {
	return "receivers:\n  otlp:\nexporters:\n  clickhouse:\n"
}

func newEngineAssetServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	assets := map[string]string{
		"compose.production.yaml":       testProductionComposeAsset(),
		"compose.setup.yaml":            "services:\n  setup:\n",
		"telemetry/otel-collector.yaml": testMainCollectorAsset(),
		"telemetry/host-metrics.yaml":   "receivers:\n  hostmetrics:\n",
		"telemetry/docker-logs.yaml":    "receivers:\n  file_log/docker:\n",
		"telemetry/docker-stats.yaml":   "receivers:\n  docker_stats:\n",
		"console/deploy/nginx.conf":     "server {\n}",
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
	plan := Plan{Layout: layout, Version: "v1.2.3", Existing: true, Cloudflare: true}

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
	if got := calls[0].args[len(calls[0].args)-10:]; !equalArgs(got, []string{"api", "worker", "console", "proxy", "otel-collector", "telemetry-host", "telemetry-docker-logs", "telemetry-docker-proxy", "telemetry-docker", "cloudflared"}) {
		t.Fatalf("Cloudflare service command = %#v", calls[0])
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
	if len(calls) != 5 {
		t.Fatalf("recorded calls = %#v, want both state inits, telemetry dependency, migration, and services", calls)
	}
	if !equalArgs(calls[0].args[len(calls[0].args)-4:], []string{"run", "--rm", "--no-deps", "otelcol-state-init"}) {
		t.Fatalf("Collector state init command = %#v", calls[0])
	}
	if !equalArgs(calls[1].args[len(calls[1].args)-4:], []string{"run", "--rm", "--no-deps", "telemetry-docker-logs-state-init"}) {
		t.Fatalf("Docker log Collector state init command = %#v", calls[1])
	}
	if !equalArgs(calls[2].args[len(calls[2].args)-3:], []string{"up", "-d", "clickhouse"}) {
		t.Fatalf("telemetry dependency command = %#v", calls[2])
	}
	if !equalArgs(calls[3].args[len(calls[3].args)-4:], []string{"run", "--rm", "--no-deps", "migrate"}) {
		t.Fatalf("external migration command = %#v", calls[3])
	}
	if !contains(calls[4].args, "--no-deps") || contains(calls[4].args, "postgres") || contains(calls[4].args, "redis") {
		t.Fatalf("external service command = %#v", calls[4])
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
	plan := Plan{Layout: layout, Version: "v1.2.3", Existing: true}
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
		"compose.production.yaml":       testProductionComposeAsset(),
		"compose.setup.yaml":            "services:\n  setup:\n",
		"telemetry/otel-collector.yaml": "receivers:\n  otlp:\nexporters:\n  clickhouse:\n",
		"telemetry/host-metrics.yaml":   "receivers:\n  hostmetrics:\n",
		"telemetry/docker-logs.yaml":    "receivers:\n  file_log/docker:\n",
		"telemetry/docker-stats.yaml":   "receivers:\n  docker_stats:\n",
		"console/deploy/nginx.conf":     "server {\n  location / { proxy_pass http://console:3000; }\n}\n",
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
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: targetVersion, InstalledVersion: "v0.2.2", Existing: true}); err != nil {
		t.Fatal(err)
	}
	for asset, want := range targetAssets {
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
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: version, InstalledVersion: version, Existing: true}); err != nil {
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
	engine := New(Options{AssetBaseURL: server.URL})
	err = engine.Prepare(context.Background(), Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", Existing: true})
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
	err = engine.RunStep(context.Background(), Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", Existing: true}, StepConfiguration)
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
			plan := Plan{Layout: layout, Version: targetVersion, InstalledVersion: "v1.2.2", Existing: true}
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
		"compose.production.yaml":       testProductionComposeAsset(),
		"telemetry/otel-collector.yaml": testMainCollectorAsset(),
		"telemetry/host-metrics.yaml":   "receivers:\n  hostmetrics:\n",
		"telemetry/docker-logs.yaml":    "receivers:\n  file_log/docker:\n",
		"telemetry/docker-stats.yaml":   "receivers:\n  docker_stats:\n",
		"console/deploy/nginx.conf":     "server {\n}",
		"telemetry/future.yaml":         "future_receiver:\n",
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
	if err := engine.Prepare(context.Background(), Plan{Layout: layout, Version: version, InstalledVersion: "v1.2.2", Existing: true}); err != nil {
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
	plan := Plan{Layout: layout, Version: "v1.2.3", InstalledVersion: "v1.2.2", Existing: true}
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
