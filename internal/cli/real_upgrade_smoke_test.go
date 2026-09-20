package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/installengine"
)

const (
	realV025UpgradeRootEnv   = "STEALTH_REAL_V025_UPGRADE_ROOT"
	realV025AssetBaseEnv     = "STEALTH_REAL_V025_ASSET_BASE"
	realV025BridgeVersionEnv = "STEALTH_REAL_V025_BRIDGE_VERSION"
	realV025TargetVersionEnv = "STEALTH_REAL_V025_TARGET_VERSION"
)

// TestRealV025UpgradeSmoke is the high-level AUD-17 proof. It is enabled by
// scripts/real-v025-upgrade-smoke.sh because it builds two real CLI binaries
// and is followed there by the Docker Compose production smoke.
//
// The release server and target asset server are local test servers. The
// updater still executes its real release discovery, HTTPS archive download,
// checksum verification, archive validation, target-version validation, target
// migration handoff, and atomic CLI replacement paths. The target migration
// hook executes the actual target binary's exact internal command. That binary
// is built with a test-only tag which injects the local managed-asset server;
// the shipped production binary has no such override.
func TestRealV025UpgradeSmoke(t *testing.T) {
	root := strings.TrimSpace(os.Getenv(realV025UpgradeRootEnv))
	assetBase := strings.TrimSpace(os.Getenv(realV025AssetBaseEnv))
	bridgeVersion := strings.TrimSpace(os.Getenv(realV025BridgeVersionEnv))
	targetVersion := strings.TrimSpace(os.Getenv(realV025TargetVersionEnv))
	if root == "" && assetBase == "" && bridgeVersion == "" && targetVersion == "" {
		t.Skip("real v0.2.5 upgrade smoke is enabled by the Docker workflow")
	}
	if root == "" || assetBase == "" || bridgeVersion == "" || targetVersion == "" {
		t.Fatal("real v0.2.5 upgrade smoke environment is incomplete")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("real release archive smoke is only configured for linux/amd64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	layout, err := installengine.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEALTH_INSTALL_DIR", root)
	fixtureRoot := realV025FixtureRoot(t)
	assertRealV025Fixture(t, root, fixtureRoot, layout)

	values, err := installengine.ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	operatorSecrets := map[string]string{
		"POSTGRES_PASSWORD":    values["POSTGRES_PASSWORD"],
		"REDIS_PASSWORD":       values["REDIS_PASSWORD"],
		"FUNCTIONS_SECRET_KEY": values["FUNCTIONS_SECRET_KEY"],
		"BOOTSTRAP_CLI_KEY":    values["BOOTSTRAP_CLI_KEY"],
	}
	operatorAPIImage := values["STEALTH_API_IMAGE"]
	operatorPublicURL := values["PUBLIC_APP_URL"]

	// The target App runs the normal lifecycle, including StepVerify. A local
	// HTTP endpoint stands in for the already-running services in this Go-level
	// handoff test; the following shell smoke starts the real migrated stack.
	probeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(probeServer.Close)
	probeAddress := probeServer.Listener.Addr().String()
	probePort := ""
	if host, port, splitErr := splitHostPort(probeAddress); splitErr == nil && host == "127.0.0.1" {
		probePort = port
		values["API_HOST_PORT"] = port
		values["CONSOLE_HOST_PORT"] = port
		values["PROXY_HTTP_PORT"] = port
	} else {
		t.Fatalf("probe server address %q is not a loopback TCP address", probeAddress)
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, installengine.FormatEnvFile(values)); err != nil {
		t.Fatal(err)
	}
	originalEnv, err := os.ReadFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	originalCompose, err := os.ReadFile(layout.ComposeFile)
	if err != nil {
		t.Fatal(err)
	}

	bridgeBinary := buildReleaseTestCLI(t, bridgeVersion, "aud17realupgrade")
	targetBinary := buildReleaseTestCLI(t, targetVersion, "aud17realupgrade")
	runRealV025BridgeReconciliation(t, fixtureRoot, assetBase, bridgeVersion, probePort)
	t.Setenv("STEALTH_INSTALL_DIR", root)
	installedCLI := filepath.Join(root, "stealth")
	if err := installengine.WriteAtomic(installedCLI, []byte("#!/bin/sh\nprintf '%s\\n' 'Stealth v0.2.5'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// This first phase deliberately uses the released v0.2.5 updater contract:
	// validate and replace only the CLI executable. It cannot know the target
	// release's managed-asset manifest.
	bridgeArchive, err := os.ReadFile(bridgeBinary)
	if err != nil {
		t.Fatal(err)
	}
	legacy := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	legacy.executablePath = func() (string, error) { return installedCLI, nil }
	legacy.runner = execCommandRunner{}
	legacyReleaseServer := newUpdateTestServer(t, testArchive(t, "stealth", bridgeArchive, 0), "")
	legacyAsset, err := releaseAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	legacyReleaseServer.release = githubRelease{
		TagName: bridgeVersion,
		Assets: []githubReleaseAsset{
			{Name: legacyAsset},
			{Name: "checksums.txt"},
		},
	}
	legacyReleaseServer.checksums = testChecksums(legacyReleaseServer.archive, legacyAsset)
	legacy.httpClient = legacyReleaseServer.server.Client()
	legacy.releaseAPIBase = legacyReleaseServer.server.URL
	legacy.releaseDownloadBase = legacyReleaseServer.server.URL
	legacy.currentVersion = func() string { return "v0.2.5" }
	if err := runV025StyleReleaseUpdate(context.Background(), legacy); err != nil {
		t.Fatalf("v0.2.5-style CLI-only update: %v", err)
	}
	assertExecutableVersion(t, installedCLI, bridgeVersion)
	if got, err := os.ReadFile(layout.ComposeFile); err != nil || !bytes.Equal(got, originalCompose) {
		t.Fatalf("phase-1 CLI update changed production assets: %q, %v", got, err)
	}
	if got, err := os.ReadFile(layout.EnvFile); err != nil || !bytes.Equal(got, originalEnv) {
		t.Fatalf("phase-1 CLI update changed config.env: %q, %v", got, err)
	}
	if got, err := os.ReadFile(layout.VersionFile); err != nil || strings.TrimSpace(string(got)) != "v0.2.5" {
		t.Fatalf("phase-1 VERSION = %q, %v", got, err)
	}

	targetArchive, err := os.ReadFile(targetBinary)
	if err != nil {
		t.Fatal(err)
	}
	releaseServer := newUpdateTestServer(t, testArchive(t, "stealth", targetArchive, 0), "")
	asset, err := releaseAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	releaseArchive := testArchive(t, "stealth", targetArchive, 0)
	releaseServer.archive = releaseArchive
	releaseServer.checksums = testChecksums(releaseArchive, asset)
	releaseServer.release = githubRelease{
		TagName: targetVersion,
		Assets: []githubReleaseAsset{
			{Name: asset},
			{Name: "checksums.txt"},
		},
	}

	var bridgeOutput, bridgeErrors bytes.Buffer
	bridge := NewApp(strings.NewReader(""), &bridgeOutput, &bridgeErrors)
	bridge.httpClient = releaseServer.server.Client()
	bridge.releaseAPIBase = releaseServer.server.URL
	bridge.releaseDownloadBase = releaseServer.server.URL
	bridge.currentVersion = func() string { return bridgeVersion }
	bridge.executablePath = func() (string, error) { return installedCLI, nil }
	bridge.runner = execCommandRunner{}
	var invokedBinary, invokedVersion, targetMigrationOutput string
	bridge.runTargetMigration = func(ctx context.Context, binaryPath, version string) error {
		invokedBinary = binaryPath
		invokedVersion = version
		assertExecutableVersion(t, binaryPath, targetVersion)
		command := exec.CommandContext(ctx, binaryPath, "internal", "migrate-installation", "--target-version", version)
		var output bytes.Buffer
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Run(); err != nil {
			targetMigrationOutput = output.String()
			return fmt.Errorf("target internal migration failed: %w: %s", err, targetMigrationOutput)
		}
		targetMigrationOutput = output.String()
		return nil
	}

	if code := bridge.run([]string{"update"}); code != 0 {
		t.Fatalf("bridge update exit code = %d, stderr = %s", code, bridgeErrors.String())
	}
	if invokedBinary == "" || invokedVersion != targetVersion || invokedBinary == installedCLI {
		t.Fatalf("target handoff = binary %q version %q installed=%q", invokedBinary, invokedVersion, installedCLI)
	}
	assertExecutableVersion(t, installedCLI, targetVersion)
	assertMigratedV025State(t, layout, targetVersion, operatorSecrets, operatorAPIImage, operatorPublicURL)
	if strings.Contains(bridgeErrors.String()+targetMigrationOutput, operatorSecrets["POSTGRES_PASSWORD"]) || strings.Contains(bridgeErrors.String()+targetMigrationOutput, operatorSecrets["FUNCTIONS_SECRET_KEY"]) {
		t.Fatal("upgrade diagnostics leaked an operator secret")
	}
}

func realV025FixtureRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate real v0.2.5 fixture")
	}
	return filepath.Join(filepath.Dir(source), "testdata", "v0.2.5")
}

func runRealV025BridgeReconciliation(t *testing.T, fixtureRoot, assetBase, bridgeVersion, probePort string) {
	t.Helper()
	reconciliationRoot := filepath.Join(t.TempDir(), "install")
	copyFixtureDirectory(t, fixtureRoot, reconciliationRoot)
	layout, err := installengine.NewLayout(reconciliationRoot)
	if err != nil {
		t.Fatal(err)
	}
	values, err := installengine.ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	values["API_HOST_PORT"] = probePort
	values["CONSOLE_HOST_PORT"] = probePort
	values["PROXY_HTTP_PORT"] = probePort
	if err := installengine.WritePrivateFile(layout.EnvFile, installengine.FormatEnvFile(values)); err != nil {
		t.Fatal(err)
	}

	// This separate copy models the case where the bridge is already the
	// latest CLI but the platform VERSION still says v0.2.5. The normal update
	// command must reconcile the platform on comparison == 0.
	releaseServer := newUpdateTestServer(t, nil, "")
	releaseServer.release = githubRelease{TagName: bridgeVersion}
	t.Setenv("STEALTH_INSTALL_DIR", reconciliationRoot)
	var output, errorsOutput bytes.Buffer
	app := NewApp(strings.NewReader(""), &output, &errorsOutput)
	app.assetBase = assetBase
	releaseClient := releaseServer.server.Client()
	app.httpClient = &http.Client{
		Timeout: 20 * time.Second,
		Transport: releaseAndHTTPTransport{
			secure: releaseClient.Transport,
		},
	}
	app.releaseAPIBase = releaseServer.server.URL
	app.releaseDownloadBase = releaseServer.server.URL
	app.currentVersion = func() string { return bridgeVersion }
	app.runner = &setupRunner{}
	app.pollAttempts = 1
	app.pollInterval = time.Millisecond
	for _, endpoint := range []string{"/healthz", "/readyz", "/version", "/"} {
		response, requestErr := app.httpClient.Get("http://127.0.0.1:" + probePort + endpoint)
		if requestErr != nil {
			t.Fatalf("probe endpoint %s failed before reconciliation: %v", endpoint, requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("probe endpoint %s status = %d", endpoint, response.StatusCode)
		}
	}
	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("bridge reconciliation exit code = %d, stdout=%s, stderr=%s", code, output.String(), errorsOutput.String())
	}
	secrets := map[string]string{
		"POSTGRES_PASSWORD":    values["POSTGRES_PASSWORD"],
		"REDIS_PASSWORD":       values["REDIS_PASSWORD"],
		"FUNCTIONS_SECRET_KEY": values["FUNCTIONS_SECRET_KEY"],
		"BOOTSTRAP_CLI_KEY":    values["BOOTSTRAP_CLI_KEY"],
	}
	assertMigratedV025State(t, layout, bridgeVersion, secrets, values["STEALTH_API_IMAGE"], values["PUBLIC_APP_URL"])
}

func copyFixtureDirectory(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, contents, info.Mode().Perm()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("copy v0.2.5 fixture: %v", err)
	}
}

