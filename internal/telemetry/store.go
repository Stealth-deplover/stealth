// Package telemetry provides the authenticated, bounded query boundary for
// Stealth's high-volume observability data. The API owns this boundary; the
// browser never receives ClickHouse credentials or arbitrary SQL access.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"math"
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

const logEventIDAttribute = "stealth.log.event_id"

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
	Range    TimeRange
	Service  string
	Level    string
	Search   string
	Limit    int
	After    *LogCursor
	LiveTail bool
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
	EventID            string            `json:"-"`
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
	Timestamp          time.Time                   `json:"timestamp"`
	Name               string                      `json:"name"`
	Service            string                      `json:"service"`
	Value              *float64                    `json:"value,omitempty"`
	Kind               string                      `json:"kind"`
	Attributes         map[string]string           `json:"attributes,omitempty"`
	ResourceAttributes map[string]string           `json:"resource_attributes,omitempty"`
	Histogram          *MetricHistogram            `json:"histogram,omitempty"`
	Summary            *MetricSummary              `json:"summary,omitempty"`
	Exponential        *MetricExponentialHistogram `json:"exponential_histogram,omitempty"`
}

type MetricHistogram struct {
	Count                  uint64    `json:"count"`
	Sum                    float64   `json:"sum"`
	BucketCounts           []uint64  `json:"bucket_counts"`
	ExplicitBounds         []float64 `json:"explicit_bounds"`
	Min                    *float64  `json:"min,omitempty"`
	Max                    *float64  `json:"max,omitempty"`
	AggregationTemporality int32     `json:"aggregation_temporality"`
}

type MetricQuantile struct {
	Quantile float64 `json:"quantile"`
	Value    float64 `json:"value"`
}

type MetricSummary struct {
	Count                  uint64           `json:"count"`
	Sum                    float64          `json:"sum"`
	Quantiles              []MetricQuantile `json:"quantiles"`
	AggregationTemporality int32            `json:"aggregation_temporality,omitempty"`
}

