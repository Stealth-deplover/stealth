package installengine

import (
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

func TestRunStepSetupUsesSetupComposeAndOnlySetupServices(t *testing.T) {
	layout := writeEngineFixture(t, true)
	runner := &fakeRunner{}
	engine := New(Options{Runner: runner, PollAttempts: 1})
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
	if len(calls) != 4 {
		t.Fatalf("recorded calls = %#v, want state init, telemetry dependency, migration, and services", calls)
	}
	if !equalArgs(calls[0].args[len(calls[0].args)-4:], []string{"run", "--rm", "--no-deps", "otelcol-state-init"}) {
		t.Fatalf("Collector state init command = %#v", calls[0])
	}
	if !equalArgs(calls[1].args[len(calls[1].args)-3:], []string{"up", "-d", "clickhouse"}) {
		t.Fatalf("telemetry dependency command = %#v", calls[1])
	}
	if !equalArgs(calls[2].args[len(calls[2].args)-4:], []string{"run", "--rm", "--no-deps", "migrate"}) {
		t.Fatalf("external migration command = %#v", calls[2])
	}
	if !contains(calls[3].args, "--no-deps") || contains(calls[3].args, "postgres") || contains(calls[3].args, "redis") {
		t.Fatalf("external service command = %#v", calls[3])
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
		case "/v1.2.3/compose.production.yaml", "/v1.2.3/compose.setup.yaml":
			_, _ = io.WriteString(w, "services:\n  setup:\n    image: example\n")
		case "/v1.2.3/telemetry/otel-collector.yaml":
			_, _ = io.WriteString(w, "receivers:\n")
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
	engine := New(Options{AssetBaseURL: server.URL, PollAttempts: 1})
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
	engine := New(Options{Runner: &fakeRunner{}})
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
