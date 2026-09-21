package telemetry

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type recordingConn struct {
	driver.Conn
	query   string
	args    []any
	execs   []string
	allArgs [][]any
}

type blockingConn struct {
	driver.Conn
}

func (c *blockingConn) Query(ctx context.Context, _ string, _ ...any) (driver.Rows, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *recordingConn) Query(_ context.Context, query string, args ...any) (driver.Rows, error) {
	c.query = query
	c.args = args
	return &emptyRows{}, nil
}

func (c *recordingConn) Ping(context.Context) error { return nil }

func (c *recordingConn) Exec(_ context.Context, query string, args ...any) error {
	c.execs = append(c.execs, query)
	c.allArgs = append(c.allArgs, args)
	return nil
}

type emptyRows struct{ driver.Rows }

func (r *emptyRows) Next() bool                       { return false }
func (r *emptyRows) Scan(...any) error                { return errors.New("no rows") }
func (r *emptyRows) ScanStruct(any) error             { return errors.New("no rows") }
func (r *emptyRows) Totals(...any) error              { return nil }
func (r *emptyRows) Columns() []string                { return nil }
func (r *emptyRows) Close() error                     { return nil }
func (r *emptyRows) Err() error                       { return nil }
func (r *emptyRows) HasData() bool                    { return false }
func (r *emptyRows) ColumnTypes() []driver.ColumnType { return nil }

func TestQueryUsesTypedParametersAndDoesNotEmbedFilters(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC().Truncate(time.Millisecond)
	injection := `service' OR 1=1 --`
	_, err := store.QueryLogs(context.Background(), LogsQuery{
		Range:   TimeRange{From: now.Add(-time.Minute), To: now},
		Service: injection,
		Search:  injection,
		Limit:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conn.query, injection) || !strings.Contains(conn.query, "{service:String}") || !strings.Contains(conn.query, "{search:String}") {
		t.Fatalf("query embedded an untrusted filter: %s", conn.query)
	}
	if len(conn.args) != 8 {
		t.Fatalf("argument count = %d, want 8", len(conn.args))
	}
	for _, argument := range conn.args {
		switch named := argument.(type) {
		case driver.NamedValue:
			if named.Name == "service" && named.Value != injection {
				t.Fatalf("service value was changed: %#v", named.Value)
			}
		case driver.NamedDateValue:
		default:
			t.Fatalf("query argument %T was not a typed named value", argument)
		}
	}
}

func TestQueryLogsAfterUsesCompleteStableCursor(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err := store.QueryLogs(context.Background(), LogsQuery{
		Range: TimeRange{From: now.Add(-time.Minute), To: now},
		Limit: 10,
		After: &LogCursor{Timestamp: now.Add(-time.Second), TraceID: "trace", SpanID: "span", Tie: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn.query, "cityHash64") || !strings.Contains(conn.query, "after_timestamp") || !strings.Contains(conn.query, "ORDER BY Timestamp ASC, TraceId ASC, SpanId ASC, CursorKey ASC") {
		t.Fatalf("cursor query does not include complete ascending ordering: %s", conn.query)
	}
	if len(conn.args) != 12 {
		t.Fatalf("cursor query argument count = %d, want 12", len(conn.args))
	}
}

func TestQueryLogsEnrichesDockerIdentityFromScalarMetrics(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC()
	if _, err := store.QueryLogs(context.Background(), LogsQuery{Range: TimeRange{From: now.Add(-time.Minute), To: now}, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"container_metadata",
		"otel_metrics_gauge",
		"otel_metrics_sum",
		"container.name",
		"container.image.name",
		"docker.compose.project",
		"docker.compose.service",
		"docker.compose.container_number",
		"from_metrics:DateTime",
	} {
		if !strings.Contains(conn.query, marker) {
			t.Fatalf("Docker log query is missing %q: %s", marker, conn.query)
		}
	}
}

func TestEnrichContainerAttributesPreservesExistingValues(t *testing.T) {
	got := enrichContainerAttributes(
		map[string]string{"container.id": "abc", "container.name": "file-log-name"},
		"stats-name", "image:tag", "image-id", "project", "service", "1",
	)
	if got["container.id"] != "abc" || got["container.name"] != "file-log-name" || got["container.image.name"] != "image:tag" || got["docker.compose.project"] != "project" {
		t.Fatalf("container metadata enrichment = %#v", got)
	}
}

func TestQueryRejectsUnboundedRangeAndLimit(t *testing.T) {
	store := NewWithConn(&recordingConn{}, Config{MaxQueryRange: time.Hour, MaxQueryRows: 10})
	now := time.Now().UTC()
	_, err := store.QueryTraces(context.Background(), TracesQuery{Range: TimeRange{From: now.Add(-2 * time.Hour), To: now}, Limit: 1})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("range error = %v, want ErrInvalidQuery", err)
	}
	_, err = store.QueryMetrics(context.Background(), MetricsQuery{Range: TimeRange{From: now.Add(-time.Minute), To: now}, Limit: 11})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("limit error = %v, want ErrInvalidQuery", err)
	}
}

func TestQueryTracesRejectsNonFiniteDuration(t *testing.T) {
	store := NewWithConn(&recordingConn{}, Config{MaxQueryRange: time.Hour, MaxQueryRows: 10})
	now := time.Now().UTC()
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		_, err := store.QueryTraces(context.Background(), TracesQuery{
			Range: TimeRange{From: now.Add(-time.Minute), To: now},
			MinMs: value,
			Limit: 1,
		})
		if !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("MinMs=%v error = %v, want ErrInvalidQuery", value, err)
		}
	}
	for _, value := range []float64{0, 0.5, 1000} {
		if _, err := store.QueryTraces(context.Background(), TracesQuery{
			Range: TimeRange{From: now.Add(-time.Minute), To: now},
			MinMs: value,
			Limit: 1,
		}); err != nil {
			t.Fatalf("finite MinMs=%v returned error: %v", value, err)
		}
	}
}