func assertRealV025Fixture(t *testing.T, root, fixtureRoot string, layout installengine.Layout) {
	t.Helper()
	for _, relative := range []string{"compose.production.yaml", "compose.setup.yaml", "console/deploy/nginx.conf", "VERSION", "config.env"} {
		want, err := os.ReadFile(filepath.Join(fixtureRoot, relative))
		if err != nil {
			t.Fatalf("read fixture %s: %v", relative, err)
		}
		got, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("read installation fixture %s: %v", relative, err)
		}
		if relative != "config.env" && !bytes.Equal(got, want) {
			t.Fatalf("installation file %s is not the checked-in v0.2.5 fixture", relative)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "telemetry")); !os.IsNotExist(err) {
		t.Fatalf("v0.2.5 fixture unexpectedly contains telemetry assets: %v", err)
	}
	if layout.Root != root {
		t.Fatalf("layout root = %q, want %q", layout.Root, root)
	}
}

func buildReleaseTestCLI(t *testing.T, version string, tags ...string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	output := filepath.Join(t.TempDir(), "stealth-"+strings.TrimPrefix(version, "v"))
	args := []string{"build", "-trimpath"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, "-ldflags", "-s -w -X github.com/Stealth-deplover/stealth/internal/buildinfo.Version="+version, "-o", output, "./cmd/stealth")
	command := exec.Command("go", args...)
	command.Dir = repositoryRoot
	if outputBytes, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build target CLI %s: %v\n%s", version, err, outputBytes)
	}
	return output
}

