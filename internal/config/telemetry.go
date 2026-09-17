package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// telemetrySettings owns the optional OpenTelemetry exporter contract. An
// empty endpoint deliberately remains valid and means the application uses
// no-op spans without feature-specific branches in API or worker code.
type telemetrySettings struct {
	otlpEndpoint string
	serviceName  string
	sampleRatio  float64
}

func loadTelemetrySettings() (telemetrySettings, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return telemetrySettings{}, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must be an absolute HTTP(S) URL without query or fragment")
		}
	}
	sampleRatio, err := strconv.ParseFloat(value("OTEL_TRACES_SAMPLER_ARG", "0.1"), 64)
	if err != nil || sampleRatio < 0 || sampleRatio > 1 {
		return telemetrySettings{}, fmt.Errorf("OTEL_TRACES_SAMPLER_ARG must be a number between 0 and 1")
	}
	return telemetrySettings{
		otlpEndpoint: endpoint,
		serviceName:  value("OTEL_SERVICE_NAME", ""),
		sampleRatio:  sampleRatio,
	}, nil
}

func (s telemetrySettings) apply(c *Config) {
	c.TelemetryOTLPEndpoint = s.otlpEndpoint
	c.TelemetryServiceName = s.serviceName
	c.TelemetrySampleRatio = s.sampleRatio
}
