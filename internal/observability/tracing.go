package observability

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	apiInstrumentationName    = "github.com/nazxf/stealth-api"
	workerInstrumentationName = "github.com/nazxf/stealth-api/worker"
)

// TracerConfig controls the optional OTLP HTTP exporter. An empty endpoint is
// intentional: local development and unit tests retain zero-cost no-op spans
// while production can enable tracing by setting one environment variable.
type TracerConfig struct {
	Endpoint    string
	ServiceName string
	SampleRatio float64
}

// HTTPTraceRecord is the bounded root-request observation handed to an
// optional persistence callback. Tenant identity is deliberately not part of
// this package: the HTTP server resolves route scopes after authorization.
type HTTPTraceRecord struct {
	TraceID       string
	SpanID        string
	Method        string
	Route         string
	Status        int
	ResponseBytes int64
	Duration      time.Duration
	StartedAt     time.Time
	FinishedAt    time.Time
}

// HTTPTraceRecorder receives a completed request observation. Implementations
// must treat failures as best-effort because the HTTP response is already
// committed when the callback runs.
type HTTPTraceRecorder func(context.Context, HTTPTraceRecord)

// Setup installs a process-wide OpenTelemetry provider and the W3C Trace
// Context/Baggage propagator. It never contacts the collector during startup;
// the OTLP exporter sends asynchronously through the batch processor. The
// returned shutdown function should be called during graceful termination so
// the final batch is flushed.
func Setup(ctx context.Context, cfg TracerConfig) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = "stealth"
	}
	ratio := cfg.SampleRatio
	if ratio < 0 || ratio > 1 {
		return nil, &invalidTracerConfig{message: "sample ratio must be between 0 and 1"}
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		// Do not replace an SDK provider that an embedding process may have
		// installed already. The default provider is no-op; this branch is
		// primarily for the standalone API and worker binaries.
		return func(context.Context) error { return nil }, nil
	}
	normalized, insecure, err := normalizeOTLPEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	exporterOptions := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(normalized)}
	if insecure {
		exporterOptions = append(exporterOptions, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		return nil, err
	}
	serviceResource, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			attribute.String("service.namespace", "stealth"),
		),
	)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(serviceResource),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(2*time.Second),
			sdktrace.WithExportTimeout(10*time.Second),
			sdktrace.WithMaxQueueSize(4096),
			sdktrace.WithMaxExportBatchSize(512),
		),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

type invalidTracerConfig struct{ message string }

func (e *invalidTracerConfig) Error() string {
	return "invalid OpenTelemetry configuration: " + e.message
}

func normalizeOTLPEndpoint(raw string) (string, bool, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false, &invalidTracerConfig{message: "OTLP endpoint must be an absolute HTTP(S) URL without query or fragment"}
	}
	if strings.TrimSuffix(parsed.Path, "/") == "" {
		parsed.Path = "/v1/traces"
	}
	return parsed.String(), parsed.Scheme == "http", nil
}

// Tracer returns an instrumentation-scoped tracer. Keeping the names stable
// gives Grafana/Tempo operators a reliable filter even when routes change.
func Tracer(scope string) trace.Tracer {
	if strings.TrimSpace(scope) == "" {
		scope = apiInstrumentationName
	}
	return otel.Tracer(scope)
}

// HTTPMiddleware creates a server span for every API request. The incoming
// W3C traceparent is extracted before the span starts, and only low-cardinality
// route templates are used for the final span name and http.route attribute.
// Request payloads, query strings, cookies, and authorization headers are
// intentionally never attached to spans.
func HTTPMiddleware(next http.Handler) http.Handler {
	return HTTPMiddlewareWithRecorder(nil)(next)
}

// HTTPMiddlewareWithRecorder creates the request middleware with an optional
// durable root-trace callback. Keeping HTTPMiddleware as a wrapper preserves
// the small, dependency-free middleware used by package tests and embedders.
func HTTPMiddlewareWithRecorder(recorder HTTPTraceRecorder) func(http.Handler) http.Handler {
	tracer := Tracer(apiInstrumentationName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			startedAt := time.Now()
			parent := otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header))
			ctx, span := tracer.Start(parent, request.Method, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(request.Method),
			))
			response := &traceResponseWriter{ResponseWriter: writer, status: http.StatusOK}
			traceID := span.SpanContext().TraceID()
			traceIDValue := ""
			if traceID.IsValid() {
				traceIDValue = traceID.String()
			} else {
				traceIDValue = newTraceID()
			}
			if traceIDValue != "" {
				response.Header().Set("X-Trace-ID", traceIDValue)
			}
			next.ServeHTTP(response, request.WithContext(ctx))
			finishedAt := time.Now()
			route := "unmatched"
			if routeContext := chi.RouteContext(request.Context()); routeContext != nil {
				if pattern := strings.TrimSpace(routeContext.RoutePattern()); pattern != "" {
					route = pattern
				}
			}
			span.SetName(request.Method + " " + route)
			span.SetAttributes(
				semconv.HTTPRouteKey.String(route),
				semconv.HTTPResponseStatusCodeKey.Int(response.status),
				semconv.HTTPResponseBodySizeKey.Int64(response.bytes),
			)
			if response.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(response.status))
			} else {
				span.SetStatus(codes.Ok, "")
			}
			span.End()
			if recorder != nil && traceIDValue != "" {
				spanIDValue := ""
				if spanID := span.SpanContext().SpanID(); spanID.IsValid() {
					spanIDValue = spanID.String()
				}
				recorder(request.Context(), HTTPTraceRecord{
					TraceID: traceIDValue, SpanID: spanIDValue, Method: request.Method, Route: route,
					Status: response.status, ResponseBytes: response.bytes, Duration: finishedAt.Sub(startedAt),
					StartedAt: startedAt, FinishedAt: finishedAt,
				})
			}
		})
	}
}

func newTraceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(value[:])
}

// StartWorkerSpan is a small helper used by queue workers. It keeps worker
// spans correlated with any context supplied by a future queue transport and
// adds fixed operation metadata without tenant IDs or user payloads.
func StartWorkerSpan(ctx context.Context, operation string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer(workerInstrumentationName).Start(ctx, operation, trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attrs...))
}

type traceResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *traceResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *traceResponseWriter) Write(value []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(value)
	w.bytes += int64(n)
	return n, err
}

func (w *traceResponseWriter) ReadFrom(source io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(source)
		w.bytes += n
		return n, err
	}
	n, err := io.Copy(w.ResponseWriter, source)
	w.bytes += n
	return n, err
}

func (w *traceResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *traceResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *traceResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *traceResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}
