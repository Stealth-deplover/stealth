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
	logsCalled bool
	logsQuery  telemetry.LogsQuery
	logs       telemetry.LogsResult
	logsErr    error
}

func (f *fakeAdminTelemetryStore) Ping(context.Context) error { return nil }

func (f *fakeAdminTelemetryStore) QueryLogs(_ context.Context, query telemetry.LogsQuery) (telemetry.LogsResult, error) {
	f.logsCalled = true
	f.logsQuery = query
	return f.logs, f.logsErr
}

func (f *fakeAdminTelemetryStore) QueryTraces(context.Context, telemetry.TracesQuery) (telemetry.TracesResult, error) {
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