// runV025StyleReleaseUpdate copies the released v0.2.5 performUpdate contract
// from the tagged updater: it discovers the release, downloads and verifies
// the archive, validates the extracted binary, and replaces only the CLI.
// Deliberately do not call performUpdate here because current performUpdate
// includes the post-bridge target migration that v0.2.5 did not have.
func runV025StyleReleaseUpdate(ctx context.Context, app *App) error {
	release, err := app.latestStableRelease(ctx)
	if err != nil {
		return err
	}
	asset, err := releaseAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	archiveURL, err := appendReleasePath(app.releaseDownloadBase, release.TagName, asset)
	if err != nil {
		return err
	}
	checksumsURL, err := appendReleasePath(app.releaseDownloadBase, release.TagName, "checksums.txt")
	if err != nil {
		return err
	}
	archive, err := app.fetchUpdateAsset(ctx, archiveURL, maxUpdateArchiveSize)
	if err != nil {
		return err
	}
	checksums, err := app.fetchUpdateAsset(ctx, checksumsURL, 128<<10)
	if err != nil {
		return err
	}
	expected, err := checksumForAsset(string(checksums), asset)
	if err != nil {
		return err
	}
	if err := verifySHA256(archive, expected); err != nil {
		return err
	}
	temporaryDir, temporaryBinary, err := extractUpdateBinary(archive)
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryDir)
	if err := app.validateDownloadedBinary(ctx, temporaryBinary, release.TagName); err != nil {
		return err
	}
	target, err := app.currentExecutablePath()
	if err != nil {
		return err
	}
	return app.replaceExecutable(ctx, temporaryBinary, target)
}

