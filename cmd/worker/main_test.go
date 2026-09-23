package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/buildinfo"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/repository"
)

type legacyCloudflareImportFake struct {
	statusCalls int
	markCalls   int
	importCalls int
	configured  bool
}

func (f *legacyCloudflareImportFake) CloudflareRoutingStatus(context.Context) (domain.CloudflareRoutingStatus, error) {
	f.statusCalls++
	return domain.CloudflareRoutingStatus{Configured: f.configured}, nil
}

func (f *legacyCloudflareImportFake) MarkCloudflareConnectionUnavailable(context.Context, string) error {
	f.markCalls++
	return nil
}

func (f *legacyCloudflareImportFake) ImportCloudflareConnectionOnce(context.Context, repository.CloudflareConnectionInput, string) (bool, error) {
	f.importCalls++
	return false, nil
}

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

	version := httptest.NewRecorder()
	handler.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/version", nil))
	if version.Code != http.StatusOK || !strings.Contains(version.Body.String(), `"version":"`+buildinfo.Version+`"`) {
		t.Fatalf("version response = status %d body %q, want build metadata", version.Code, version.Body.String())
	}

	probe := httptest.NewRecorder()
	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRequest.Header.Set("X-Metrics-Token", "metrics-test-token")
	handler.ServeHTTP(probe, metricsRequest)
	if probe.Code != http.StatusTeapot || !strings.Contains(probe.Body.String(), "metrics") {
		t.Fatalf("metrics response = status %d body %q, want delegated handler", probe.Code, probe.Body.String())
	}
}

func TestLegacyCloudflareImportMissingSnapshotIsCleanNoop(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{7}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	store := &legacyCloudflareImportFake{}
	missingPath := filepath.Join(t.TempDir(), "missing", "setup-state.enc")
	importLegacyCloudflareConnection(context.Background(), missingPath, cipher, store, logger)
	if store.statusCalls != 1 || store.markCalls != 0 || store.importCalls != 0 {
		t.Fatalf("missing snapshot caused import work: %#v", store)
	}
	if strings.Contains(logs.String(), "could not be imported") || strings.Contains(logs.String(), "decryption") {
		t.Fatalf("missing snapshot logged a decryption/import warning: %s", logs.String())
	}
}