func TestTelemetryQueryCancelsWhenTheBackendExceedsTheDeadline(t *testing.T) {
	now := time.Now().UTC()
	store := NewWithConn(&blockingConn{}, Config{MaxQueryDuration: 5 * time.Millisecond, MaxQueryRange: time.Hour, MaxQueryRows: 10})
	_, err := store.QueryLogs(context.Background(), LogsQuery{Range: TimeRange{From: now.Add(-time.Minute), To: now}, Limit: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query error = %v, want context deadline", err)
	}
}

func TestTelemetryOutputRedactsSensitiveFields(t *testing.T) {
	attributes := redactAttributes(map[string]string{
		"api_token":  "abc123",
		"request_id": "req-1",
		"message":    "password=top-secret",
	})
	if attributes["api_token"] != "[REDACTED]" || attributes["message"] != "password=[REDACTED]" || attributes["request_id"] != "req-1" {
		t.Fatalf("unexpected redaction: %#v", attributes)
	}
	if got := redactText("Authorization: Bearer abc.def"); !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "abc.def") {
		t.Fatalf("authorization was not redacted: %q", got)
	}
	for _, value := range []string{
		`refresh_token=top-secret`,
		`https://user:top-secret@example.test/health`,
		`{"password":"top-secret"}`,
	} {
		if got := redactText(value); strings.Contains(got, "top-secret") {
			t.Fatalf("sensitive value survived redaction: input=%q output=%q", value, got)
		}
	}
	if got := redactAttributes(map[string]string{"client_secret": "top-secret"})["client_secret"]; got != "[REDACTED]" {
		t.Fatalf("client secret was not redacted: %q", got)
	}
}

