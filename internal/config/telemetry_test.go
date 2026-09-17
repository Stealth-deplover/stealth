package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadTelemetrySettings(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://tempo.example.test:4318")
	t.Setenv("OTEL_SERVICE_NAME", "stealth-api")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")

	settings, err := loadTelemetrySettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.otlpEndpoint != "http://tempo.example.test:4318" || settings.serviceName != "stealth-api" || settings.sampleRatio != 0.25 {
		t.Fatalf("unexpected telemetry settings: %+v", settings)
	}
	var config Config
	settings.apply(&config)
	if config.TelemetryOTLPEndpoint != settings.otlpEndpoint || config.TelemetrySampleRatio != settings.sampleRatio {
		t.Fatalf("settings were not applied: %+v", config)
	}
}

func TestLoadTelemetrySettingsAllowsDisabledExporter(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	settings, err := loadTelemetrySettings()
	if err != nil || settings.otlpEndpoint != "" {
		t.Fatalf("empty telemetry endpoint returned settings=%+v err=%v", settings, err)
	}
}

func TestLoadTelemetrySettingsRejectsUnsafeValues(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "tempo.example.test:4318")
	if _, err := loadTelemetrySettings(); err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Fatalf("invalid endpoint returned %v", err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://tempo.example.test:4318")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.1")
	if _, err := loadTelemetrySettings(); err == nil || !strings.Contains(err.Error(), "OTEL_TRACES_SAMPLER_ARG") {
		t.Fatalf("invalid sample ratio returned %v", err)
	}
}

func TestLoadTelemetryStoreSettings(t *testing.T) {
	t.Setenv("CLICKHOUSE_ADDR", "clickhouse.example.test:9000,[::1]:9001")
	t.Setenv("CLICKHOUSE_DATABASE", "telemetry_v2")
	t.Setenv("CLICKHOUSE_USER", "stealth_reader")
	t.Setenv("CLICKHOUSE_PASSWORD", "not-logged")
	t.Setenv("OTEL_COLLECTOR_HEALTH_URL", "http://otel-collector:13133")
	t.Setenv("TELEMETRY_MAX_QUERY_DURATION", "15s")
	t.Setenv("TELEMETRY_MAX_QUERY_RANGE", "48h")
	t.Setenv("TELEMETRY_MAX_QUERY_ROWS", "2500")
	t.Setenv("TELEMETRY_RETENTION", "168h")

	settings, err := loadTelemetryStoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.addr != "clickhouse.example.test:9000,[::1]:9001" || settings.database != "telemetry_v2" || settings.user != "stealth_reader" || settings.collectorHealthURL != "http://otel-collector:13133" || settings.maxDuration != 15*time.Second || settings.maxRange != 48*time.Hour || settings.maxRows != 2500 || settings.retention != 168*time.Hour {
		t.Fatalf("unexpected ClickHouse settings: %+v", settings)
	}
}

func TestLoadTelemetryStoreSettingsRejectsUnsafeValues(t *testing.T) {
	t.Setenv("CLICKHOUSE_ADDR", "clickhouse.example.test:9000;DROP TABLE")
	if _, err := loadTelemetryStoreSettings(); err == nil || !strings.Contains(err.Error(), "CLICKHOUSE_ADDR") {
		t.Fatalf("unsafe address returned %v", err)
	}
	t.Setenv("CLICKHOUSE_ADDR", "clickhouse.example.test:9000")
	t.Setenv("OTEL_COLLECTOR_HEALTH_URL", "http://otel-collector:13133")
	t.Setenv("CLICKHOUSE_DATABASE", "telemetry;drop")
	if _, err := loadTelemetryStoreSettings(); err == nil || !strings.Contains(err.Error(), "CLICKHOUSE_DATABASE") {
		t.Fatalf("unsafe database returned %v", err)
	}
	t.Setenv("CLICKHOUSE_DATABASE", "stealth_telemetry")
	t.Setenv("TELEMETRY_MAX_QUERY_ROWS", "10001")
	if _, err := loadTelemetryStoreSettings(); err == nil || !strings.Contains(err.Error(), "TELEMETRY_MAX_QUERY_ROWS") {
		t.Fatalf("unsafe row limit returned %v", err)
	}
	t.Setenv("TELEMETRY_MAX_QUERY_ROWS", "1000")
	t.Setenv("OTEL_COLLECTOR_HEALTH_URL", "otel-collector:13133")
	if _, err := loadTelemetryStoreSettings(); err == nil || !strings.Contains(err.Error(), "OTEL_COLLECTOR_HEALTH_URL") {
		t.Fatalf("unsafe collector health URL returned %v", err)
	}
}
