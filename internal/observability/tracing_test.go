package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

func TestHTTPMiddlewareExtractsParentAndUsesRouteTemplate(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter), trace.WithSampler(trace.AlwaysSample()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	})

	router := chi.NewRouter()
	router.Use(HTTPMiddleware)
	router.Get("/v1/projects/{projectID}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/projects/0192c0f4-0d7d-7f01-8f51-4d4c4b2e2f02", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", response.Code)
	}
	if got := response.Header().Get("X-Trace-ID"); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("X-Trace-ID = %q", got)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	if spans[0].Name != "GET /v1/projects/{projectID}" {
		t.Fatalf("span name = %q", spans[0].Name)
	}
	if spans[0].Parent.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("parent trace id = %s", spans[0].Parent.TraceID())
	}
	if spans[0].Status.Code.String() != "Ok" {
		t.Fatalf("span status = %s", spans[0].Status.Code)
	}
}

func TestNormalizeOTLPEndpoint(t *testing.T) {
	endpoint, insecure, err := normalizeOTLPEndpoint("http://tempo:4318")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://tempo:4318/v1/traces" || !insecure {
		t.Fatalf("normalized endpoint = %q insecure=%v", endpoint, insecure)
	}
	if _, _, err := normalizeOTLPEndpoint("tempo:4318"); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
}
