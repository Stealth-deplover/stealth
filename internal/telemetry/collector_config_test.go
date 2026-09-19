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
		"http_headers:",
		"layout_type: gotime",
		"layout: '2006-01-02T15:04:05.999999999Z07:00'",
		"id: stream-severity",
		"info: stdout",
		"warn: stderr",
		"id: application-json",
		"id: application-severity",
		"id: application-severity-alias",
		"on_error: send",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("collector config is missing %q", expected)
		}
	}
	if strings.Contains(config, "layout: '%Y-") {
		t.Fatal("collector config still uses a strptime layout with gotime")
	}
	if strings.Contains(config, "http_config:") {
		t.Fatal("Prometheus receiver config must use its inline http_headers field")
	}
}

func TestDockerStatsExplicitlyEnablesInfrastructureMetrics(t *testing.T) {
	config := readRepositoryFile(t, "telemetry", "docker-stats.yaml")
	for _, expected := range []string{
		"container.cpu.usage.total:",
		"container.memory.usage.total:",
		"container.network.io.usage.rx_bytes:",
		"container.blockio.io_service_bytes_recursive:",
		"container.state.status:",
		"container.state.health.status:",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("Docker stats config is missing %q", expected)
		}
	}
	if strings.Count(config, "enabled: true") < 6 {
		t.Fatal("Docker stats infrastructure metrics are not explicitly enabled")
	}
}

func TestProductionComposeUsesLiveScratchCompatibleCollectorCheckAndProxy(t *testing.T) {
	compose := readRepositoryFile(t, "compose.production.yaml")
	collector := serviceText(compose, "otel-collector")
	if !strings.Contains(collector, `test: ["CMD", "/usr/local/bin/telemetry-collector-healthcheck", "http://127.0.0.1:13133/"]`) {
		t.Fatal("collector healthcheck does not probe the live health endpoint")
	}
	if strings.Contains(collector, "CMD-SHELL") || strings.Contains(collector, "otelcol-contrib validate") {
		t.Fatal("collector healthcheck regressed to a shell or static config validation")
	}
	if !strings.Contains(readRepositoryFile(t, "telemetry", "otel-collector.yaml"), "health_check:") {
		t.Fatal("collector configuration must enable the health_check extension")
	}
	if !strings.Contains(readRepositoryFile(t, "telemetry", "otel-collector.yaml"), "host: 0.0.0.0") ||
		!strings.Contains(readRepositoryFile(t, "telemetry", "otel-collector.yaml"), "port: 8888") {
		t.Fatal("collector self-telemetry must be reachable on the private Compose network")
	}
	if strings.Contains(collector, "8888:") {
		t.Fatal("collector self-telemetry must not be published to the host")
	}
	if !strings.Contains(compose, `image: "${OTEL_DOCKER_COLLECTOR_IMAGE:-otel/opentelemetry-collector-contrib:0.161.0}"`) {
		t.Fatal("isolated Docker collector should use the upstream image independently")
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

	dockerfile := readRepositoryFile(t, "Dockerfile")
	if !strings.Contains(dockerfile, "AS telemetry-collector") || !strings.Contains(dockerfile, "telemetry-collector-healthcheck") {
		t.Fatal("Stealth Collector wrapper image is missing the live health probe")
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
