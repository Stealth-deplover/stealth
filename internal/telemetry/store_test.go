package telemetry

import (
	"context"
	"errors"
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
	if len(conn.args) != 6 {
		t.Fatalf("argument count = %d, want 6", len(conn.args))
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
