// Package telemetry provides the authenticated, bounded query boundary for
// Stealth's high-volume observability data. The API owns this boundary; the
// browser never receives ClickHouse credentials or arbitrary SQL access.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var (
	ErrDisabled     = errors.New("telemetry backend is disabled")
	ErrInvalidQuery = errors.New("invalid telemetry query")
)

// Config contains only the ClickHouse connection and server-side safety
// limits. Password is held in memory by the process and is never included in
// errors, logs, or query text.
type Config struct {
	Address          string
	Database         string
	Username         string
	Password         string
	MaxQueryDuration time.Duration
	MaxQueryRange    time.Duration
	MaxQueryRows     int
	Retention        time.Duration
}

// Store is the only data access contract exposed to HTTP handlers. New query
// features should add a constrained domain method rather than accepting SQL.
type Store interface {
	Ping(context.Context) error
	QueryLogs(context.Context, LogsQuery) (LogsResult, error)
	QueryTraces(context.Context, TracesQuery) (TracesResult, error)
	QueryMetrics(context.Context, MetricsQuery) (MetricsResult, error)
	ListSources(context.Context, SourcesQuery) (SourcesResult, error)
}

type ClickHouseStore struct {
	conn             driver.Conn
	database         string
	maxQueryDuration time.Duration
	maxQueryRange    time.Duration
	maxQueryRows     int
	retention        time.Duration
}

func New(cfg Config) (*ClickHouseStore, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, ErrDisabled
	}
	if cfg.Database == "" {
		cfg.Database = "stealth_telemetry"
	}
	if !isIdentifier(cfg.Database) {
		return nil, fmt.Errorf("%w: telemetry database must be a valid identifier", ErrInvalidQuery)
	}
	if cfg.Username == "" {
		cfg.Username = "stealth"
	}
	if cfg.MaxQueryDuration <= 0 {
		cfg.MaxQueryDuration = 10 * time.Second
	}
	if cfg.MaxQueryRange <= 0 {
		cfg.MaxQueryRange = 30 * 24 * time.Hour
	}
	if cfg.MaxQueryRows <= 0 {
		cfg.MaxQueryRows = 1000
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 30 * 24 * time.Hour
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: strings.Split(cfg.Address, ","),
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout: 5 * time.Second,
		ReadTimeout: cfg.MaxQueryDuration,
		Compression: &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
	})
	if err != nil {
		return nil, fmt.Errorf("open telemetry store: %w", err)
	}
	return &ClickHouseStore{conn: conn, database: cfg.Database, maxQueryDuration: cfg.MaxQueryDuration, maxQueryRange: cfg.MaxQueryRange, maxQueryRows: cfg.MaxQueryRows, retention: cfg.Retention}, nil
}

// NewWithConn is intentionally small and is useful for integration tests
// that connect to a real ClickHouse instance as well as unit tests that embed
// driver.Conn and inspect the bounded query contract.
func NewWithConn(conn driver.Conn, cfg Config) *ClickHouseStore {
	if cfg.MaxQueryDuration <= 0 {
		cfg.MaxQueryDuration = 10 * time.Second
	}
	if cfg.MaxQueryRange <= 0 {
		cfg.MaxQueryRange = 30 * 24 * time.Hour
	}
	if cfg.MaxQueryRows <= 0 {
		cfg.MaxQueryRows = 1000
	}
	if cfg.Database == "" {
		cfg.Database = "stealth_telemetry"
	}
	return &ClickHouseStore{conn: conn, database: cfg.Database, maxQueryDuration: cfg.MaxQueryDuration, maxQueryRange: cfg.MaxQueryRange, maxQueryRows: cfg.MaxQueryRows, retention: cfg.Retention}
}

func (s *ClickHouseStore) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *ClickHouseStore) Ping(ctx context.Context) error {
	if s == nil || s.conn == nil {
		return ErrDisabled
	}
	if err := s.conn.Ping(ctx); err != nil {
		return fmt.Errorf("ping telemetry backend: %w", err)
	}
	return nil
}

type TimeRange struct {
	From time.Time
	To   time.Time
}

func (r TimeRange) validate(maxRange time.Duration) error {
	if r.From.IsZero() || r.To.IsZero() || r.From.Location() == nil || r.To.Location() == nil {
		return fmt.Errorf("%w: from and to are required", ErrInvalidQuery)
	}
	from, to := r.From.UTC(), r.To.UTC()
	if !to.After(from) {
		return fmt.Errorf("%w: to must be after from", ErrInvalidQuery)
	}
	if maxRange > 0 && to.Sub(from) > maxRange {
		return fmt.Errorf("%w: time range exceeds the configured limit", ErrInvalidQuery)
	}
	if to.After(time.Now().UTC().Add(5 * time.Minute)) {
		return fmt.Errorf("%w: time range cannot extend far into the future", ErrInvalidQuery)
	}
	return nil
}

