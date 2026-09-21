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
	if metric.Value == nil || !metric.Timestamp.Equal(timestamp) || metric.Name != "stealth.compose.smoke" || metric.Service != "telemetry.integration" || *metric.Value != 3.5 || metric.Kind != "gauge" || metric.Attributes["smoke.marker"] != marker || metric.ResourceAttributes["smoke.marker"] != marker {
		t.Fatalf("unexpected real Collector metric result = %#v", metric)
	}
	if err := assertRawTelemetrySecretAbsent(ctx, conn, marker, secret, timestamp); err != nil {
		t.Fatal(err)
	}
}

func TestClickHouseStoreLogCursorIntegration(t *testing.T) {
	address := os.Getenv("TEST_CLICKHOUSE_ADDR")
	collectorHTTP := os.Getenv("TEST_OTEL_COLLECTOR_HTTP")
	if address == "" || collectorHTTP == "" {
		t.Skip("set TEST_CLICKHOUSE_ADDR and TEST_OTEL_COLLECTOR_HTTP to run the real log cursor integration test")
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
	store := NewWithConn(conn, Config{Database: database, MaxQueryDuration: 5 * time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	t.Cleanup(func() { _ = store.Close() })
	if err := conn.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	marker := fmt.Sprintf("telemetry-log-cursor-%d", time.Now().UnixNano())
	timestamp := time.Now().UTC().Truncate(time.Millisecond)
	containerID := "abcdef123456"
	emitCollectorSignal(t, collectorHTTP, "metrics", map[string]any{
		"resourceMetrics": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAttribute("service.name", "telemetry.cursor"),
				stringAttribute("container.id", containerID),
				stringAttribute("container.name", "cursor-api"),
				stringAttribute("container.image.name", "cursor-api:test"),
				stringAttribute("container.image.id", "sha256:cursor"),
			}},
			"scopeMetrics": []any{map[string]any{
				"scope": map[string]any{"name": "telemetry.cursor"},
				"metrics": []any{map[string]any{
					"name": "container.cpu.usage.total",
					"gauge": map[string]any{"dataPoints": []any{map[string]any{
						"timeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
						"asDouble":     1.0,
						"attributes": []any{
							stringAttribute("docker.compose.project", "stealth"),
							stringAttribute("service.name", "api"),
							stringAttribute("docker.compose.container_number", "1"),
						},
					}},
					},
				}},
			}},
		}},
	})
	logRecords := make([]any, 0, 3)
	for _, suffix := range []string{"a", "b", "c"} {
		traceID := fmt.Sprintf("%032x", time.Now().UnixNano())
		logRecords = append(logRecords, map[string]any{
			"timeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
			"severityText": "INFO",
			"body":         map[string]any{"stringValue": marker + "-" + suffix},
			"attributes":   []any{stringAttribute("smoke.marker", marker)},
			"traceId":      traceID,
			"spanId":       fmt.Sprintf("%016x", time.Now().UnixNano()),
		})
	}
	emitCollectorSignal(t, collectorHTTP, "logs", map[string]any{
		"resourceLogs": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAttribute("service.name", "telemetry.cursor"),
				stringAttribute("container.id", containerID),
			}},
			"scopeLogs": []any{map[string]any{"scope": map[string]any{"name": "telemetry.cursor"}, "logRecords": logRecords}},
		}},
	})

	rangeQuery := TimeRange{From: timestamp.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute)}
	var page LogsResult
	var lastErr error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		page, lastErr = store.QueryLogs(ctx, LogsQuery{Range: rangeQuery, Service: "telemetry.cursor", Search: marker, Limit: 10})
		if lastErr == nil && len(page.Items) == 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil || len(page.Items) != 3 {
		t.Fatalf("cursor seed rows = %d, last error = %v", len(page.Items), lastErr)
	}
	for _, item := range page.Items {
		if item.EventID == "" {
			t.Fatalf("Collector did not persist a log event ID: %#v", item)
		}
		if _, exposed := item.Attributes[logEventIDAttribute]; exposed {
			t.Fatalf("internal log event ID leaked through log attributes: %#v", item.Attributes)
		}
	}
	metadataDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(metadataDeadline) && page.Items[0].ResourceAttributes["container.name"] != "cursor-api" {
		page, lastErr = store.QueryLogs(ctx, LogsQuery{Range: rangeQuery, Service: "telemetry.cursor", Search: marker, Limit: 10})
		if lastErr != nil {
			t.Fatal(lastErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
	metadata := page.Items[0].ResourceAttributes
	for key, want := range map[string]string{
		"container.id":                    containerID,
		"container.name":                  "cursor-api",
		"container.image.name":            "cursor-api:test",
		"container.image.id":              "sha256:cursor",
		"docker.compose.project":          "stealth",
		"docker.compose.service":          "api",
		"docker.compose.container_number": "1",
	} {
		if metadata[key] != want {
			t.Fatalf("log metadata %s = %q, want %q (all=%#v)", key, metadata[key], want, metadata)
		}
	}
	oldest := page.Items[len(page.Items)-1]
	after, err := store.QueryLogs(ctx, LogsQuery{
		Range:   rangeQuery,
		Service: "telemetry.cursor",
		Search:  marker,
		Limit:   10,
		After:   &LogCursor{Timestamp: oldest.Timestamp, EventID: oldest.EventID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 2 {
		t.Fatalf("cursor page = %d rows, want 2 newer rows", len(after.Items))
	}
	previous := LogCursor{Timestamp: oldest.Timestamp, EventID: oldest.EventID}
	for _, item := range after.Items {
		current := LogCursor{Timestamp: item.Timestamp, EventID: item.EventID}
		if !current.After(previous) {
			t.Fatalf("cursor result did not advance: previous=%+v current=%+v", previous, current)
		}
		if item.Body == oldest.Body {
			t.Fatalf("cursor returned the boundary row again: %#v", item)
		}
		previous = current
	}
}

func TestClickHouseStoreIdenticalLogRowsPaginateByPersistedEventIDIntegration(t *testing.T) {
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
	store := NewWithConn(conn, Config{Database: database, MaxQueryDuration: 5 * time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	t.Cleanup(func() { _ = store.Close() })
	if err := conn.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	marker := fmt.Sprintf("telemetry-identical-log-cursor-%d", time.Now().UnixNano())
	timestamp := time.Now().UTC().Truncate(time.Millisecond)
	identicalRecord := map[string]any{
		"timeUnixNano":         fmt.Sprintf("%d", timestamp.UnixNano()),
		"observedTimeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
		"severityText":         "INFO",
		"body":                 map[string]any{"stringValue": marker},
		"attributes":           []any{stringAttribute("smoke.marker", marker)},
		"traceId":              "11111111111111111111111111111111",
		"spanId":               "2222222222222222",
	}
	emitCollectorSignal(t, collectorHTTP, "logs", map[string]any{
		"resourceLogs": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{stringAttribute("service.name", "telemetry.identical")}},
			"scopeLogs": []any{map[string]any{
				"scope":      map[string]any{"name": "telemetry.identical"},
				"logRecords": []any{identicalRecord, identicalRecord},
			}},
		}},
	})

	rangeQuery := TimeRange{From: timestamp.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute)}
	var seeded LogsResult
	var lastErr error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		seeded, lastErr = store.QueryLogs(ctx, LogsQuery{Range: rangeQuery, Service: "telemetry.identical", Search: marker, Limit: 10})
		if lastErr == nil && len(seeded.Items) == 2 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil || len(seeded.Items) != 2 {
		t.Fatalf("identical log rows = %d, last error = %v", len(seeded.Items), lastErr)
	}
	if seeded.Items[0].EventID == "" || seeded.Items[1].EventID == "" || seeded.Items[0].EventID == seeded.Items[1].EventID {
		t.Fatalf("identical log rows did not receive distinct persisted event IDs: %#v", seeded.Items)
	}

	page, err := store.QueryLogs(ctx, LogsQuery{
		Range: rangeQuery, Service: "telemetry.identical", Search: marker, Limit: 1,
		After: &LogCursor{Timestamp: timestamp.Add(-time.Nanosecond)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("identical log page 1 = %d rows, want 1", len(page.Items))
	}
	first := page.Items[0]

	page, err = store.QueryLogs(ctx, LogsQuery{
		Range: rangeQuery, Service: "telemetry.identical", Search: marker, Limit: 1,
		After: &LogCursor{Timestamp: first.Timestamp, EventID: first.EventID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("identical log page 2 = %d rows, want 1", len(page.Items))
	}
	second := page.Items[0]
	if second.EventID == first.EventID || second.Body != first.Body || second.TraceID != first.TraceID || second.SpanID != first.SpanID || second.Timestamp != first.Timestamp {
		t.Fatalf("identical log page 2 did not return the distinct duplicate row: first=%#v second=%#v", first, second)
	}

	page, err = store.QueryLogs(ctx, LogsQuery{
		Range: rangeQuery, Service: "telemetry.identical", Search: marker, Limit: 1,
		After: &LogCursor{Timestamp: second.Timestamp, EventID: second.EventID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("identical log page 3 = %d rows, want empty", len(page.Items))
	}
}

func TestClickHouseStoreMetricKindsIntegration(t *testing.T) {
	address := os.Getenv("TEST_CLICKHOUSE_ADDR")
	collectorHTTP := os.Getenv("TEST_OTEL_COLLECTOR_HTTP")
	if address == "" || collectorHTTP == "" {
		t.Skip("set TEST_CLICKHOUSE_ADDR and TEST_OTEL_COLLECTOR_HTTP to run the real metric-kind integration test")
	}
	database := valueOrDefault("TEST_CLICKHOUSE_DATABASE", "stealth_telemetry")
	username := valueOrDefault("TEST_CLICKHOUSE_USER", "stealth")
	password := os.Getenv("TEST_CLICKHOUSE_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	service := fmt.Sprintf("telemetry.metric-kinds-%d", time.Now().UnixNano())
	marker := fmt.Sprintf("telemetry-metric-kinds-%d", time.Now().UnixNano())
	timestamp := time.Now().UTC().Truncate(time.Second)
	attributes := []any{stringAttribute("smoke.marker", marker)}
	resource := map[string]any{"attributes": []any{
		stringAttribute("service.name", service),
		stringAttribute("smoke.marker", marker),
	}}
	emitCollectorSignal(t, collectorHTTP, "metrics", map[string]any{
		"resourceMetrics": []any{map[string]any{
			"resource": resource,
			"scopeMetrics": []any{map[string]any{
				"scope": map[string]any{"name": "telemetry.metric-kinds"},
				"metrics": []any{
					map[string]any{
						"name": "aud14.histogram",
						"histogram": map[string]any{
							"aggregationTemporality": 2,
							"dataPoints": []any{map[string]any{
								"timeUnixNano":   fmt.Sprintf("%d", timestamp.UnixNano()),
								"count":          "3",
								"sum":            6.0,
								"bucketCounts":   []string{"1", "2"},
								"explicitBounds": []float64{1, 2},
								"min":            0.5,
								"max":            3.0,
								"attributes":     attributes,
							}},
						},
					},
					map[string]any{
						"name": "aud14.summary",
						"summary": map[string]any{
							"dataPoints": []any{map[string]any{
								"timeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
								"count":        "3",
								"sum":          6.0,
								"quantileValues": []any{
									map[string]any{"quantile": 0.5, "value": 1.5},
									map[string]any{"quantile": 0.9, "value": 2.5},
								},
								"attributes": attributes,
							}},
						},
					},
					map[string]any{
						"name": "aud14.exponential",
						"exponentialHistogram": map[string]any{
							"aggregationTemporality": 2,
							"dataPoints": []any{map[string]any{
								"timeUnixNano": fmt.Sprintf("%d", timestamp.UnixNano()),
								"count":        "3",
								"sum":          6.0,
								"scale":        1,
								"zeroCount":    "1",
								"positive": map[string]any{
									"offset":       0,
									"bucketCounts": []string{"1", "2"},
								},
								"negative": map[string]any{
									"offset":       -1,
									"bucketCounts": []string{"1"},
								},
								"attributes": attributes,
							}},
						},
					},
				},
			}},
		}},
	})

	rangeQuery := TimeRange{From: timestamp.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute)}
	var results map[string]MetricRecord
	var lastErr error
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		results = make(map[string]MetricRecord)
		for _, name := range []string{"aud14.histogram", "aud14.summary", "aud14.exponential"} {
			var result MetricsResult
			result, lastErr = store.QueryMetrics(ctx, MetricsQuery{Range: rangeQuery, Service: service, Name: name, Limit: 10})
			if lastErr != nil {
				break
			}
			if len(result.Items) == 1 {
				results[name] = result.Items[0]
			}
		}
		if lastErr == nil && len(results) == 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil || len(results) != 3 {
		t.Fatalf("metric-kind results = %d, last error = %v", len(results), lastErr)
	}
	histogram := results["aud14.histogram"]
	if histogram.Kind != "histogram" || histogram.Histogram == nil || histogram.Histogram.Count != 3 || histogram.Histogram.Sum != 6 || len(histogram.Histogram.BucketCounts) != 2 || len(histogram.Histogram.ExplicitBounds) != 2 || histogram.Attributes["smoke.marker"] != marker {
		t.Fatalf("unexpected histogram result = %#v", histogram)
	}
	summary := results["aud14.summary"]
	if summary.Kind != "summary" || summary.Summary == nil || summary.Summary.Count != 3 || len(summary.Summary.Quantiles) != 2 || summary.Summary.Quantiles[0].Quantile != 0.5 || summary.Summary.Quantiles[0].Value != 1.5 {
		t.Fatalf("unexpected summary result = %#v", summary)
	}
	exponential := results["aud14.exponential"]
	if exponential.Kind != "exponential_histogram" || exponential.Exponential == nil || exponential.Exponential.Count != 3 || exponential.Exponential.Scale != 1 || exponential.Exponential.ZeroCount != 1 || len(exponential.Exponential.PositiveBucketCounts) != 2 || len(exponential.Exponential.NegativeBucketCounts) != 1 {
		t.Fatalf("unexpected exponential histogram result = %#v", exponential)
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
