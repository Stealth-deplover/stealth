package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestClickHouseStoreIntegration(t *testing.T) {
	address := os.Getenv("TEST_CLICKHOUSE_ADDR")
	collectorHTTP := os.Getenv("TEST_OTEL_COLLECTOR_HTTP")
	if address == "" || collectorHTTP == "" {
		t.Skip("set TEST_CLICKHOUSE_ADDR and TEST_OTEL_COLLECTOR_HTTP to run the real Collector telemetry integration test")
	}
	database := valueOrDefault("TEST_CLICKHOUSE_DATABASE", "stealth_telemetry")
	username := valueOrDefault("TEST_CLICKHOUSE_USER", "stealth")
	password := os.Getenv("TEST_CLICKHOUSE_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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

	marker := fmt.Sprintf("telemetry-integration-%d", time.Now().UnixNano())
	secret := "STEALTH_AUD01_SUPER_SECRET_123456"
	timestamp := time.Now().UTC().Truncate(time.Second)
	traceID := fmt.Sprintf("%032x", time.Now().UnixNano())
	spanID := fmt.Sprintf("%016x", time.Now().UnixNano())
	emitCollectorSignal(t, collectorHTTP, "logs", map[string]any{
		"resourceLogs": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAttribute("service.name", "telemetry.integration"),
				stringAttribute("smoke.marker", marker),
				stringAttribute("client_secret", secret),
			}},
			"scopeLogs": []any{map[string]any{
				"scope": map[string]any{
					"name":       "telemetry.integration",
					"attributes": []any{stringAttribute("scope_secret", secret)},
				},
				"logRecords": []any{map[string]any{
					"timeUnixNano":         fmt.Sprintf("%d", timestamp.UnixNano()),
					"observedTimeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
					"severityNumber":       17,
					"severityText":         "ERROR",
					"body":                 map[string]any{"stringValue": "password=" + secret + " " + marker},
					"attributes": []any{
						stringAttribute("smoke.marker", marker),
						stringAttribute("password", secret),
						stringAttribute("message", "authorization: Bearer "+secret),
					},
					"traceId": traceID,
					"spanId":  spanID,
				}},
			}},
		}},
	})
	emitCollectorSignal(t, collectorHTTP, "traces", map[string]any{
		"resourceSpans": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAttribute("service.name", "telemetry.integration"),
				stringAttribute("smoke.marker", marker),
				stringAttribute("access_token", secret),
			}},
			"scopeSpans": []any{map[string]any{
				"scope": map[string]any{"name": "telemetry.integration"},
				"spans": []any{map[string]any{
					"traceId":           traceID,
					"spanId":            spanID,
					"name":              "GET /integration?token=" + secret,
					"kind":              "SPAN_KIND_SERVER",
					"startTimeUnixNano": fmt.Sprintf("%d", timestamp.Add(-250*time.Millisecond).UnixNano()),
					"endTimeUnixNano":   fmt.Sprintf("%d", timestamp.UnixNano()),
					"attributes": []any{
						stringAttribute("smoke.marker", marker),
						stringAttribute("api_key", secret),
						stringAttribute("http.url", "https://user:"+secret+"@example.test/health"),
					},
					"events": []any{map[string]any{
						"name":         "exception",
						"timeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
						"attributes": []any{
							stringAttribute("exception.message", "password="+secret),
							stringAttribute("exception.stacktrace", "token="+secret),
						},
					}},
					"status": map[string]any{
						"code":    "STATUS_CODE_ERROR",
						"message": "token=" + secret,
					},
				}},
			}},
		}},
	})
	emitCollectorSignal(t, collectorHTTP, "metrics", map[string]any{
		"resourceMetrics": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAttribute("service.name", "telemetry.integration"),
				stringAttribute("smoke.marker", marker),
				stringAttribute("refresh_token", secret),
			}},
			"scopeMetrics": []any{map[string]any{
				"scope": map[string]any{
					"name":       "telemetry.integration",
					"attributes": []any{stringAttribute("scope_secret", secret)},
				},
				"metrics": []any{map[string]any{
					"name":        "stealth.compose.smoke",
					"description": "client_secret=" + secret,
					"unit":        "1",
					"gauge": map[string]any{"dataPoints": []any{map[string]any{
						"timeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
						"asDouble":     3.5,
						"attributes": []any{
							stringAttribute("smoke.marker", marker),
							stringAttribute("private_key", secret),
						},
					}}},
				}},
			}},
		}},
	})

	rangeQuery := TimeRange{From: timestamp.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute)}
	var logsResult LogsResult
	var tracesResult TracesResult
	var metricsResult MetricsResult
	var sourcesResult SourcesResult
	var lastErr error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		logsResult, lastErr = store.QueryLogs(ctx, LogsQuery{Range: rangeQuery, Service: "telemetry.integration", Search: marker, Limit: 10})
		if lastErr == nil {
			tracesResult, lastErr = store.QueryTraces(ctx, TracesQuery{Range: rangeQuery, Service: "telemetry.integration", TraceID: traceID, Limit: 10})
		}
		if lastErr == nil {
			metricsResult, lastErr = store.QueryMetrics(ctx, MetricsQuery{Range: rangeQuery, Service: "telemetry.integration", Name: "stealth.compose.smoke", Limit: 10})
		}
		if lastErr == nil {
			sourcesResult, lastErr = store.ListSources(ctx, SourcesQuery{Range: rangeQuery, Limit: 10})
		}
		if lastErr == nil && len(logsResult.Items) == 1 && len(tracesResult.Items) == 1 && len(metricsResult.Items) == 1 && len(sourcesResult.Items) >= 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil || len(logsResult.Items) != 1 || len(tracesResult.Items) != 1 || len(metricsResult.Items) != 1 || len(sourcesResult.Items) < 3 {
		t.Fatalf("real Collector telemetry results = logs:%d traces:%d metrics:%d sources:%d, last error = %v", len(logsResult.Items), len(tracesResult.Items), len(metricsResult.Items), len(sourcesResult.Items), lastErr)
	}
	if logsResult.Items[0].TraceID != traceID || logsResult.Items[0].Body != "**** "+marker || logsResult.Items[0].Attributes["smoke.marker"] != marker {
		t.Fatalf("unexpected real Collector log result = %#v", logsResult.Items[0])
	}
	if tracesResult.Items[0].TraceID != traceID || tracesResult.Items[0].StatusMessage != "[REDACTED]" || tracesResult.Items[0].ResourceAttributes["smoke.marker"] != marker {
		t.Fatalf("unexpected real Collector trace result = %#v", tracesResult.Items[0])
	}
	metric := metricsResult.Items[0]
	if !metric.Timestamp.Equal(timestamp) || metric.Name != "stealth.compose.smoke" || metric.Service != "telemetry.integration" || metric.Value != 3.5 || metric.Kind != "gauge" || metric.Attributes["smoke.marker"] != marker || metric.ResourceAttributes["smoke.marker"] != marker {
		t.Fatalf("unexpected real Collector metric result = %#v", metric)
	}
	if err := assertRawTelemetrySecretAbsent(ctx, conn, marker, secret, timestamp); err != nil {
		t.Fatal(err)
	}
}

