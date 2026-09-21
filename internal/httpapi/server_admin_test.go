package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
)

type fakeAdminTelemetryStore struct {
	logsCalled   bool
	logsQuery    telemetry.LogsQuery
	logsQueries  []telemetry.LogsQuery
	logs         telemetry.LogsResult
	logsResults  []telemetry.LogsResult
	logsErr      error
	logsStarted  chan struct{}
	logsHook     func(int, telemetry.LogsQuery)
	tracesCalled bool
	tracesQuery  telemetry.TracesQuery
}

func (f *fakeAdminTelemetryStore) Ping(context.Context) error { return nil }

func (f *fakeAdminTelemetryStore) QueryLogs(_ context.Context, query telemetry.LogsQuery) (telemetry.LogsResult, error) {
	f.logsCalled = true
	f.logsQuery = query
	call := len(f.logsQueries)
	f.logsQueries = append(f.logsQueries, query)
	if f.logsHook != nil {
		f.logsHook(call, query)
	}
	if f.logsStarted != nil {
		select {
		case f.logsStarted <- struct{}{}:
		default:
		}
	}
	if call < len(f.logsResults) {
		return f.logsResults[call], f.logsErr
	}
	return f.logs, f.logsErr
}

func (f *fakeAdminTelemetryStore) QueryTraces(_ context.Context, query telemetry.TracesQuery) (telemetry.TracesResult, error) {
	f.tracesCalled = true
	f.tracesQuery = query
	return telemetry.TracesResult{}, nil
}

func (f *fakeAdminTelemetryStore) QueryMetrics(context.Context, telemetry.MetricsQuery) (telemetry.MetricsResult, error) {
	return telemetry.MetricsResult{}, nil
}

func (f *fakeAdminTelemetryStore) ListSources(context.Context, telemetry.SourcesQuery) (telemetry.SourcesResult, error) {
	return telemetry.SourcesResult{}, nil
}