type LogsQuery struct {
	Range   TimeRange
	Service string
	Level   string
	Search  string
	Limit   int
}

type LogRecord struct {
	Timestamp          time.Time         `json:"timestamp"`
	TraceID            string            `json:"trace_id,omitempty"`
	SpanID             string            `json:"span_id,omitempty"`
	Severity           string            `json:"level,omitempty"`
	Service            string            `json:"service"`
	Body               string            `json:"message"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
}

type LogsResult struct {
	Items []LogRecord `json:"items"`
}

type TracesQuery struct {
	Range   TimeRange
	Service string
	TraceID string
	MinMs   float64
	Limit   int
}

type SpanRecord struct {
	Timestamp          time.Time         `json:"timestamp"`
	TraceID            string            `json:"trace_id"`
	SpanID             string            `json:"span_id"`
	ParentSpanID       string            `json:"parent_span_id,omitempty"`
	Name               string            `json:"name"`
	Kind               string            `json:"kind"`
	Service            string            `json:"service"`
	DurationNs         uint64            `json:"duration_ns"`
	Status             string            `json:"status"`
	StatusMessage      string            `json:"status_message,omitempty"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
}

type TracesResult struct {
	Items []SpanRecord `json:"items"`
}

type MetricsQuery struct {
	Range   TimeRange
	Service string
	Name    string
	Limit   int
}

