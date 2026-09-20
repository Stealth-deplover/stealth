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

func TestDockerLogPipelinePreservesUnstructuredRecords(t *testing.T) {
	config := readRepositoryFile(t, "telemetry", "docker-logs.yaml")
	for _, expected := range []string{
		"poll_interval: 200ms",
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
	if strings.Contains(configSection(config, "receivers:", "processors:"), "\n  otlp:") {
		t.Fatal("Docker log collector must not expose an OTLP receiver")
	}
}

func TestMainCollectorDoesNotOwnHostCollectors(t *testing.T) {
	config := readRepositoryFile(t, "telemetry", "otel-collector.yaml")
	for _, forbidden := range []string{"hostmetrics:", "file_log/docker:", "/hostfs"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("main collector still contains %q", forbidden)
		}
	}
}

func TestMainCollectorRedactsSignalsBeforeClickHouse(t *testing.T) {
	config := readRepositoryFile(t, "telemetry", "otel-collector.yaml")
	for _, expected := range []string{
		"redaction/telemetry:",
		"transform/telemetry:",
		"blocked_key_patterns:",
		"blocked_values:",
		"summary: silent",
		"context: span",
		"context: spanevent",
		"context: metric",
		"replace_pattern(span.status.message",
		"replace_pattern(span.name",
		"replace_pattern(metric.description",
		"exporters: [clickhouse]",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("main collector redaction contract is missing %q", expected)
		}
	}
	for _, pipeline := range []string{
		"processors: [memory_limiter, redaction/telemetry, transform/telemetry, resource, batch]",
	} {
		if strings.Count(config, pipeline) != 3 {
			t.Fatalf("expected all three ClickHouse pipelines to use %q exactly three times", pipeline)
		}
	}
}

func TestHostMetricsCollectorUsesReadOnlyHostRootWithoutOTLPReceiver(t *testing.T) {
	config := readRepositoryFile(t, "telemetry", "host-metrics.yaml")
	if !strings.Contains(config, "hostmetrics:") || !strings.Contains(config, "root_path: /hostfs") {
		t.Fatal("host metrics collector is missing its hostmetrics root path")
	}
	if strings.Contains(configSection(config, "receivers:", "processors:"), "\n  otlp:") {
		t.Fatal("host metrics collector must not expose an OTLP receiver")
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
	for _, forbidden := range []string{"/:/hostfs", "/var/lib/docker/containers", "DAC_READ_SEARCH", "/var/run/docker.sock"} {
		if strings.Contains(collector, forbidden) {
			t.Fatalf("main Collector still contains %q", forbidden)
		}
	}
	if !strings.Contains(collector, "telemetry_ingest:") {
		t.Fatal("main Collector must join the private telemetry ingest network")
	}
	hostMetrics := serviceText(compose, "telemetry-host")
	if !strings.Contains(hostMetrics, "/:/hostfs:ro") || strings.Contains(hostMetrics, "DAC_READ_SEARCH") || strings.Contains(hostMetrics, "/var/run/docker.sock") {
		t.Fatal("host metrics Collector privilege boundary is incorrect")
	}
	if !strings.Contains(hostMetrics, "networks: [telemetry_ingest]") || strings.Contains(hostMetrics, "telemetry_store") || strings.Contains(hostMetrics, "telemetry_docker") {
		t.Fatal("host metrics Collector network boundary is incorrect")
	}
	dockerLogs := serviceText(compose, "telemetry-docker-logs")
	if !strings.Contains(dockerLogs, "/var/lib/docker/containers:/hostfs/var/lib/docker/containers:ro") || strings.Contains(dockerLogs, "/:/hostfs") || strings.Contains(dockerLogs, "/var/run/docker.sock") {
		t.Fatal("Docker log Collector mount boundary is incorrect")
	}
	if !strings.Contains(dockerLogs, "networks: [telemetry_ingest]") || strings.Contains(dockerLogs, "telemetry_store") || strings.Contains(dockerLogs, "telemetry_docker") {
		t.Fatal("Docker log Collector network boundary is incorrect")
	}
	dockerMetrics := serviceText(compose, "telemetry-docker")
	if !strings.Contains(dockerMetrics, "telemetry_ingest:") || !strings.Contains(dockerMetrics, "telemetry_docker:") || strings.Contains(dockerMetrics, "telemetry_store") {
		t.Fatal("Docker metrics Collector network boundary is incorrect")
	}
	if !strings.Contains(compose, `image: "${OTEL_DOCKER_COLLECTOR_IMAGE:-ghcr.io/stealth-deplover/stealth-otel-collector:v0.2.2}"`) {
		t.Fatal("isolated Docker metrics collector should use the capability-free wrapper image")
	}
	for _, service := range []string{"telemetry-host", "telemetry-docker-logs"} {
		block := serviceText(compose, service)
		if !strings.Contains(block, `test: ["CMD", "/usr/local/bin/telemetry-collector-healthcheck", "http://127.0.0.1:13133/"]`) {
			t.Fatalf("%s does not probe the live health endpoint", service)
		}
	}
	if !strings.Contains(compose, `image: "${OTEL_DOCKER_LOGS_COLLECTOR_IMAGE:-ghcr.io/stealth-deplover/stealth-otel-docker-logs:v0.2.2}"`) {
		t.Fatal("Docker log collector should use its dedicated image")
	}
	if !strings.Contains(compose, "otelcol-state-init:") || !strings.Contains(compose, "chown -R 10001:10001 /var/lib/otelcol") {
		t.Fatal("collector persistent-state ownership init is missing")
	}
	if !strings.Contains(compose, "telemetry-docker-logs-state-init:") {
		t.Fatal("Docker log collector persistent-state ownership init is missing")
	}
	if !strings.Contains(compose, "telemetry-docker-proxy:") || !strings.Contains(compose, "/var/run/docker.sock:/var/run/docker.sock:ro") {
		t.Fatal("restricted Docker metrics proxy is missing")
	}
	if strings.Contains(serviceText(compose, "telemetry-docker"), "/var/run/docker.sock:/var/run/docker.sock") {
		t.Fatal("telemetry-docker directly mounts the Docker socket")
	}
	if !strings.Contains(compose, "telemetry_ingest:\n    name: \"${STEALTH_TELEMETRY_INGEST_NETWORK_NAME:-stealth_telemetry_ingest}\"\n    internal: true") {
		t.Fatal("private telemetry ingest network definition is missing")
	}

	dockerfile := readRepositoryFile(t, "Dockerfile")
	if !strings.Contains(dockerfile, "FROM scratch AS telemetry-collector") || !strings.Contains(dockerfile, "FROM scratch AS telemetry-docker-logs") || !strings.Contains(dockerfile, "telemetry-collector-healthcheck") {
		t.Fatal("Stealth Collector wrapper image targets are missing the live health probe")
	}
	mainImage := dockerfile[strings.Index(dockerfile, "FROM scratch AS telemetry-collector"):]
	if strings.Contains(mainImage[:strings.Index(mainImage, "FROM alpine:3.24 AS telemetry-docker-logs-capability")], "cap_dac_read_search") {
		t.Fatal("main Collector image target carries the Docker log capability")
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

func configSection(config, header, nextHeader string) string {
	start := strings.Index(config, header)
	if start == -1 {
		return ""
	}
	section := config[start:]
	if end := strings.Index(section, "\n"+nextHeader); end != -1 {
		section = section[:end]
	}
	return section
}