type MetricExponentialHistogram struct {
	Count                  uint64   `json:"count"`
	Sum                    float64  `json:"sum"`
	Scale                  int32    `json:"scale"`
	ZeroCount              uint64   `json:"zero_count"`
	PositiveOffset         int32    `json:"positive_offset"`
	PositiveBucketCounts   []uint64 `json:"positive_bucket_counts"`
	NegativeOffset         int32    `json:"negative_offset"`
	NegativeBucketCounts   []uint64 `json:"negative_bucket_counts"`
	Min                    *float64 `json:"min,omitempty"`
	Max                    *float64 `json:"max,omitempty"`
	AggregationTemporality int32    `json:"aggregation_temporality"`
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

const dockerContainerMetadataQuery = `
SELECT ContainerID,
       argMax(ContainerName, TimeUnix) AS ContainerName,
       argMax(ImageName, TimeUnix) AS ImageName,
       argMax(ImageID, TimeUnix) AS ImageID,
       argMax(ComposeProject, TimeUnix) AS ComposeProject,
       argMax(ComposeService, TimeUnix) AS ComposeService,
       argMax(ComposeContainerNumber, TimeUnix) AS ComposeContainerNumber
FROM (
  SELECT TimeUnix,
         ResourceAttributes['container.id'] AS ContainerID,
         if(ResourceAttributes['container.name'] != '', ResourceAttributes['container.name'], Attributes['container.name']) AS ContainerName,
         if(ResourceAttributes['container.image.name'] != '', ResourceAttributes['container.image.name'], Attributes['container.image.name']) AS ImageName,
         if(ResourceAttributes['container.image.id'] != '', ResourceAttributes['container.image.id'], Attributes['container.image.id']) AS ImageID,
         if(Attributes['docker.compose.project'] != '', Attributes['docker.compose.project'], ResourceAttributes['docker.compose.project']) AS ComposeProject,
         if(Attributes['service.name'] != '', Attributes['service.name'], ResourceAttributes['docker.compose.service']) AS ComposeService,
         if(Attributes['docker.compose.container_number'] != '', Attributes['docker.compose.container_number'], ResourceAttributes['docker.compose.container_number']) AS ComposeContainerNumber
  FROM otel_metrics_gauge
  WHERE TimeUnix >= {from_metrics:DateTime} AND TimeUnix < {to_metrics:DateTime}
  UNION ALL
  SELECT TimeUnix,
         ResourceAttributes['container.id'] AS ContainerID,
         if(ResourceAttributes['container.name'] != '', ResourceAttributes['container.name'], Attributes['container.name']) AS ContainerName,
         if(ResourceAttributes['container.image.name'] != '', ResourceAttributes['container.image.name'], Attributes['container.image.name']) AS ImageName,
         if(ResourceAttributes['container.image.id'] != '', ResourceAttributes['container.image.id'], Attributes['container.image.id']) AS ImageID,
         if(Attributes['docker.compose.project'] != '', Attributes['docker.compose.project'], ResourceAttributes['docker.compose.project']) AS ComposeProject,
         if(Attributes['service.name'] != '', Attributes['service.name'], ResourceAttributes['docker.compose.service']) AS ComposeService,
         if(Attributes['docker.compose.container_number'] != '', Attributes['docker.compose.container_number'], ResourceAttributes['docker.compose.container_number']) AS ComposeContainerNumber
  FROM otel_metrics_sum
  WHERE TimeUnix >= {from_metrics:DateTime} AND TimeUnix < {to_metrics:DateTime}
)
WHERE ContainerID != ''
GROUP BY ContainerID`

const logsQuery = `
WITH container_metadata AS (` + dockerContainerMetadataQuery + `)
SELECT Timestamp, TraceId, SpanId, SeverityText, ServiceName, Body,
       LogAttributes, ResourceAttributes,
       ifNull(container_metadata.ContainerName, ''), ifNull(container_metadata.ImageName, ''),
       ifNull(container_metadata.ImageID, ''), ifNull(container_metadata.ComposeProject, ''),
       ifNull(container_metadata.ComposeService, ''), ifNull(container_metadata.ComposeContainerNumber, ''),
       LogAttributes['stealth.log.event_id'] AS EventID
FROM otel_logs
LEFT JOIN container_metadata ON ResourceAttributes['container.id'] = container_metadata.ContainerID
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
  AND ({live_tail:UInt8} = 0 OR LogAttributes['stealth.log.event_id'] != '')
ORDER BY Timestamp DESC, EventID DESC
LIMIT {limit:UInt32}`

// logsAfterQuery is intentionally separate from logsQuery so the normal
// explorer keeps its newest-first contract while the live tail can advance in
// chronological order from a complete cursor. The tuple predicate mirrors
// every ORDER BY key; timestamp-only polling is not sufficient when several
// records share the same timestamp. Rows without the Collector-generated
// event ID are excluded because they cannot participate in a lossless cursor.
const logsAfterQuery = `
WITH container_metadata AS (` + dockerContainerMetadataQuery + `)
SELECT Timestamp, TraceId, SpanId, SeverityText, ServiceName, Body,
       LogAttributes, ResourceAttributes,
       ifNull(container_metadata.ContainerName, ''), ifNull(container_metadata.ImageName, ''),
       ifNull(container_metadata.ImageID, ''), ifNull(container_metadata.ComposeProject, ''),
       ifNull(container_metadata.ComposeService, ''), ifNull(container_metadata.ComposeContainerNumber, ''),
       LogAttributes['stealth.log.event_id'] AS EventID
FROM otel_logs
LEFT JOIN container_metadata ON ResourceAttributes['container.id'] = container_metadata.ContainerID
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
  AND LogAttributes['stealth.log.event_id'] != ''
  AND (Timestamp, LogAttributes['stealth.log.event_id']) >
      ({after_timestamp:DateTime64(9)}, {after_event_id:String})
ORDER BY Timestamp ASC, EventID ASC
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
SELECT TimeUnix, MetricName, ServiceName, ScalarValue, Attributes, ResourceAttributes,
       MetricKind, MetricCount, MetricSum, BucketCounts, ExplicitBounds,
       Quantiles, QuantileValues, Scale, ZeroCount, PositiveOffset,
       PositiveBucketCounts, NegativeOffset, NegativeBucketCounts, MetricMin,
       MetricMax, AggregationTemporality
FROM (
  SELECT TimeUnix, MetricName, ServiceName, Value AS ScalarValue, Attributes, ResourceAttributes,
         'gauge' AS MetricKind, toUInt64(0) AS MetricCount, toFloat64(0) AS MetricSum,
         emptyArrayUInt64() AS BucketCounts, emptyArrayFloat64() AS ExplicitBounds,
         emptyArrayFloat64() AS Quantiles, emptyArrayFloat64() AS QuantileValues,
         toInt32(0) AS Scale, toUInt64(0) AS ZeroCount, toInt32(0) AS PositiveOffset,
         emptyArrayUInt64() AS PositiveBucketCounts, toInt32(0) AS NegativeOffset,
         emptyArrayUInt64() AS NegativeBucketCounts, toFloat64(0) AS MetricMin,
         toFloat64(0) AS MetricMax, toInt32(0) AS AggregationTemporality
  FROM otel_metrics_gauge
  UNION ALL
  SELECT TimeUnix, MetricName, ServiceName, Value AS ScalarValue, Attributes, ResourceAttributes,
         'sum' AS MetricKind, toUInt64(0) AS MetricCount, toFloat64(0) AS MetricSum,
         emptyArrayUInt64() AS BucketCounts, emptyArrayFloat64() AS ExplicitBounds,
         emptyArrayFloat64() AS Quantiles, emptyArrayFloat64() AS QuantileValues,
         toInt32(0) AS Scale, toUInt64(0) AS ZeroCount, toInt32(0) AS PositiveOffset,
         emptyArrayUInt64() AS PositiveBucketCounts, toInt32(0) AS NegativeOffset,
         emptyArrayUInt64() AS NegativeBucketCounts, toFloat64(0) AS MetricMin,
         toFloat64(0) AS MetricMax, toInt32(0) AS AggregationTemporality
  FROM otel_metrics_sum
  UNION ALL
  SELECT TimeUnix, MetricName, ServiceName, toFloat64(0) AS ScalarValue, Attributes, ResourceAttributes,
         'histogram' AS MetricKind, Count AS MetricCount, Sum AS MetricSum,
         BucketCounts, ExplicitBounds, emptyArrayFloat64() AS Quantiles,
         emptyArrayFloat64() AS QuantileValues, toInt32(0) AS Scale,
         toUInt64(0) AS ZeroCount, toInt32(0) AS PositiveOffset,
         emptyArrayUInt64() AS PositiveBucketCounts, toInt32(0) AS NegativeOffset,
         emptyArrayUInt64() AS NegativeBucketCounts, Min AS MetricMin,
         Max AS MetricMax, AggregationTemporality
  FROM otel_metrics_histogram
  UNION ALL
  SELECT TimeUnix, MetricName, ServiceName, toFloat64(0) AS ScalarValue, Attributes, ResourceAttributes,
         'summary' AS MetricKind, Count AS MetricCount, Sum AS MetricSum,
         emptyArrayUInt64() AS BucketCounts, emptyArrayFloat64() AS ExplicitBounds,
         ValueAtQuantiles.Quantile AS Quantiles,
         ValueAtQuantiles.Value AS QuantileValues, toInt32(0) AS Scale,
         toUInt64(0) AS ZeroCount, toInt32(0) AS PositiveOffset,
         emptyArrayUInt64() AS PositiveBucketCounts, toInt32(0) AS NegativeOffset,
         emptyArrayUInt64() AS NegativeBucketCounts, toFloat64(0) AS MetricMin,
         toFloat64(0) AS MetricMax, toInt32(0) AS AggregationTemporality
  FROM otel_metrics_summary
  UNION ALL
  SELECT TimeUnix, MetricName, ServiceName, toFloat64(0) AS ScalarValue, Attributes, ResourceAttributes,
         'exponential_histogram' AS MetricKind, Count AS MetricCount, Sum AS MetricSum,
         emptyArrayUInt64() AS BucketCounts, emptyArrayFloat64() AS ExplicitBounds,
         emptyArrayFloat64() AS Quantiles, emptyArrayFloat64() AS QuantileValues,
         Scale, ZeroCount, PositiveOffset, PositiveBucketCounts, NegativeOffset,
         NegativeBucketCounts, Min AS MetricMin, Max AS MetricMax,
         AggregationTemporality
  FROM otel_metrics_exp_histogram
)
WHERE TimeUnix >= {from:DateTime}
  AND TimeUnix < {to:DateTime}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({name:String} = '' OR MetricName = {name:String})
ORDER BY TimeUnix DESC, ServiceName ASC, MetricName ASC, MetricKind ASC
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
	  UNION ALL
	  SELECT ServiceName, 'metrics' AS Signal, max(TimeUnix) AS LastReceived, count() AS Volume
	  FROM otel_metrics_histogram
	  WHERE TimeUnix >= {from_metrics:DateTime} AND TimeUnix < {to_metrics:DateTime}
	  GROUP BY ServiceName
	  UNION ALL
	  SELECT ServiceName, 'metrics' AS Signal, max(TimeUnix) AS LastReceived, count() AS Volume
	  FROM otel_metrics_summary
	  WHERE TimeUnix >= {from_metrics:DateTime} AND TimeUnix < {to_metrics:DateTime}
	  GROUP BY ServiceName
	  UNION ALL
	  SELECT ServiceName, 'metrics' AS Signal, max(TimeUnix) AS LastReceived, count() AS Volume
	  FROM otel_metrics_exp_histogram
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
	statement := logsQuery
	args := []any{
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("from_metrics", query.Range.From.UTC(), clickhouse.Seconds),
		clickhouse.DateNamed("to_metrics", query.Range.To.UTC(), clickhouse.Seconds),
		clickhouse.Named("service", boundedFilter(query.Service, 128)),
		clickhouse.Named("level", boundedFilter(query.Level, 64)),
		clickhouse.Named("search", boundedFilter(query.Search, 256)),
	}
	if query.After != nil {
		statement = logsAfterQuery
		args = append(args,
			clickhouse.DateNamed("after_timestamp", query.After.Timestamp.UTC(), clickhouse.NanoSeconds),
			clickhouse.Named("after_event_id", boundedFilter(query.After.EventID, 256)),
		)
	} else {
		args = append(args, clickhouse.Named("live_tail", boolToUint(query.LiveTail)))
	}
	args = append(args, clickhouse.Named("limit", limit))
	rows, err := s.query(ctx, statement, args...)
	if err != nil {
		return LogsResult{}, err
	}
	defer rows.Close()
	result := LogsResult{Items: make([]LogRecord, 0, query.Limit)}
	for rows.Next() {
		var item LogRecord
		var attributes, resourceAttributes map[string]string
		var containerName, imageName, imageID, composeProject, composeService, composeContainerNumber string
		if err := rows.Scan(&item.Timestamp, &item.TraceID, &item.SpanID, &item.Severity, &item.Service, &item.Body, &attributes, &resourceAttributes, &containerName, &imageName, &imageID, &composeProject, &composeService, &composeContainerNumber, &item.EventID); err != nil {
			return LogsResult{}, fmt.Errorf("scan telemetry log: %w", err)
		}
		item.Body = redactText(item.Body)
		delete(attributes, logEventIDAttribute)
		item.Attributes = redactAttributes(attributes)
		item.ResourceAttributes = redactAttributes(resourceAttributes)
		item.ResourceAttributes = enrichContainerAttributes(item.ResourceAttributes, containerName, imageName, imageID, composeProject, composeService, composeContainerNumber)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return LogsResult{}, fmt.Errorf("read telemetry logs: %w", err)
	}
	return result, nil
}

func boolToUint(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

func enrichContainerAttributes(attributes map[string]string, containerName, imageName, imageID, composeProject, composeService, composeContainerNumber string) map[string]string {
	values := map[string]string{
		"container.name":                  containerName,
		"container.image.name":            imageName,
		"container.image.id":              imageID,
		"docker.compose.project":          composeProject,
		"docker.compose.service":          composeService,
		"docker.compose.container_number": composeContainerNumber,
	}
	for key, value := range values {
		if value == "" {
			delete(values, key)
		}
	}
	if len(values) == 0 {
		return attributes
	}
	if attributes == nil {
		attributes = make(map[string]string, len(values))
	} else {
		copy := make(map[string]string, len(attributes)+len(values))
		for key, value := range attributes {
			copy[key] = value
		}
		attributes = copy
	}
	for key, value := range values {
		if _, exists := attributes[key]; !exists || attributes[key] == "" {
			attributes[key] = value
		}
	}
	return attributes
}

func (s *ClickHouseStore) QueryTraces(ctx context.Context, query TracesQuery) (TracesResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return TracesResult{}, err
	}
	if math.IsNaN(query.MinMs) || math.IsInf(query.MinMs, 0) || query.MinMs < 0 || query.MinMs > 24*60*60*1000 {
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
		var scalarValue, metricSum, metricMin, metricMax float64
		var metricCount, zeroCount uint64
		var bucketCounts, positiveBucketCounts, negativeBucketCounts []uint64
		var explicitBounds, quantiles, quantileValues []float64
		var scale, positiveOffset, negativeOffset, aggregationTemporality int32
		if err := rows.Scan(&item.Timestamp, &item.Name, &item.Service, &scalarValue, &attributes, &resourceAttributes, &item.Kind, &metricCount, &metricSum, &bucketCounts, &explicitBounds, &quantiles, &quantileValues, &scale, &zeroCount, &positiveOffset, &positiveBucketCounts, &negativeOffset, &negativeBucketCounts, &metricMin, &metricMax, &aggregationTemporality); err != nil {
			return MetricsResult{}, fmt.Errorf("scan telemetry metric: %w", err)
		}
		switch item.Kind {
		case "gauge", "sum":
			item.Value = finiteMetricValue(scalarValue)
		case "histogram":
			item.Histogram = &MetricHistogram{Count: metricCount, Sum: metricSum, BucketCounts: bucketCounts, ExplicitBounds: explicitBounds, Min: finiteMetricValue(metricMin), Max: finiteMetricValue(metricMax), AggregationTemporality: aggregationTemporality}
		case "summary":
			item.Summary = &MetricSummary{Count: metricCount, Sum: metricSum, Quantiles: metricQuantiles(quantiles, quantileValues), AggregationTemporality: aggregationTemporality}
		case "exponential_histogram":
			item.Exponential = &MetricExponentialHistogram{Count: metricCount, Sum: metricSum, Scale: scale, ZeroCount: zeroCount, PositiveOffset: positiveOffset, PositiveBucketCounts: positiveBucketCounts, NegativeOffset: negativeOffset, NegativeBucketCounts: negativeBucketCounts, Min: finiteMetricValue(metricMin), Max: finiteMetricValue(metricMax), AggregationTemporality: aggregationTemporality}
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

func finiteMetricValue(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func metricQuantiles(quantiles, values []float64) []MetricQuantile {
	count := len(quantiles)
	if len(values) < count {
		count = len(values)
	}
	result := make([]MetricQuantile, 0, count)
	for index := 0; index < count; index++ {
		if math.IsNaN(quantiles[index]) || math.IsInf(quantiles[index], 0) || math.IsNaN(values[index]) || math.IsInf(values[index], 0) {
			continue
		}
		result = append(result, MetricQuantile{Quantile: quantiles[index], Value: values[index]})
	}
	return result
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

var sensitiveKeyPattern = regexp.MustCompile(`(?i)(^|[._-])(password|passwd|pwd|secret|token|api[_-]?key|apikey|authorization|cookie|set-cookie|private[_-]?key|client[_-]?secret|access[_-]?token|refresh[_-]?token)([._-]|$)`)
var sensitiveTextPattern = regexp.MustCompile(`(?i)((?:bearer|basic)\s+|(?:password|passwd|pwd|secret|token|api[_-]?key|apikey|authorization|cookie|set-cookie|private[_-]?key|client[_-]?secret|access[_-]?token|refresh[_-]?token)['"]?\s*[:=]\s*(?:(?:bearer|basic)\s+)?)(["']?)[^\s,;{}"']+`)
var sensitiveURLPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^:/\s]+:)[^@/\s]+`)

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
	value = sensitiveTextPattern.ReplaceAllString(value, `$1$2[REDACTED]`)
	return sensitiveURLPattern.ReplaceAllString(value, `$1[REDACTED]`)
}
