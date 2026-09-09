package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerMetricsHandlerExposesHealthAndMetrics(t *testing.T) {
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "metrics")
	})
	handler := workerMetricsHandler(metrics, "metrics-test-token")

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != `{"status":"ok"}` {
		t.Fatalf("health response = status %d body %q, want 200 and ok payload", health.Code, health.Body.String())
	}

	probe := httptest.NewRecorder()
	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRequest.Header.Set("X-Metrics-Token", "metrics-test-token")
	handler.ServeHTTP(probe, metricsRequest)
	if probe.Code != http.StatusTeapot || !strings.Contains(probe.Body.String(), "metrics") {
		t.Fatalf("metrics response = status %d body %q, want delegated handler", probe.Code, probe.Body.String())
	}
}
