package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminTelemetryQueriesRealClickHouseIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	clickHouseAddress := os.Getenv("TEST_CLICKHOUSE_ADDR")
	collectorHTTP := os.Getenv("TEST_OTEL_COLLECTOR_HTTP")
	if databaseURL == "" || clickHouseAddress == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_CLICKHOUSE_ADDR to run Admin telemetry integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	database := valueOrDefaultForHTTPTest("TEST_CLICKHOUSE_DATABASE", "stealth_telemetry")
	username := valueOrDefaultForHTTPTest("TEST_CLICKHOUSE_USER", "stealth")
	password := os.Getenv("TEST_CLICKHOUSE_PASSWORD")
	connection, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{clickHouseAddress},
		Auth: clickhouse.Auth{Database: database, Username: username, Password: password},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	store := telemetry.NewWithConn(connection, telemetry.Config{Database: database, MaxQueryDuration: 5 * time.Second, MaxQueryRange: time.Hour, MaxQueryRows: 100})
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	createAdminTelemetryTestTables(t, ctx, connection)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName:      "stealth_session",
		SessionTTL:             time.Hour,
		StorageRoot:            t.TempDir(),
		StorageMaxFileSize:     1 << 20,
		FunctionsSecretKey:     bytes.Repeat([]byte("k"), 32),
		TelemetryMaxQueryRange: time.Hour,
		TelemetryMaxQueryRows:  100,
	}, repository.New(pool), logger, httpapi.Dependencies{TelemetryStore: store}))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	owner := registerAdminTestAccount(t, ownerClient, server.URL, "telemetry")
	if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id, role) VALUES ($1, 'instance_admin')`, owner.accountID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, owner.accountID, owner.organizationID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM instance_roles WHERE account_id=$1`, owner.accountID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, owner.organizationID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, owner.accountID)
	})

	timestamp := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Microsecond)
	traceID := uuid.Must(uuid.NewV7()).String()
	insertAdminTelemetryTestRows(t, ctx, connection, timestamp, traceID)
	query := "?from=" + url.QueryEscape(timestamp.Add(-time.Minute).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(timestamp.Add(time.Minute).Format(time.RFC3339Nano))

	var logs telemetry.LogsResult
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/admin/telemetry/logs"+query, nil, http.StatusOK, &logs)
	if len(logs.Items) != 1 || logs.Items[0].TraceID != traceID {
		t.Fatalf("Admin logs result = %+v", logs)
	}
	var traces telemetry.TracesResult
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/admin/telemetry/traces"+query+"&trace_id="+url.QueryEscape(traceID), nil, http.StatusOK, &traces)
	if len(traces.Items) != 1 || traces.Items[0].TraceID != traceID {
		t.Fatalf("Admin traces result = %+v", traces)
	}
	var metrics telemetry.MetricsResult
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/admin/telemetry/metrics"+query+"&name=smoke_metric", nil, http.StatusOK, &metrics)
	if len(metrics.Items) != 1 || metrics.Items[0].Name != "smoke_metric" {
		t.Fatalf("Admin metrics result = %+v", metrics)
	}
	if collectorHTTP == "" {
		return
	}

	marker := fmt.Sprintf("admin-telemetry-redaction-%d", time.Now().UnixNano())
	secret := "STEALTH_AUD01_ADMIN_SECRET_123456"
	redactionTraceID := fmt.Sprintf("%032x", time.Now().UnixNano())
	collectorTimestamp := time.Now().UTC().Truncate(time.Second)
	emitAdminTelemetrySignal(t, collectorHTTP, "logs", map[string]any{
		"resourceLogs": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAdminTelemetryAttribute("service.name", "telemetry.admin.integration"),
				stringAdminTelemetryAttribute("smoke.marker", marker),
				stringAdminTelemetryAttribute("client_secret", secret),
			}},
			"scopeLogs": []any{map[string]any{
				"scope": map[string]any{"name": "telemetry.admin.integration"},
				"logRecords": []any{map[string]any{
					"timeUnixNano": fmt.Sprintf("%d", collectorTimestamp.UnixNano()),
					"body":         map[string]any{"stringValue": "password=" + secret + " " + marker},
					"attributes": []any{
						stringAdminTelemetryAttribute("smoke.marker", marker),
						stringAdminTelemetryAttribute("message", "authorization: Bearer "+secret),
					},
				}},
			}},
		}},
	})
	emitAdminTelemetrySignal(t, collectorHTTP, "traces", map[string]any{
		"resourceSpans": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAdminTelemetryAttribute("service.name", "telemetry.admin.integration"),
				stringAdminTelemetryAttribute("smoke.marker", marker),
				stringAdminTelemetryAttribute("access_token", secret),
			}},
			"scopeSpans": []any{map[string]any{
				"scope": map[string]any{"name": "telemetry.admin.integration"},
				"spans": []any{map[string]any{
					"traceId":           redactionTraceID,
					"spanId":            "admin-redaction-span",
					"name":              "GET /admin?token=" + secret,
					"kind":              "SPAN_KIND_SERVER",
					"startTimeUnixNano": fmt.Sprintf("%d", collectorTimestamp.Add(-time.Millisecond).UnixNano()),
					"endTimeUnixNano":   fmt.Sprintf("%d", collectorTimestamp.UnixNano()),
					"attributes": []any{
						stringAdminTelemetryAttribute("smoke.marker", marker),
						stringAdminTelemetryAttribute("api_key", secret),
					},
					"status": map[string]any{"message": "token=" + secret},
				}},
			}},
		}},
	})
	emitAdminTelemetrySignal(t, collectorHTTP, "metrics", map[string]any{
		"resourceMetrics": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				stringAdminTelemetryAttribute("service.name", "telemetry.admin.integration"),
				stringAdminTelemetryAttribute("smoke.marker", marker),
				stringAdminTelemetryAttribute("refresh_token", secret),
			}},
			"scopeMetrics": []any{map[string]any{
				"scope": map[string]any{"name": "telemetry.admin.integration"},
				"metrics": []any{map[string]any{
					"name": "admin.redaction.metric",
					"gauge": map[string]any{"dataPoints": []any{map[string]any{
						"timeUnixNano": fmt.Sprintf("%d", collectorTimestamp.UnixNano()),
						"asDouble":     1.0,
						"attributes": []any{
							stringAdminTelemetryAttribute("smoke.marker", marker),
							stringAdminTelemetryAttribute("private_key", secret),
						},
					}}},
				}},
			}},
		}},
	})

	adminRedactionQuery := "?from=" + url.QueryEscape(collectorTimestamp.Add(-time.Minute).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano))
	var redactedLogs telemetry.LogsResult
	var redactedTraces telemetry.TracesResult
	var redactedMetrics telemetry.MetricsResult
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/admin/telemetry/logs"+adminRedactionQuery+"&query="+url.QueryEscape(marker), nil, http.StatusOK, &redactedLogs)
		requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/admin/telemetry/traces"+adminRedactionQuery+"&trace_id="+url.QueryEscape(redactionTraceID), nil, http.StatusOK, &redactedTraces)
		requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/admin/telemetry/metrics"+adminRedactionQuery+"&name=admin.redaction.metric", nil, http.StatusOK, &redactedMetrics)
		if len(redactedLogs.Items) == 1 && len(redactedTraces.Items) == 1 && len(redactedMetrics.Items) == 1 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(redactedLogs.Items) != 1 || len(redactedTraces.Items) != 1 || len(redactedMetrics.Items) != 1 {
		t.Fatalf("Admin ingest redaction results = logs:%d traces:%d metrics:%d", len(redactedLogs.Items), len(redactedTraces.Items), len(redactedMetrics.Items))
	}
	if strings.Contains(fmt.Sprintf("%+v", redactedLogs.Items[0]), secret) || strings.Contains(fmt.Sprintf("%+v", redactedTraces.Items[0]), secret) || strings.Contains(fmt.Sprintf("%+v", redactedMetrics.Items[0]), secret) {
		t.Fatalf("Admin API returned an ingest secret: logs=%+v traces=%+v metrics=%+v", redactedLogs.Items[0], redactedTraces.Items[0], redactedMetrics.Items[0])
	}
}

