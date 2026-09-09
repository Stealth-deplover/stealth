package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDPreservesValidIncomingValue(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(requestIDHeader, "edge-123.test")
	recorder := httptest.NewRecorder()
	server.requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestIDFrom(r.Context()); got != "edge-123.test" {
			t.Fatalf("request ID in context = %q", got)
		}
	})).ServeHTTP(recorder, request)
	if got := recorder.Header().Get(requestIDHeader); got != "edge-123.test" {
		t.Fatalf("response request ID = %q", got)
	}
}

func TestRequestIDReplacesUnsafeOrOversizedValue(t *testing.T) {
	server := &Server{}
	for _, value := range []string{"bad value", "bad\nvalue", strings.Repeat("x", 129)} {
		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		request.Header.Set(requestIDHeader, value)
		recorder := httptest.NewRecorder()
		server.requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !validRequestID(requestIDFrom(r.Context())) {
				t.Fatalf("generated request ID is unsafe: %q", requestIDFrom(r.Context()))
			}
		})).ServeHTTP(recorder, request)
		if got := recorder.Header().Get(requestIDHeader); !validRequestID(got) || got == value {
			t.Fatalf("replacement request ID = %q for input %q", got, value)
		}
	}
}