func TestTelemetrySchemaMigrationUsesPinnedVersionAndSafeIdentifier(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{Database: "telemetry_v2", MaxQueryDuration: time.Second})
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(conn.execs) != 2 {
		t.Fatalf("migration exec count = %d, want 2", len(conn.execs))
	}
	for _, query := range conn.execs {
		if strings.Contains(query, "telemetry;drop") {
			t.Fatalf("unsafe database entered migration SQL: %s", query)
		}
	}
	if !strings.Contains(conn.execs[0], "`telemetry_v2`.`telemetry_schema_migrations`") || !strings.Contains(conn.execs[1], "{version:String}") {
		t.Fatalf("migration SQL lost the bounded identifier or typed version: %#v", conn.execs)
	}
	if len(conn.allArgs[1]) != 1 {
		t.Fatalf("migration version args = %#v, want one typed value", conn.allArgs[1])
	}
	version, ok := conn.allArgs[1][0].(driver.NamedValue)
	if !ok || version.Name != "version" || version.Value != CollectorSchemaVersion {
		t.Fatalf("migration version arg = %#v, want typed pinned version", conn.allArgs[1][0])
	}
}

func TestTelemetrySchemaMigrationRejectsUnsafeIdentifier(t *testing.T) {
	store := NewWithConn(&recordingConn{}, Config{Database: "telemetry;drop"})
	if err := store.Migrate(context.Background()); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("migration error = %v, want ErrInvalidQuery", err)
	}
}

