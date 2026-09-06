package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/config"
)

func TestProjectIDFromCORSPath(t *testing.T) {
	projectID := uuid.Must(uuid.NewV7())
	got, ok := projectIDFromCORSPath("/v1/projects/" + projectID.String() + "/realtime")
	if !ok || got != projectID {
		t.Fatalf("projectIDFromCORSPath() = %v, %v", got, ok)
	}
	if _, ok := projectIDFromCORSPath("/v1/organizations/" + projectID.String() + "/projects"); ok {
		t.Fatal("organization path was treated as a project path")
	}
}

func TestCORSMethodAllowed(t *testing.T) {
	for _, method := range []string{"GET", "post", "PATCH", "delete"} {
		if !corsMethodAllowed(method) {
			t.Errorf("corsMethodAllowed(%q) = false", method)
		}
	}
	for _, method := range []string{"", "PUT", "TRACE", "OPTIONS"} {
		if corsMethodAllowed(method) {
			t.Errorf("corsMethodAllowed(%q) = true", method)
		}
	}
}

func TestSetCORSHeadersAndDeniedRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/projects/example", nil)
	recorder := httptest.NewRecorder()
	setCORSHeaders(recorder, "https://app.example.com", request)
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "Content-Type, X-Trace-ID" {
		t.Fatalf("exposed headers = %q", got)
	}

	denied := httptest.NewRecorder()
	corsDenied(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("corsDenied status = %d", denied.Code)
	}
}

func TestConsoleCORSAllowsConfiguredManagementOrigin(t *testing.T) {
	server := &Server{config: config.Config{ConsoleCORSOrigins: []string{"http://localhost:5173"}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	preflight := httptest.NewRequest(http.MethodOptions, "/v1/account", nil)
	preflight.Header.Set("Origin", "http://localhost:5173")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type,idempotency-key")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("preflight status=%d allow-origin=%q", preflightResponse.Code, preflightResponse.Header().Get("Access-Control-Allow-Origin"))
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/018f27e3-5d1a-7c44-ae35-1db4ea12e6d2", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("configured console request status=%d credentials=%q", response.Code, response.Header().Get("Access-Control-Allow-Credentials"))
	}
}

func TestConsoleCORSRejectsUnconfiguredManagementOrigin(t *testing.T) {
	server := &Server{config: config.Config{ConsoleCORSOrigins: []string{"http://localhost:5173"}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("next handler called for denied origin") }))
	request := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	request.Header.Set("Origin", "https://evil.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("denied management request status=%d", response.Code)
	}
}
