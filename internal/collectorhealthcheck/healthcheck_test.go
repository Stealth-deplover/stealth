package collectorhealthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckAcceptsHealthyHTTPEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/" {
			t.Fatalf("health request = %s %s", request.Method, request.URL.Path)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := Check(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRejectsUnhealthyHTTPEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := Check(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("Check error = %v, want HTTP 503 error", err)
	}
}

func TestCheckRejectsNonHTTPEndpoint(t *testing.T) {
	if err := Check(context.Background(), "https://collector.example.test"); err == nil {
		t.Fatal("https endpoint unexpectedly accepted")
	}
}