func TestAdminTelemetryHandlerPassesBoundedTypedQuery(t *testing.T) {
	store := &fakeAdminTelemetryStore{logs: telemetry.LogsResult{Items: []telemetry.LogRecord{{Service: "api", Body: "healthy"}}}}
	server := &Server{
		config: config.Config{
			TelemetryMaxQueryRange: 2 * time.Hour,
			TelemetryMaxQueryRows:  100,
		},
		telemetry: store,
	}
	from := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	to := time.Now().UTC().Format(time.RFC3339Nano)
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/logs?from="+from+"&to="+to+"&service=api&level=error&query=timeout&limit=7", nil)
	recorder := httptest.NewRecorder()

	server.adminTelemetryLogs(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if store.logsQuery.Service != "api" || store.logsQuery.Level != "error" || store.logsQuery.Search != "timeout" || store.logsQuery.Limit != 7 {
		t.Fatalf("query = %+v", store.logsQuery)
	}
	if store.logsQuery.Range.To.Sub(store.logsQuery.Range.From) > 2*time.Hour {
		t.Fatalf("handler passed an unbounded range: %+v", store.logsQuery.Range)
	}
	var response telemetry.LogsResult
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Body != "healthy" {
		t.Fatalf("response = %+v", response)
	}
}

func TestAdminTelemetryHandlerRejectsInvalidRangeBeforeStore(t *testing.T) {
	store := &fakeAdminTelemetryStore{}
	server := &Server{
		config:    config.Config{TelemetryMaxQueryRange: time.Hour, TelemetryMaxQueryRows: 100},
		telemetry: store,
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/logs?from=not-a-time", nil)
	recorder := httptest.NewRecorder()

	server.adminTelemetryLogs(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if store.logsCalled {
		t.Fatalf("store was called for invalid range: %+v", store.logsQuery)
	}
}

func TestAdminTelemetryTracesRejectsNonFiniteDuration(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(value, func(t *testing.T) {
			store := &fakeAdminTelemetryStore{}
			server := &Server{config: config.Config{TelemetryMaxQueryRange: time.Hour, TelemetryMaxQueryRows: 100}, telemetry: store}
			request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/traces?min_duration_ms="+value, nil)
			recorder := httptest.NewRecorder()

			server.adminTelemetryTraces(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if store.tracesCalled {
				t.Fatalf("store was called for non-finite duration: %+v", store.tracesQuery)
			}
		})
	}
}

func TestAdminTelemetryHandlerDoesNotExposeBackendError(t *testing.T) {
	store := &fakeAdminTelemetryStore{logsErr: errors.New("clickhouse password=do-not-return")}
	server := &Server{
		config:    config.Config{TelemetryMaxQueryRange: time.Hour, TelemetryMaxQueryRows: 100},
		telemetry: store,
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/logs", nil)
	recorder := httptest.NewRecorder()

	server.adminTelemetryLogs(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "do-not-return") || strings.Contains(recorder.Body.String(), "password") {
		t.Fatalf("backend details leaked: %s", recorder.Body.String())
	}
}

func TestAdminTelemetryHandlerReportsOptionalBackendDegraded(t *testing.T) {
	server := &Server{config: config.Config{TelemetryMaxQueryRange: time.Hour, TelemetryMaxQueryRows: 100}}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/logs", nil)
	recorder := httptest.NewRecorder()

	server.adminTelemetryLogs(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "telemetry_unavailable") || !strings.Contains(recorder.Body.String(), "telemetry backend is unavailable") {
		t.Fatalf("degraded response = %s", recorder.Body.String())
	}
}

func TestAdminTelemetryLogTailStreamsRedactedDomainRecords(t *testing.T) {
	started := make(chan struct{}, 1)
	store := &fakeAdminTelemetryStore{
		logsStarted: started,
		logs: telemetry.LogsResult{Items: []telemetry.LogRecord{{
			Timestamp: time.Now().UTC(), TraceID: "trace-1", Service: "api", Body: "request failed",
		}}},
	}
	server := &Server{config: config.Config{TelemetryMaxQueryRange: time.Hour, TelemetryMaxQueryRows: 100}, telemetry: store}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/logs/tail?limit=7&service=api", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.adminTelemetryLogTail(recorder, request)
		close(done)
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("tail did not query the telemetry store")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tail did not stop after request cancellation")
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: log") || !strings.Contains(body, "trace-1") {
		t.Fatalf("stream body = %s", body)
	}
}

func TestAdminTelemetryLogTailUsesShortStableCursorWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &fakeAdminTelemetryStore{
		logsResults: []telemetry.LogsResult{
			// QueryLogs is newest-first for the initial window. These records
			// share a timestamp, so trace/span/tie ordering must be retained.
			{Items: []telemetry.LogRecord{
				{Timestamp: now, TraceID: "trace-b", SpanID: "span-b", CursorKey: 2, Service: "api", Body: "same-time-b"},
				{Timestamp: now, TraceID: "trace-a", SpanID: "span-a", CursorKey: 1, Service: "api", Body: "same-time-a"},
			}},
			{Items: nil},
		},
	}
	// The second poll is the first cursor query. Cancel after it has been
	// observed so the handler exits without waiting for the two-second ticker.
	store.logsResults[1] = telemetry.LogsResult{Items: []telemetry.LogRecord{{
		Timestamp: now.Add(time.Second), TraceID: "trace-c", SpanID: "span-c", CursorKey: 3, Service: "api", Body: "new-row",
	}}}
	store.logsHook = func(call int, _ telemetry.LogsQuery) {
		if call == 1 {
			cancel()
		}
	}
	server := &Server{config: config.Config{TelemetryMaxQueryRange: 31 * 24 * time.Hour, TelemetryMaxQueryRows: 100}, telemetry: store}
	from := now.Add(-29 * 24 * time.Hour).Format(time.RFC3339Nano)
	to := now.Add(time.Second).Format(time.RFC3339Nano)
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/logs/tail?from="+from+"&to="+to+"&limit=10", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.adminTelemetryLogTail(recorder, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("tail did not stop after the cursor poll")
	}
	if len(store.logsQueries) != 2 {
		t.Fatalf("query count = %d, want two polls", len(store.logsQueries))
	}
	if store.logsQueries[0].Range.To.Sub(store.logsQueries[0].Range.From) > 10*time.Minute {
		t.Fatalf("initial tail window = %s, want bounded lookback", store.logsQueries[0].Range.To.Sub(store.logsQueries[0].Range.From))
	}
	if store.logsQueries[1].After == nil || store.logsQueries[1].After.TraceID != "trace-b" || store.logsQueries[1].After.Tie != 2 {
		t.Fatalf("second query cursor = %+v, want last same-timestamp row", store.logsQueries[1].After)
	}
	body := recorder.Body.String()
	for _, marker := range []string{"same-time-a", "same-time-b", "new-row"} {
		if strings.Count(body, marker) != 1 {
			t.Fatalf("stream marker %q count = %d, body = %s", marker, strings.Count(body, marker), body)
		}
	}
	if strings.Count(body, "event: log") != 3 || !strings.Contains(body, "id: ") {
		t.Fatalf("stream did not emit one resumable event per row: %s", body)
	}
}

func TestAdminTelemetryLogTailResumesFromLastEventID(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	previous := telemetry.LogCursor{Timestamp: now, TraceID: "trace-previous", SpanID: "span-previous", Tie: 9}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &fakeAdminTelemetryStore{logs: telemetry.LogsResult{Items: []telemetry.LogRecord{{
		Timestamp: now.Add(time.Second), TraceID: "trace-next", SpanID: "span-next", CursorKey: 10, Service: "api", Body: "resumed-row",
	}}}}
	store.logsHook = func(call int, _ telemetry.LogsQuery) {
		if call == 0 {
			cancel()
		}
	}
	server := &Server{config: config.Config{TelemetryMaxQueryRange: time.Hour, TelemetryMaxQueryRows: 100}, telemetry: store}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/logs/tail?limit=10", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", telemetry.EncodeLogCursor(previous))
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.adminTelemetryLogTail(recorder, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tail did not stop after resume poll")
	}
	if len(store.logsQueries) != 1 || store.logsQueries[0].After == nil || store.logsQueries[0].After.TraceID != previous.TraceID {
		t.Fatalf("resume query = %+v, want Last-Event-ID cursor", store.logsQueries)
	}
	if !strings.Contains(recorder.Body.String(), "resumed-row") {
		t.Fatalf("resume body = %s", recorder.Body.String())
	}
}
