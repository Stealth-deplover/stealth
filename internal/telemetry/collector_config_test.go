package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerLogTimestampLayoutMatchesDockerJSON(t *testing.T) {
	const layout = "2006-01-02T15:04:05.999999999Z07:00"
	for _, sample := range []string{
		"2026-09-18T02:12:44Z",
		"2026-09-18T02:12:44.123Z",
		"2026-09-18T02:12:44.123456Z",
		"2026-09-18T02:12:44.123456789Z",
	} {
		parsed, err := time.Parse(layout, sample)
		if err != nil {
			t.Fatalf("Docker timestamp %q did not parse: %v", sample, err)
		}
		if parsed.UTC().Format(time.RFC3339Nano) != sample {
			t.Fatalf("parsed timestamp = %q, want %q", parsed.UTC().Format(time.RFC3339Nano), sample)
		}
	}
}

func TestCollectorDockerLogPipelinePreservesUnstructuredRecords(t *testing.T) {
	config := readRepositoryFile(t, "telemetry", "otel-collector.yaml")
	for _, expected := range []string{
		"layout_type: gotime",
		"layout: '2006-01-02T15:04:05.999999999Z07:00'",
		"id: stream-severity",
		"info: stdout",
		"warn: stderr",
		"id: application-json",
		"id: application-severity",
		"on_error: send",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("collector config is missing %q", expected)
		}
	}
	if strings.Contains(config, "layout: '%Y-") {
		t.Fatal("collector config still uses a strptime layout with gotime")
	}
}

func TestProductionComposeUsesScratchCompatibleCollectorChecksAndProxy(t *testing.T) {
	compose := readRepositoryFile(t, "compose.production.yaml")
	if !strings.Contains(compose, `test: ["CMD", "/otelcol-contrib", "validate", "--config=/etc/otelcol-contrib/config.yaml"]`) {
		t.Fatal("collector healthcheck is not exec-form against the published binary")
	}
	if strings.Contains(compose, "otelcol-contrib validate --config=/etc/otelcol-contrib/config.yaml >/dev/null") {
		t.Fatal("collector healthcheck regressed to shell syntax")
	}
	if !strings.Contains(compose, "otelcol-state-init:") || !strings.Contains(compose, "chown -R 10001:10001 /var/lib/otelcol") {
		t.Fatal("collector persistent-state ownership init is missing")
	}
	if !strings.Contains(compose, "telemetry-docker-proxy:") || !strings.Contains(compose, "/var/run/docker.sock:/var/run/docker.sock:ro") {
		t.Fatal("restricted Docker metrics proxy is missing")
	}
	if strings.Contains(serviceText(compose, "telemetry-docker"), "/var/run/docker.sock:/var/run/docker.sock") {
		t.Fatal("telemetry-docker directly mounts the Docker socket")
	}
}

func readRepositoryFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func serviceText(compose, service string) string {
	marker := "  " + service + ":"
	lines := strings.Split(compose, "\n")
	var builder strings.Builder
	started := false
	for _, line := range lines {
		if line == marker {
			started = true
		}
		if started && line != marker && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
			break
		}
		if started {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}
