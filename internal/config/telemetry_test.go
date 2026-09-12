package config

import (
	"strings"
	"testing"
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
