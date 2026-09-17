package telemetry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestClickHouseStoreIntegration(t *testing.T) {
	address := os.Getenv("TEST_CLICKHOUSE_ADDR")
	if address == "" {
		t.Skip("set TEST_CLICKHOUSE_ADDR to run ClickHouse telemetry integration tests")
	}
	database := valueOrDefault("TEST_CLICKHOUSE_DATABASE", "stealth_telemetry")
	username := valueOrDefault("TEST_CLICKHOUSE_USER", "stealth")
	password := os.Getenv("TEST_CLICKHOUSE_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:        []string{address},
		Auth:        clickhouse.Auth{Database: database, Username: username, Password: password},
		DialTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	store := NewWithConn(conn, Config{Database: database, MaxQueryDuration: 5 * time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS otel_logs (Timestamp DateTime64(9), TraceId String, SpanId String, SeverityText String, ServiceName String, Body String, LogAttributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY Timestamp`,
		`CREATE TABLE IF NOT EXISTS otel_traces (Timestamp DateTime64(9), TraceId String, SpanId String, ParentSpanId String, SpanName String, SpanKind String, ServiceName String, Duration UInt64, StatusCode String, StatusMessage String, SpanAttributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY Timestamp`,
		`CREATE TABLE IF NOT EXISTS otel_metrics_gauge (TimeUnix DateTime, MetricName String, ServiceName String, Value Float64, Attributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY TimeUnix`,
		`CREATE TABLE IF NOT EXISTS otel_metrics_sum (TimeUnix DateTime, MetricName String, ServiceName String, Value Float64, Attributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY TimeUnix`,
		`TRUNCATE TABLE otel_logs`,
		`TRUNCATE TABLE otel_traces`,
		`TRUNCATE TABLE otel_metrics_gauge`,
		`TRUNCATE TABLE otel_metrics_sum`,
	} {
		if err := conn.Exec(ctx, statement); err != nil {
			t.Fatalf("ClickHouse statement %q: %v", statement, err)
		}
	}

	timestamp := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Microsecond)
	const traceID = "0123456789abcdef0123456789abcdef"
	logs, err := conn.PrepareBatch(ctx, "INSERT INTO otel_logs (Timestamp, TraceId, SpanId, SeverityText, ServiceName, Body, LogAttributes, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Append(timestamp, traceID, "0123456789abcdef", "ERROR", "api", "password=super-secret", map[string]string{"password": "super-secret", "request_id": "req-integration"}, map[string]string{"deployment.id": "deployment-integration"}); err != nil {
		t.Fatal(err)
	}
	if err := logs.Send(); err != nil {
		t.Fatal(err)
	}

	traces, err := conn.PrepareBatch(ctx, "INSERT INTO otel_traces (Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, Duration, StatusCode, StatusMessage, SpanAttributes, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	if err := traces.Append(timestamp, traceID, "0123456789abcdef", "", "GET /healthz", "Server", "api", uint64(250000000), "Error", "token=super-secret", map[string]string{"http.route": "/healthz"}, map[string]string{"deployment.id": "deployment-integration"}); err != nil {
		t.Fatal(err)
	}
	if err := traces.Send(); err != nil {
		t.Fatal(err)
	}

	metrics, err := conn.PrepareBatch(ctx, "INSERT INTO otel_metrics_gauge (TimeUnix, MetricName, ServiceName, Value, Attributes, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	if err := metrics.Append(timestamp, "stealth_api_http_requests_total", "api", 3.0, map[string]string{"status": "200"}, map[string]string{"deployment.id": "deployment-integration"}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.Send(); err != nil {
		t.Fatal(err)
	}

	rangeQuery := TimeRange{From: timestamp.Add(-time.Minute), To: timestamp.Add(time.Minute)}
	logsResult, err := store.QueryLogs(ctx, LogsQuery{Range: rangeQuery, Limit: 10})
	if err != nil || len(logsResult.Items) != 1 {
		t.Fatalf("logs result = %#v, err = %v", logsResult, err)
	}
	if logsResult.Items[0].TraceID != traceID || logsResult.Items[0].Body != "password=[REDACTED]" || logsResult.Items[0].Attributes["password"] != "[REDACTED]" {
		t.Fatalf("unsafe or incomplete log result = %#v", logsResult.Items[0])
	}

	tracesResult, err := store.QueryTraces(ctx, TracesQuery{Range: rangeQuery, TraceID: traceID, Limit: 10})
	if err != nil || len(tracesResult.Items) != 1 {
		t.Fatalf("traces result = %#v, err = %v", tracesResult, err)
	}
	if tracesResult.Items[0].TraceID != traceID || tracesResult.Items[0].StatusMessage != "token=[REDACTED]" {
		t.Fatalf("unsafe or incomplete trace result = %#v", tracesResult.Items[0])
	}

	metricsResult, err := store.QueryMetrics(ctx, MetricsQuery{Range: rangeQuery, Name: "stealth_api_http_requests_total", Limit: 10})
	if err != nil || len(metricsResult.Items) != 1 || metricsResult.Items[0].Value != 3 {
		t.Fatalf("metrics result = %#v, err = %v", metricsResult, err)
	}
	sourcesResult, err := store.ListSources(ctx, SourcesQuery{Range: rangeQuery, Limit: 10})
	if err != nil || len(sourcesResult.Items) < 3 {
		t.Fatalf("sources result = %#v, err = %v", sourcesResult, err)
	}
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
