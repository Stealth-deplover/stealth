package dockermetricsproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowedPathOnlyExposesDockerStatsReadSurface(t *testing.T) {
	tests := []struct {
		path  string
		allow bool
	}{
		{path: "/_ping", allow: true},
		{path: "/v1.44/version", allow: true},
		{path: "/containers/json", allow: true},
		{path: "/v1.44/containers/0123456789ab/stats", allow: true},
		{path: "/containers/0123456789ab/json", allow: true},
		{path: "/events", allow: true},
		{path: "/containers/0123456789ab/exec", allow: false},
		{path: "/containers/0123456789ab/start", allow: false},
		{path: "/images/json", allow: false},
		{path: "/containers/not-a-container/stats", allow: false},
	}
	for _, test := range tests {
		_, _, got := allowedPath(test.path)
		if got != test.allow {
			t.Fatalf("allowedPath(%q) = %v, want %v", test.path, got, test.allow)
		}
	}
}

func TestAllowedQueryRejectsMutationAndCredentialLikeParameters(t *testing.T) {
	if _, kind, ok := allowedPath("/containers/0123456789ab/stats"); !ok || !allowedQuery(kind, url.Values{"stream": {"false"}}) {
		t.Fatal("normal stats query was rejected")
	}
	if _, kind, ok := allowedPath("/containers/0123456789ab/stats"); !ok || allowedQuery(kind, url.Values{"exec": {"true"}}) {
		t.Fatal("unexpected stats query was accepted")
	}
}

func TestSanitizeInspectRemovesContainerSecrets(t *testing.T) {
	input := []byte(`{"Id":"0123456789abcdef","Config":{"Image":"api:v1","Cmd":["--token","secret"],"Env":["PASSWORD=secret"],"Labels":{"com.docker.compose.service":"api","secret":"do-not-forward"}},"State":{"Status":"running","StartedAt":"2026-09-18T02:12:44.123456789Z","Health":{"Status":"healthy"}},"HostConfig":{"CPUShares":1024,"CPUQuota":100000,"Binds":["/host:/container"]}}`)
	output, err := sanitizeInspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "secret") || strings.Contains(string(output), "Binds") {
		t.Fatalf("sanitized inspect response leaked sensitive fields: %s", output)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded["State"]), "healthy") || !strings.Contains(string(decoded["HostConfig"]), "CPUShares") {
		t.Fatalf("sanitized inspect response lost metrics fields: %s", output)
	}
}

func TestSanitizeContainersRemovesMountsAndUntrustedLabels(t *testing.T) {
	input := []byte(`[{"Id":"0123456789abcdef","Names":["/api"],"Image":"api:v1","Labels":{"com.docker.compose.service":"api","secret":"do-not-forward"},"Mounts":[{"Source":"/host","Destination":"/app"}],"NetworkSettings":{"Networks":{"default":{}}}}]`)
	output, err := sanitizeContainers(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "do-not-forward") || strings.Contains(string(output), "Mounts") || strings.Contains(string(output), "NetworkSettings") {
		t.Fatalf("sanitized container list leaked fields: %s", output)
	}
	if !strings.Contains(string(output), "com.docker.compose.service") {
		t.Fatalf("sanitized container list lost required Compose label: %s", output)
	}
}

func TestProxyRejectsMutationBeforeTouchingSocket(t *testing.T) {
	handler := NewHandler(Config{SocketPath: "/path/that/does/not/exist"})
	request := httptest.NewRequest(http.MethodPost, "http://proxy/containers/0123456789ab/start", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("mutation status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestProxyHealthFailsWhenSocketUnavailable(t *testing.T) {
	handler := NewHandler(Config{SocketPath: "/path/that/does/not/exist"})
	request := httptest.NewRequest(http.MethodGet, "http://proxy/healthz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestProxyForwardsDockerStatsThroughUnixSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1.44/containers/0123456789ab/stats" || request.URL.Query().Get("stream") != "false" {
			t.Fatalf("upstream request = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"cpu_stats":{"cpu_usage":{"total_usage":7}},"memory_stats":{"usage":11}}`)
	})}
	go func() { _ = upstream.Serve(listener) }()
	t.Cleanup(func() {
		_ = upstream.Shutdown(context.Background())
	})

	handler := NewHandler(Config{SocketPath: socketPath})
	request := httptest.NewRequest(http.MethodGet, "http://proxy/v1.44/containers/0123456789ab/stats?stream=false", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stats status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"total_usage":7`) {
		t.Fatalf("stats body = %s", recorder.Body.String())
	}
}

func TestHealthcheckUsesHTTPEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			t.Fatalf("health path = %q", request.URL.Path)
		}
		_, _ = io.WriteString(writer, "ok\n")
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if err := Healthcheck(context.Background(), address); err != nil {
		t.Fatal(err)
	}
}