func TestExplorerQueriesKeepFiltersOutOfSQL(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: 24 * time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC().Truncate(time.Millisecond)
	injection := `api' OR 1=1 --`

	if _, err := store.QueryErrorGroups(context.Background(), ErrorGroupsQuery{
		Range: TimeRange{From: now.Add(-time.Hour), To: now}, Service: injection, Search: injection, Limit: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conn.query, injection) || !strings.Contains(conn.query, "{service:String}") || !strings.Contains(conn.query, "{search:String}") {
		t.Fatalf("error query embedded an untrusted filter: %s", conn.query)
	}

	if _, err := store.QueryServiceMap(context.Background(), ServiceMapQuery{Range: TimeRange{From: now.Add(-time.Hour), To: now}, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn.query, "quantileTDigest") || strings.Contains(conn.query, "1=1") {
		t.Fatalf("service map query was not the fixed aggregate: %s", conn.query)
	}

	if _, err := store.QueryLogVolume(context.Background(), LogVolumeQuery{Range: TimeRange{From: now.Add(-time.Hour), To: now}, Service: injection, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn.query, "toStartOfInterval") || strings.Contains(conn.query, injection) {
		t.Fatalf("log volume query embedded an untrusted filter: %s", conn.query)
	}
}

func TestSourcesQueryUsesDistinctClickHouseDateTypes(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC()
	if _, err := store.ListSources(context.Background(), SourcesQuery{
		Range: TimeRange{From: now.Add(-time.Minute), To: now}, Limit: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn.query, "{from:DateTime64(9)}") || !strings.Contains(conn.query, "{from_metrics:DateTime}") {
		t.Fatalf("sources query did not keep signal date types distinct: %s", conn.query)
	}
	if len(conn.args) != 5 {
		t.Fatalf("sources argument count = %d, want 5", len(conn.args))
	}
}

func TestMetricQueryUsesExporterDateTimeForFractionalRanges(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	from := time.Date(2026, time.September, 19, 12, 0, 0, 123456789, time.FixedZone("test", 3600))
	to := from.Add(1500 * time.Millisecond)
	if _, err := store.QueryMetrics(context.Background(), MetricsQuery{
		Range: TimeRange{From: from, To: to}, Limit: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn.query, "{from:DateTime}") || !strings.Contains(conn.query, "{to:DateTime}") {
		t.Fatalf("metric query did not use the exporter DateTime columns: %s", conn.query)
	}
	if len(conn.args) != 5 {
		t.Fatalf("metric argument count = %d, want 5", len(conn.args))
	}
	for _, argument := range conn.args[:2] {
		if _, ok := argument.(driver.NamedDateValue); !ok {
			t.Fatalf("metric range argument %T was not a typed date value", argument)
		}
	}
}

func TestMetricQueryIncludesEveryPinnedExporterMetricKind(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC()
	if _, err := store.QueryMetrics(context.Background(), MetricsQuery{Range: TimeRange{From: now.Add(-time.Minute), To: now}, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"FROM otel_metrics_gauge",
		"FROM otel_metrics_sum",
		"FROM otel_metrics_histogram",
		"FROM otel_metrics_summary",
		"FROM otel_metrics_exp_histogram",
		"BucketCounts",
		"ExplicitBounds",
		"ValueAtQuantiles.Quantile",
		"PositiveBucketCounts",
	} {
		if !strings.Contains(conn.query, marker) {
			t.Fatalf("metrics query is missing %q: %s", marker, conn.query)
		}
	}
}

func TestLogVolumeChoosesDeterministicBuckets(t *testing.T) {
	now := time.Now().UTC()
	checks := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{name: "minute", duration: time.Hour, want: "INTERVAL 1 MINUTE"},
		{name: "five minute", duration: 6 * time.Hour, want: "INTERVAL 5 MINUTE"},
		{name: "quarter hour", duration: 24 * time.Hour, want: "INTERVAL 15 MINUTE"},
		{name: "hour", duration: 7 * 24 * time.Hour, want: "INTERVAL 1 HOUR"},
		{name: "day", duration: 30 * 24 * time.Hour, want: "INTERVAL 1 DAY"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			query := logVolumeQueryFor(TimeRange{From: now.Add(-check.duration), To: now})
			if !strings.Contains(query, check.want) {
				t.Fatalf("bucket query = %s, want %q", query, check.want)
			}
		})
	}
}

func TestInfrastructureQueryUsesFixedNamespacesAndValidatesScope(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC()
	if _, err := store.QueryInfrastructure(context.Background(), InfrastructureQuery{
		Range: TimeRange{From: now.Add(-time.Minute), To: now}, Scope: "host", Limit: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn.query, "startsWith(MetricName, 'container.')") || !strings.Contains(conn.query, "{scope:String}") {
		t.Fatalf("infrastructure query lost fixed namespace or typed scope: %s", conn.query)
	}
	if _, err := store.QueryInfrastructure(context.Background(), InfrastructureQuery{
		Range: TimeRange{From: now.Add(-time.Minute), To: now}, Scope: "system', drop", Limit: 10,
	}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("invalid infrastructure scope error = %v, want ErrInvalidQuery", err)
	}
}

func TestHTTPOverviewUsesBoundedTraceAggregate(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC()
	if _, err := store.QueryHTTPOverview(context.Background(), HTTPOverviewQuery{Range: TimeRange{From: now.Add(-time.Minute), To: now}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn.query, "quantileTDigest") || !strings.Contains(conn.query, "{seconds:Float64}") || strings.Contains(conn.query, "SELECT "+"*") {
		t.Fatalf("HTTP overview query is not a fixed aggregate: %s", conn.query)
	}
}

func TestAlertQueriesKeepDimensionsOutOfSQL(t *testing.T) {
	conn := &recordingConn{}
	store := NewWithConn(conn, Config{MaxQueryDuration: time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	now := time.Now().UTC()
	injection := `api' OR 1=1 --`
	if _, err := store.EvaluateAlert(context.Background(), AlertQuery{
		Kind: "metric_threshold", Metric: injection, Service: injection,
		Range: TimeRange{From: now.Add(-time.Minute), To: now},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conn.query, injection) || !strings.Contains(conn.query, "{metric:String}") || !strings.Contains(conn.query, "{service:String}") {
		t.Fatalf("alert query embedded an untrusted dimension: %s", conn.query)
	}
}