func assertExecutableVersion(t *testing.T, executable, expected string) {
	t.Helper()
	output, err := exec.Command(executable, "version").Output()
	if err != nil {
		t.Fatalf("execute %s version: %v", executable, err)
	}
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if line != "Stealth "+expected {
		t.Fatalf("%s version = %q, want %q", executable, line, "Stealth "+expected)
	}
}

func assertMigratedV025State(t *testing.T, layout installengine.Layout, targetVersion string, secrets map[string]string, operatorAPIImage, operatorPublicURL string) {
	t.Helper()
	values, err := installengine.ReadEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range secrets {
		if values[key] != want {
			t.Fatalf("migration changed %s", key)
		}
	}
	if values["STEALTH_API_IMAGE"] != operatorAPIImage || values["PUBLIC_APP_URL"] != operatorPublicURL {
		t.Fatalf("operator overrides changed: API=%q public_url=%q", values["STEALTH_API_IMAGE"], values["PUBLIC_APP_URL"])
	}
	for _, key := range []string{
		"OTEL_COLLECTOR_IMAGE",
		"OTEL_HOST_COLLECTOR_IMAGE",
		"OTEL_DOCKER_COLLECTOR_IMAGE",
		"OTEL_DOCKER_LOGS_COLLECTOR_IMAGE",
		"STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE",
		"CLICKHOUSE_IMAGE",
		"CLICKHOUSE_DATABASE",
		"CLICKHOUSE_USER",
		"CLICKHOUSE_PASSWORD",
		"CLICKHOUSE_VOLUME_NAME",
		"OTELCOL_VOLUME_NAME",
		"OTEL_DOCKER_LOGS_VOLUME_NAME",
		"STEALTH_TELEMETRY_STORE_NETWORK_NAME",
		"STEALTH_TELEMETRY_INGEST_NETWORK_NAME",
		"STEALTH_TELEMETRY_DOCKER_NETWORK_NAME",
	} {
		if strings.TrimSpace(values[key]) == "" {
			t.Fatalf("migration did not populate %s", key)
		}
	}
	if !installengine.FileIsPrivate(layout.EnvFile) {
		t.Fatal("migration changed config.env permissions")
	}
	version, err := os.ReadFile(layout.VersionFile)
	if err != nil || strings.TrimSpace(string(version)) != targetVersion {
		t.Fatalf("platform VERSION = %q, %v", version, err)
	}

	composeBytes, err := os.ReadFile(layout.ComposeFile)
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeBytes)
	for _, marker := range []string{"  telemetry-host:", "  telemetry-docker-logs:", "  telemetry-docker:", "  telemetry-docker-proxy:", "  telemetry_ingest:"} {
		if !strings.Contains(compose, marker) {
			t.Fatalf("target Compose is missing %q", marker)
		}
	}
	main := composeServiceSection(compose, "otel-collector", "telemetry-host")
	for _, forbidden := range []string{"/:/hostfs", "/var/lib/docker/containers", "/var/run/docker.sock", "DAC_READ_SEARCH"} {
		if strings.Contains(main, forbidden) {
			t.Fatalf("main Collector retained %q", forbidden)
		}
	}
	host := composeServiceSection(compose, "telemetry-host", "telemetry-docker-logs")
	if !strings.Contains(host, "/:/hostfs:ro") || strings.Contains(host, "DAC_READ_SEARCH") || strings.Contains(host, "/var/run/docker.sock") {
		t.Fatalf("host Collector boundary is incorrect: %s", host)
	}
	logs := composeServiceSection(compose, "telemetry-docker-logs", "telemetry-docker")
	if !strings.Contains(logs, "/var/lib/docker/containers:/hostfs/var/lib/docker/containers:ro") || strings.Contains(logs, "/:/hostfs") || strings.Contains(logs, "/var/run/docker.sock") || !strings.Contains(logs, "DAC_READ_SEARCH") {
		t.Fatalf("Docker log Collector boundary is incorrect: %s", logs)
	}
	dockerMetrics := composeServiceSection(compose, "telemetry-docker", "telemetry-docker-proxy")
	if strings.Contains(dockerMetrics, "/var/run/docker.sock") {
		t.Fatalf("Docker metrics Collector retained the Docker socket: %s", dockerMetrics)
	}
	proxy := composeServiceSection(compose, "telemetry-docker-proxy", "clickhouse")
	if !strings.Contains(proxy, "/var/run/docker.sock") {
		t.Fatalf("Docker proxy lost its Docker socket: %s", proxy)
	}
	for _, file := range []struct {
		path   string
		marker string
	}{
		{layout.TelemetryDir + "/otel-collector.yaml", "receivers:"},
		{layout.TelemetryDir + "/host-metrics.yaml", "hostmetrics:"},
		{layout.TelemetryDir + "/docker-logs.yaml", "file_log/docker:"},
		{layout.TelemetryDir + "/docker-stats.yaml", "docker_stats:"},
		{layout.ProxyFile, "server {"},
	} {
		contents, readErr := os.ReadFile(file.path)
		if readErr != nil || !strings.Contains(string(contents), file.marker) {
			t.Fatalf("target managed asset %s is invalid: %v", file.path, readErr)
		}
	}
}

func composeServiceSection(compose, service, _ string) string {
	start := strings.Index(compose, "\n  "+service+":")
	if start < 0 {
		return ""
	}
	start++
	end := len(compose)
	searchAt := start + len(service) + 3
	for searchAt < len(compose) {
		next := strings.Index(compose[searchAt:], "\n  ")
		if next < 0 {
			break
		}
		lineStart := searchAt + next + 1
		lineEnd := strings.IndexByte(compose[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(compose) - lineStart
		}
		line := compose[lineStart : lineStart+lineEnd]
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			end = lineStart
			break
		}
		searchAt = lineStart + lineEnd
	}
	return compose[start:end]
}

func splitHostPort(address string) (string, string, error) {
	lastColon := strings.LastIndexByte(address, ':')
	if lastColon <= 0 || lastColon == len(address)-1 {
		return "", "", fmt.Errorf("invalid TCP address %q", address)
	}
	return address[:lastColon], address[lastColon+1:], nil
}

type releaseAndHTTPTransport struct {
	secure http.RoundTripper
}

func (transport releaseAndHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme == "https" {
		return transport.secure.RoundTrip(request)
	}
	return http.DefaultTransport.RoundTrip(request)
}
