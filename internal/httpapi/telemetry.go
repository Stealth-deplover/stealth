package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/repository"
)

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered", "path", r.URL.Path, "panic", fmt.Sprint(recovered))
				writeError(w, 500, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := newMetricsResponseWriter(w)
		if s.metrics != nil {
			s.metrics.InFlight.Inc()
			defer s.metrics.InFlight.Dec()
		}
		next.ServeHTTP(recorder, r)
		duration := time.Since(started)
		route := metricRoute(r)
		status := strconv.Itoa(recorder.Status())
		if s.metrics != nil {
			s.metrics.Requests.WithLabelValues(r.Method, route, status).Inc()
			s.metrics.RequestDuration.WithLabelValues(r.Method, route).Observe(duration.Seconds())
			s.metrics.ResponseBytes.WithLabelValues(r.Method, route, status).Add(float64(recorder.BytesWritten()))
		}
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "route", route, "status", recorder.Status(), "bytes", recorder.BytesWritten(), "duration", duration.String())
	})
}

// recordHTTPTrace stores a tenant-scoped root request index after the
// response has been selected. Full nested spans stay in the private OTLP
// backend; a persistence failure is observable but never changes the caller's
// already-written response.
func (s *Server) recordHTTPTrace(requestContext context.Context, observation observability.HTTPTraceRecord) {
	// Authentication and authorization failures do not belong to the tenant's
	// trace index. Apart from avoiding noisy rows, this prevents an outsider
	// from manufacturing organization-scoped observations through a guessed
	// route identifier.
	if s.repo == nil || observation.TraceID == "" || observation.Status == http.StatusUnauthorized || observation.Status == http.StatusForbidden {
		return
	}
	routeContext := chi.RouteContext(requestContext)
	if routeContext == nil {
		return
	}
	var organizationID, projectID, accountID *uuid.UUID
	if raw := strings.TrimSpace(routeContext.URLParam("organizationID")); raw != "" {
		parsed, err := repository.ParseUUID(raw)
		if err != nil {
			return
		}
		organizationID = &parsed
	}
	if raw := strings.TrimSpace(routeContext.URLParam("projectID")); raw != "" {
		parsed, err := repository.ParseUUID(raw)
		if err != nil {
			return
		}
		projectID = &parsed
	}
	if account, ok := requestContext.Value(accountContextKey).(domain.Account); ok {
		parsed, err := repository.ParseUUID(account.ID)
		if err == nil {
			accountID = &parsed
		}
	}
	if organizationID == nil && projectID == nil {
		return
	}
	traceID, err := uuid.NewV7()
	if err != nil {
		s.logger.Warn("trace index id generation failed", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.repo.RecordHTTPTrace(ctx, traceID, repository.HTTPTraceInput{
		TraceID: observation.TraceID, SpanID: observation.SpanID, OrganizationID: organizationID,
		ProjectID: projectID, AccountID: accountID, Method: observation.Method, Route: observation.Route,
		Status: observation.Status, Duration: observation.Duration, ResponseBytes: observation.ResponseBytes,
		StartedAt: observation.StartedAt, FinishedAt: observation.FinishedAt,
	}); err != nil {
		s.logger.Warn("trace index write failed", "error", err, "route", observation.Route)
	}
}

// metricRoute obtains Chi's route template only after the handler has run.
// This prevents UUIDs, object names, and arbitrary 404 paths from becoming
// Prometheus label values. A missing template has one stable fallback.
func metricRoute(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := strings.TrimSpace(routeContext.RoutePattern()); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

// metricsResponseWriter retains the standard optional ResponseWriter
// interfaces used by streaming handlers while recording the final HTTP
// status and body bytes for Prometheus. Its Unwrap method also keeps it
// compatible with net/http ResponseController.
type metricsResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func newMetricsResponseWriter(writer http.ResponseWriter) *metricsResponseWriter {
	return &metricsResponseWriter{ResponseWriter: writer, status: http.StatusOK}
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(value []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(value)
	w.bytes += int64(n)
	return n, err
}

func (w *metricsResponseWriter) ReadFrom(source io.Reader) (int64, error) {
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

func (w *metricsResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *metricsResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *metricsResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *metricsResponseWriter) Status() int { return w.status }

func (w *metricsResponseWriter) BytesWritten() int64 { return w.bytes }