func emitAdminTelemetrySignal(t *testing.T, collectorHTTP, signal string, payload map[string]any) {
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
		t.Fatalf("send Admin redaction %s signal: %v", signal, err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("send Admin redaction %s signal returned HTTP %d: %s", signal, response.StatusCode, body)
	}
}

func stringAdminTelemetryAttribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func createAdminTelemetryTestTables(t *testing.T, ctx context.Context, connection driver.Conn) {
	t.Helper()
	// This is an intentionally small fixture for isolated Admin route tests.
	// Production schema compatibility is covered by
	// internal/telemetry/store_integration_test.go, which starts the pinned
	// Collector and reads the tables it creates through ClickHouseStore.
	if os.Getenv("TEST_OTEL_COLLECTOR_HTTP") != "" {
		return
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS otel_logs (Timestamp DateTime64(9), TraceId String, SpanId String, SeverityText String, ServiceName String, Body String, LogAttributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY Timestamp`,
		`CREATE TABLE IF NOT EXISTS otel_traces (Timestamp DateTime64(9), TraceId String, SpanId String, ParentSpanId String, SpanName String, SpanKind String, ServiceName String, Duration UInt64, StatusCode String, StatusMessage String, SpanAttributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY Timestamp`,
		`CREATE TABLE IF NOT EXISTS otel_metrics_gauge (TimeUnix DateTime, MetricName String, ServiceName String, Value Float64, Attributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY TimeUnix`,
		`CREATE TABLE IF NOT EXISTS otel_metrics_sum (TimeUnix DateTime, MetricName String, ServiceName String, Value Float64, Attributes Map(String, String), ResourceAttributes Map(String, String)) ENGINE = MergeTree ORDER BY TimeUnix`,
	} {
		if err := connection.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

func insertAdminTelemetryTestRows(t *testing.T, ctx context.Context, connection driver.Conn, timestamp time.Time, traceID string) {
	t.Helper()
	logs, err := connection.PrepareBatch(ctx, "INSERT INTO otel_logs (Timestamp, TraceId, SpanId, SeverityText, ServiceName, Body, LogAttributes, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Append(timestamp, traceID, "span-http", "ERROR", "api", "collector smoke log", map[string]string{"request_id": "smoke-request"}, map[string]string{"deployment.id": "smoke-deployment"}); err != nil {
		t.Fatal(err)
	}
	if err := logs.Send(); err != nil {
		t.Fatal(err)
	}

	traces, err := connection.PrepareBatch(ctx, "INSERT INTO otel_traces (Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, Duration, StatusCode, StatusMessage, SpanAttributes, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	if err := traces.Append(timestamp, traceID, "span-http", "", "GET /smoke", "Server", "api", uint64(20_000_000), "Ok", "", map[string]string{"http.route": "/smoke"}, map[string]string{"deployment.id": "smoke-deployment"}); err != nil {
		t.Fatal(err)
	}
	if err := traces.Send(); err != nil {
		t.Fatal(err)
	}

	metrics, err := connection.PrepareBatch(ctx, "INSERT INTO otel_metrics_gauge (TimeUnix, MetricName, ServiceName, Value, Attributes, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	if err := metrics.Append(timestamp, "smoke_metric", "api", 1.0, map[string]string{"source": "integration"}, map[string]string{"deployment.id": "smoke-deployment"}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.Send(); err != nil {
		t.Fatal(err)
	}
}

func valueOrDefaultForHTTPTest(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