func assertRawTelemetrySecretAbsent(ctx context.Context, conn clickhouse.Conn, marker, secret string, timestamp time.Time) error {
	from := timestamp.Add(-time.Minute)
	to := timestamp.Add(time.Minute)
	queries := []struct {
		name      string
		query     string
		precision clickhouse.TimeUnit
	}{
		{
			name:      "logs",
			precision: clickhouse.NanoSeconds,
			query: `
SELECT count()
FROM otel_logs
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
  AND ResourceAttributes['smoke.marker'] = {marker:String}
  AND (
    positionCaseInsensitiveUTF8(Body, {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(LogAttributes), {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(ResourceAttributes), {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(ScopeAttributes), {secret:String}) > 0
  )`,
		},
		{
			name:      "traces",
			precision: clickhouse.NanoSeconds,
			query: `
SELECT count()
FROM otel_traces
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
  AND ResourceAttributes['smoke.marker'] = {marker:String}
  AND (
    positionCaseInsensitiveUTF8(SpanName, {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(StatusMessage, {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(SpanAttributes), {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(ResourceAttributes), {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(Events.Attributes), {secret:String}) > 0
  )`,
		},
		{
			name:      "metrics",
			precision: clickhouse.Seconds,
			query: `
SELECT count()
FROM otel_metrics_gauge
WHERE TimeUnix >= {from:DateTime}
  AND TimeUnix < {to:DateTime}
  AND ResourceAttributes['smoke.marker'] = {marker:String}
  AND (
    positionCaseInsensitiveUTF8(MetricName, {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(MetricDescription, {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(Attributes), {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(ResourceAttributes), {secret:String}) > 0
    OR positionCaseInsensitiveUTF8(toString(ScopeAttributes), {secret:String}) > 0
  )`,
		},
	}
	for _, item := range queries {
		var count uint64
		if err := conn.QueryRow(ctx, item.query,
			clickhouse.DateNamed("from", from, item.precision),
			clickhouse.DateNamed("to", to, item.precision),
			clickhouse.Named("marker", marker),
			clickhouse.Named("secret", secret),
		).Scan(&count); err != nil {
			return fmt.Errorf("query raw %s telemetry for secret marker: %w", item.name, err)
		}
		if count != 0 {
			return fmt.Errorf("raw secret found in %s ClickHouse rows: %d", item.name, count)
		}
	}
	return nil
}

func emitCollectorSignal(t *testing.T, collectorHTTP, signal string, payload map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/%s", collectorHTTP, signal), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("send OTLP %s: %v", signal, err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("send OTLP %s returned HTTP %d: %s", signal, response.StatusCode, responseBody)
	}
}

func stringAttribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
