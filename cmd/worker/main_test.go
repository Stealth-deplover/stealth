package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/buildinfo"
	"github.com/Stealth-deplover/stealth/internal/cloudflareimport"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/repository"
)

type legacyCloudflareImportFake struct {
	statusCalls int
	markCalls   int
	importCalls int
	configured  bool
	input       repository.CloudflareConnectionInput
	reason      string
	markReason  string
}

func (f *legacyCloudflareImportFake) CloudflareRoutingStatus(context.Context) (domain.CloudflareRoutingStatus, error) {
	f.statusCalls++
	return domain.CloudflareRoutingStatus{Configured: f.configured}, nil
}

func (f *legacyCloudflareImportFake) MarkCloudflareConnectionUnavailable(_ context.Context, reason string) error {
	f.markCalls++
	f.markReason = reason
	return nil
}

func (f *legacyCloudflareImportFake) ImportCloudflareConnectionOnce(_ context.Context, input repository.CloudflareConnectionInput, reason string) (bool, error) {
	f.importCalls++
	f.input = input
	f.reason = reason
	return true, nil
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

func TestLegacyCloudflareImportMissingArtifactIsCleanNoop(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{7}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	store := &legacyCloudflareImportFake{}
	missingPath := filepath.Join(t.TempDir(), "missing", "cloudflare-import.enc")
	importLegacyCloudflareConnection(context.Background(), missingPath, cipher, store, logger)
	if store.statusCalls != 1 || store.markCalls != 0 || store.importCalls != 0 {
		t.Fatalf("missing snapshot caused import work: %#v", store)
	}
	if strings.Contains(logs.String(), "could not be loaded") || strings.Contains(logs.String(), "decrypted") {
		t.Fatalf("missing artifact logged a decryption/import warning: %s", logs.String())
	}
}

func TestLegacyCloudflareImportReadsNarrowArtifactOnce(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{8}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cloudflare-import.enc")
	envelope := cloudflareimport.Envelope{
		Version: cloudflareimport.Version, State: cloudflareimport.StateConnection,
		AccountID: "account-1", ConsoleZoneID: "zone-1", ConsoleHostname: "cloud.example.com",
		TunnelID: "tunnel-1", TunnelName: "stealth-prod", ConsoleRecordID: "record-1", APIToken: "SECRET-CLOUDFLARE-API",
	}
	ciphertext, err := cloudflareimport.Encrypt(envelope, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &legacyCloudflareImportFake{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	importLegacyCloudflareConnection(context.Background(), path, cipher, store, logger)
	if store.statusCalls != 1 || store.importCalls != 1 || store.markCalls != 0 {
		t.Fatalf("narrow artifact import calls = %#v", store)
	}
	if store.input.AccountID != envelope.AccountID || store.input.ConsoleZoneID != envelope.ConsoleZoneID || store.input.ConsoleHostname != envelope.ConsoleHostname || store.input.TunnelID != envelope.TunnelID || store.input.TunnelName != envelope.TunnelName || store.input.ConsoleRecordID != envelope.ConsoleRecordID || store.input.APIToken != envelope.APIToken {
		t.Fatalf("imported Cloudflare connection = %#v", store.input)
	}
	if strings.Contains(logs.String(), "SECRET-CLOUDFLARE-API") {
		t.Fatal("worker log contains the Cloudflare API token")
	}
}

func TestLegacyCloudflareImportMalformedOrUndecryptableArtifactIsSafe(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{9}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "malformed", data: func() []byte {
			data, err := cipher.Encrypt([]byte("SECRET-GITHUB-CLIENT"))
			if err != nil {
				t.Fatal(err)
			}
			return data
		}()},
		{name: "undecryptable", data: []byte("SECRET-CLOUDFLARE-API")},
		{name: "wrong key", data: func() []byte {
			other, err := functionsecret.New(bytes.Repeat([]byte{10}, functionsecret.KeySize))
			if err != nil {
				t.Fatal(err)
			}
			data, err := other.Encrypt([]byte(`{"version":1,"state":"reconnect_required"}`))
			if err != nil {
				t.Fatal(err)
			}
			return data
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cloudflare-import.enc")
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			store := &legacyCloudflareImportFake{}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			importLegacyCloudflareConnection(context.Background(), path, cipher, store, logger)
			if store.statusCalls != 1 || store.markCalls != 0 || store.importCalls != 0 {
				t.Fatalf("invalid artifact caused provider import mutation: %#v", store)
			}
			for _, marker := range []string{"SECRET-GITHUB-CLIENT", "SECRET-CLOUDFLARE-API"} {
				if strings.Contains(logs.String(), marker) {
					t.Fatal("worker log contains setup credential material")
				}
			}
		})
	}
}

func TestLegacyCloudflareImportRequiresReconnectForIncompleteState(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{11}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cloudflare-import.enc")
	ciphertext, err := cloudflareimport.Encrypt(cloudflareimport.Envelope{Version: cloudflareimport.Version, State: cloudflareimport.StateReconnectRequired}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &legacyCloudflareImportFake{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	importLegacyCloudflareConnection(context.Background(), path, cipher, store, logger)
	if store.statusCalls != 1 || store.markCalls != 1 || store.importCalls != 0 || !strings.Contains(store.markReason, "Instance Owner") {
		t.Fatalf("reconnect-required handling = %#v", store)
	}
}

func TestLegacyCloudflareImportDoesNotOverwriteConfiguredConnection(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{12}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store := &legacyCloudflareImportFake{configured: true}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	importLegacyCloudflareConnection(context.Background(), filepath.Join(t.TempDir(), "missing.enc"), cipher, store, logger)
	if store.statusCalls != 1 || store.markCalls != 0 || store.importCalls != 0 {
		t.Fatalf("configured connection was overwritten: %#v", store)
	}
}