type MetricRecord struct {
	Timestamp          time.Time         `json:"timestamp"`
	Name               string            `json:"name"`
	Service            string            `json:"service"`
	Value              float64           `json:"value"`
	Kind               string            `json:"kind"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
}

type MetricsResult struct {
	Items []MetricRecord `json:"items"`
}

type SourcesQuery struct {
	Range TimeRange
	Limit int
}

type SourceRecord struct {
	Service      string    `json:"service"`
	Signal       string    `json:"signal"`
	LastReceived time.Time `json:"last_received"`
	Volume       uint64    `json:"volume"`
}

type SourcesResult struct {
	Items []SourceRecord `json:"items"`
}

const logsQuery = `
SELECT Timestamp, TraceId, SpanId, SeverityText, ServiceName, Body,
       LogAttributes, ResourceAttributes
FROM otel_logs
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
ORDER BY Timestamp DESC, TraceId DESC, SpanId DESC
LIMIT {limit:UInt32}`

const tracesQuery = `
SELECT Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind,
       ServiceName, Duration, StatusCode, StatusMessage,
       SpanAttributes, ResourceAttributes
FROM otel_traces
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({trace_id:String} = '' OR TraceId = {trace_id:String})
  AND ({min_duration_ns:UInt64} = 0 OR Duration >= {min_duration_ns:UInt64})
ORDER BY Timestamp DESC, TraceId DESC, SpanId DESC
LIMIT {limit:UInt32}`

const metricsQuery = `
SELECT TimeUnix, MetricName, ServiceName, Value, Attributes, ResourceAttributes, 'gauge' AS MetricKind
FROM otel_metrics_gauge
WHERE TimeUnix >= {from:DateTime}
  AND TimeUnix < {to:DateTime}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({name:String} = '' OR MetricName = {name:String})
UNION ALL
SELECT TimeUnix, MetricName, ServiceName, Value, Attributes, ResourceAttributes, 'sum' AS MetricKind
FROM otel_metrics_sum
WHERE TimeUnix >= {from:DateTime}
  AND TimeUnix < {to:DateTime}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({name:String} = '' OR MetricName = {name:String})
ORDER BY TimeUnix DESC, ServiceName ASC, MetricName ASC
LIMIT {limit:UInt32}`

const sourcesQuery = `
SELECT ServiceName, Signal, max(LastReceived) AS LastReceived, sum(Volume) AS Volume
FROM (
  SELECT ServiceName, 'logs' AS Signal, max(Timestamp) AS LastReceived, count() AS Volume
  FROM otel_logs
  WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}
  GROUP BY ServiceName
  UNION ALL
  SELECT ServiceName, 'traces' AS Signal, max(Timestamp) AS LastReceived, count() AS Volume
  FROM otel_traces
  WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}
  GROUP BY ServiceName
  UNION ALL
	  SELECT ServiceName, 'metrics' AS Signal, max(TimeUnix) AS LastReceived, count() AS Volume
	  FROM otel_metrics_gauge
	  WHERE TimeUnix >= {from_metrics:DateTime} AND TimeUnix < {to_metrics:DateTime}
	  GROUP BY ServiceName
	  UNION ALL
	  SELECT ServiceName, 'metrics' AS Signal, max(TimeUnix) AS LastReceived, count() AS Volume
	  FROM otel_metrics_sum
	  WHERE TimeUnix >= {from_metrics:DateTime} AND TimeUnix < {to_metrics:DateTime}
  GROUP BY ServiceName
)
GROUP BY ServiceName, Signal
ORDER BY LastReceived DESC, ServiceName ASC, Signal ASC
LIMIT {limit:UInt32}`

func (s *ClickHouseStore) QueryLogs(ctx context.Context, query LogsQuery) (LogsResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return LogsResult{}, err
	}
	limit, err := clickHouseLimit(query.Limit)
	if err != nil {
		return LogsResult{}, err
	}
	rows, err := s.query(ctx, logsQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.Named("service", boundedFilter(query.Service, 128)),
		clickhouse.Named("level", boundedFilter(query.Level, 64)),
		clickhouse.Named("search", boundedFilter(query.Search, 256)),
		clickhouse.Named("limit", limit),
	)
	if err != nil {
		return LogsResult{}, err
	}
	defer rows.Close()
	result := LogsResult{Items: make([]LogRecord, 0, query.Limit)}
	for rows.Next() {
		var item LogRecord
		var attributes, resourceAttributes map[string]string
		if err := rows.Scan(&item.Timestamp, &item.TraceID, &item.SpanID, &item.Severity, &item.Service, &item.Body, &attributes, &resourceAttributes); err != nil {
			return LogsResult{}, fmt.Errorf("scan telemetry log: %w", err)
		}
		item.Body = redactText(item.Body)
		item.Attributes = redactAttributes(attributes)
		item.ResourceAttributes = redactAttributes(resourceAttributes)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return LogsResult{}, fmt.Errorf("read telemetry logs: %w", err)
	}
	return result, nil
}

func (s *ClickHouseStore) QueryTraces(ctx context.Context, query TracesQuery) (TracesResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return TracesResult{}, err
	}
	if query.MinMs < 0 || query.MinMs > 24*60*60*1000 {
		return TracesResult{}, fmt.Errorf("%w: min duration is outside the allowed range", ErrInvalidQuery)
	}
	limit, err := clickHouseLimit(query.Limit)
	if err != nil {
		return TracesResult{}, err
	}
	minDuration := uint64(query.MinMs * float64(time.Millisecond))
	rows, err := s.query(ctx, tracesQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.Named("service", boundedFilter(query.Service, 128)),
		clickhouse.Named("trace_id", boundedFilter(query.TraceID, 64)),
		clickhouse.Named("min_duration_ns", minDuration),
		clickhouse.Named("limit", limit),
	)
	if err != nil {
		return TracesResult{}, err
	}
	defer rows.Close()
	result := TracesResult{Items: make([]SpanRecord, 0, query.Limit)}
	for rows.Next() {
		var item SpanRecord
		var attributes, resourceAttributes map[string]string
		if err := rows.Scan(&item.Timestamp, &item.TraceID, &item.SpanID, &item.ParentSpanID, &item.Name, &item.Kind, &item.Service, &item.DurationNs, &item.Status, &item.StatusMessage, &attributes, &resourceAttributes); err != nil {
			return TracesResult{}, fmt.Errorf("scan telemetry span: %w", err)
		}
		item.StatusMessage = redactText(item.StatusMessage)
		item.Attributes = redactAttributes(attributes)
		item.ResourceAttributes = redactAttributes(resourceAttributes)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return TracesResult{}, fmt.Errorf("read telemetry traces: %w", err)
	}
	return result, nil
}

func (s *ClickHouseStore) QueryMetrics(ctx context.Context, query MetricsQuery) (MetricsResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return MetricsResult{}, err
	}
	limit, err := clickHouseLimit(query.Limit)
	if err != nil {
		return MetricsResult{}, err
	}
	rows, err := s.query(ctx, metricsQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.Seconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.Seconds),
		clickhouse.Named("service", boundedFilter(query.Service, 128)),
		clickhouse.Named("name", boundedFilter(query.Name, 256)),
		clickhouse.Named("limit", limit),
	)
	if err != nil {
		return MetricsResult{}, err
	}
	defer rows.Close()
	result := MetricsResult{Items: make([]MetricRecord, 0, query.Limit)}
	for rows.Next() {
		var item MetricRecord
		var attributes, resourceAttributes map[string]string
		if err := rows.Scan(&item.Timestamp, &item.Name, &item.Service, &item.Value, &attributes, &resourceAttributes, &item.Kind); err != nil {
			return MetricsResult{}, fmt.Errorf("scan telemetry metric: %w", err)
		}
		item.Attributes = redactAttributes(attributes)
		item.ResourceAttributes = redactAttributes(resourceAttributes)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return MetricsResult{}, fmt.Errorf("read telemetry metrics: %w", err)
	}
	return result, nil
}

func (s *ClickHouseStore) ListSources(ctx context.Context, query SourcesQuery) (SourcesResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return SourcesResult{}, err
	}
	limit, err := clickHouseLimit(query.Limit)
	if err != nil {
		return SourcesResult{}, err
	}
	rows, err := s.query(ctx, sourcesQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("from_metrics", query.Range.From.UTC(), clickhouse.Seconds),
		clickhouse.DateNamed("to_metrics", query.Range.To.UTC(), clickhouse.Seconds),
		clickhouse.Named("limit", limit),
	)
	if err != nil {
		return SourcesResult{}, err
	}
	defer rows.Close()
	result := SourcesResult{Items: make([]SourceRecord, 0, query.Limit)}
	for rows.Next() {
		var item SourceRecord
		if err := rows.Scan(&item.Service, &item.Signal, &item.LastReceived, &item.Volume); err != nil {
			return SourcesResult{}, fmt.Errorf("scan telemetry source: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return SourcesResult{}, fmt.Errorf("read telemetry sources: %w", err)
	}
	return result, nil
}

func (s *ClickHouseStore) validate(queryRange TimeRange, limit int) error {
	if s == nil || s.conn == nil {
		return ErrDisabled
	}
	if err := queryRange.validate(s.maxQueryRange); err != nil {
		return err
	}
	if limit < 1 || limit > s.maxQueryRows {
		return fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidQuery, s.maxQueryRows)
	}
	return nil
}

func clickHouseLimit(value int) (uint32, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: result limit cannot fit ClickHouse UInt32", ErrInvalidQuery)
	}
	parsed, err := strconv.ParseUint(strconv.FormatInt(int64(value), 10), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: result limit cannot fit ClickHouse UInt32", ErrInvalidQuery)
	}
	return uint32(parsed), nil
}

func (s *ClickHouseStore) query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	queryContext, cancel := context.WithTimeout(ctx, s.maxQueryDuration)
	queryContext = clickhouse.Context(queryContext, clickhouse.WithSettings(clickhouse.Settings{
		"max_execution_time":   uint64(s.maxQueryDuration / time.Second),
		"max_result_rows":      uint64(s.maxQueryRows),
		"max_result_bytes":     uint64(16 << 20),
		"result_overflow_mode": "throw",
	}))
	rows, err := s.conn.Query(queryContext, query, args...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("query telemetry backend: %w", err)
	}
	return &cancellableRows{Rows: rows, cancel: cancel}, nil
}

// cancellableRows keeps the query deadline alive while ClickHouse streams
// rows. Cancelling the context in query() immediately after Query returned
// would make real ClickHouse result sets fail with context.Canceled before
// their first row was read. Every domain query defers Rows.Close, which
// releases the timer and context once scanning is complete.
type cancellableRows struct {
	driver.Rows
	cancel context.CancelFunc
}

func (r *cancellableRows) Close() error {
	err := r.Rows.Close()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return err
}

func boundedFilter(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}

var sensitiveKeyPattern = regexp.MustCompile(`(?i)(pass(word)?|secret|token|authorization|cookie|api[_-]?key|private[_-]?key|client[_-]?secret)`)
var sensitiveTextPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]+|((?:password|secret|token|api[_-]?key|authorization)\s*[:=]\s*(?:bearer\s+)?)` + "[^\\s,;]+")

func redactAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	redacted := make(map[string]string, len(attributes))
	for key, value := range attributes {
		if sensitiveKeyPattern.MatchString(key) {
			redacted[key] = "[REDACTED]"
			continue
		}
		redacted[key] = redactText(value)
	}
	return redacted
}

func redactText(value string) string {
	if value == "" {
		return value
	}
	return sensitiveTextPattern.ReplaceAllString(value, `$1$2[REDACTED]`)
}
