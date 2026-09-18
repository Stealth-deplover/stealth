package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Explorer contains bounded, domain-specific queries used by the admin
// control room. It is intentionally separate from Store so existing API test
// doubles do not gain an accidental obligation to implement every new view.
type Explorer interface {
	QueryLogVolume(context.Context, LogVolumeQuery) (LogVolumeResult, error)
	QueryErrorGroups(context.Context, ErrorGroupsQuery) (ErrorGroupsResult, error)
	QueryServiceMap(context.Context, ServiceMapQuery) (ServiceMapResult, error)
}

// InfrastructureExplorer is kept separate from Explorer so a consumer that
// only needs logs, errors, or topology does not accidentally lose test
// doubles when infrastructure views evolve.
type InfrastructureExplorer interface {
	QueryInfrastructure(context.Context, InfrastructureQuery) (InfrastructureResult, error)
}

type OverviewExplorer interface {
	QueryHTTPOverview(context.Context, HTTPOverviewQuery) (HTTPOverviewResult, error)
}

type HTTPOverviewQuery struct {
	Range TimeRange
}

type HTTPOverviewResult struct {
	RequestRate  float64 `json:"request_rate"`
	ErrorRate    float64 `json:"error_rate"`
	P50LatencyMS float64 `json:"p50_latency_ms"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
	P99LatencyMS float64 `json:"p99_latency_ms"`
	SampleCount  uint64  `json:"sample_count"`
}

type LogVolumeQuery struct {
	Range   TimeRange
	Service string
	Level   string
	Search  string
	Limit   int
}

type LogVolumeBucket struct {
	Timestamp time.Time `json:"timestamp"`
	Count     uint64    `json:"count"`
}

type LogVolumeResult struct {
	Items []LogVolumeBucket `json:"items"`
}

type ErrorGroupsQuery struct {
	Range   TimeRange
	Service string
	Search  string
	Limit   int
}

type ErrorGroup struct {
	Fingerprint     string    `json:"fingerprint"`
	Service         string    `json:"service"`
	ErrorType       string    `json:"error_type"`
	Message         string    `json:"message"`
	FirstSeen       time.Time `json:"first_seen"`
	LastSeen        time.Time `json:"last_seen"`
	OccurrenceCount uint64    `json:"occurrence_count"`
	TraceID         string    `json:"trace_id,omitempty"`
}

type ErrorGroupsResult struct {
	Items []ErrorGroup `json:"items"`
}

type ServiceMapQuery struct {
	Range TimeRange
	Limit int
}

type ServiceMapEdge struct {
	Source       string  `json:"source"`
	Target       string  `json:"target"`
	RequestCount uint64  `json:"request_count"`
	ErrorCount   uint64  `json:"error_count"`
	ErrorRate    float64 `json:"error_rate"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
}

type ServiceMapResult struct {
	Items []ServiceMapEdge `json:"items"`
}

type InfrastructureQuery struct {
	Range TimeRange
	Scope string
	Limit int
}

type InfrastructureMetric struct {
	Timestamp          time.Time         `json:"timestamp"`
	Scope              string            `json:"scope"`
	Name               string            `json:"name"`
	Service            string            `json:"service"`
	Value              float64           `json:"value"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
}

type InfrastructureResult struct {
	Items []InfrastructureMetric `json:"items"`
}

const errorGroupsQuery = `
SELECT ServiceName, SeverityText, Message, min(Timestamp) AS FirstSeen,
       max(Timestamp) AS LastSeen, count() AS OccurrenceCount,
       anyIf(TraceId, TraceId != '') AS TraceID
FROM (
  SELECT Timestamp, TraceId, ServiceName, SeverityText,
         substring(Body, 1, 512) AS Message
  FROM otel_logs
  WHERE Timestamp >= {from:DateTime64(9)}
    AND Timestamp < {to:DateTime64(9)}
    AND upperUTF8(SeverityText) IN ('ERROR', 'FATAL')
    AND ({service:String} = '' OR ServiceName = {service:String})
    AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
)
GROUP BY ServiceName, SeverityText, Message
ORDER BY LastSeen DESC, ServiceName ASC, Message ASC
LIMIT {limit:UInt32}`

const serviceMapQuery = `
SELECT ServiceName,
       if(SpanAttributes['peer.service'] != '', SpanAttributes['peer.service'],
          if(ResourceAttributes['peer.service'] != '', ResourceAttributes['peer.service'],
             if(SpanAttributes['server.address'] != '', SpanAttributes['server.address'], ''))) AS TargetService,
       count() AS RequestCount,
       countIf(upperUTF8(StatusCode) = 'ERROR') AS ErrorCount,
       quantileTDigest(0.95)(toFloat64(Duration) / 1000000.0) AS P95LatencyMS
FROM otel_traces
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
GROUP BY ServiceName, TargetService
HAVING TargetService != '' AND TargetService != ServiceName
ORDER BY RequestCount DESC, ServiceName ASC, TargetService ASC
LIMIT {limit:UInt32}`

const httpOverviewQuery = `
SELECT toFloat64(count()) / {seconds:Float64} AS RequestRate,
       if(count() = 0, 0., toFloat64(countIf(upperUTF8(StatusCode) IN ('ERROR', 'STATUS_CODE_ERROR'))) / toFloat64(count())) AS ErrorRate,
       quantileTDigest(0.50)(toFloat64(Duration) / 1000000.0) AS P50LatencyMS,
       quantileTDigest(0.95)(toFloat64(Duration) / 1000000.0) AS P95LatencyMS,
       quantileTDigest(0.99)(toFloat64(Duration) / 1000000.0) AS P99LatencyMS,
       count() AS SampleCount
FROM otel_traces
WHERE Timestamp >= {from:DateTime64(9)}
  AND Timestamp < {to:DateTime64(9)}
  AND (lowerUTF8(SpanKind) IN ('server', 'span_kind_server')
       OR SpanAttributes['http.request.method'] != ''
       OR SpanAttributes['http.method'] != '')`

func (s *ClickHouseStore) QueryHTTPOverview(ctx context.Context, query HTTPOverviewQuery) (HTTPOverviewResult, error) {
	if err := s.validate(query.Range, 1); err != nil {
		return HTTPOverviewResult{}, err
	}
	seconds := query.Range.To.Sub(query.Range.From).Seconds()
	rows, err := s.query(ctx, httpOverviewQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.Named("seconds", seconds),
	)
	if err != nil {
		return HTTPOverviewResult{}, err
	}
	defer rows.Close()
	var result HTTPOverviewResult
	if rows.Next() {
		if err := rows.Scan(&result.RequestRate, &result.ErrorRate, &result.P50LatencyMS, &result.P95LatencyMS, &result.P99LatencyMS, &result.SampleCount); err != nil {
			return HTTPOverviewResult{}, fmt.Errorf("scan HTTP overview: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return HTTPOverviewResult{}, fmt.Errorf("read HTTP overview: %w", err)
	}
	return result, nil
}

// Infrastructure metrics are intentionally selected by a fixed allowlist of
// signal namespaces. The browser can choose a scope, but it cannot turn this
// endpoint into an arbitrary ClickHouse metric query.
const infrastructureQuery = `
SELECT TimeUnix,
       multiIf(startsWith(MetricName, 'container.'), 'containers',
               startsWith(MetricName, 'postgresql.'), 'postgres',
               startsWith(MetricName, 'redis.'), 'redis',
               startsWith(MetricName, 'system.'), 'host',
               startsWith(MetricName, 'process.'), 'host',
               startsWith(MetricName, 'load.'), 'host',
               startsWith(MetricName, 'disk.'), 'host',
               startsWith(MetricName, 'filesystem.'), 'host',
               startsWith(MetricName, 'network.'), 'host',
               startsWith(MetricName, 'paging.'), 'host',
               'services') AS Scope,
       MetricName, ServiceName, Value, Attributes, ResourceAttributes
FROM (
  SELECT TimeUnix, MetricName, ServiceName, Value, Attributes, ResourceAttributes
  FROM otel_metrics_gauge
  WHERE TimeUnix >= {from:DateTime} AND TimeUnix < {to:DateTime}
  UNION ALL
  SELECT TimeUnix, MetricName, ServiceName, Value, Attributes, ResourceAttributes
  FROM otel_metrics_sum
  WHERE TimeUnix >= {from:DateTime} AND TimeUnix < {to:DateTime}
)
WHERE (startsWith(MetricName, 'system.')
    OR startsWith(MetricName, 'process.')
    OR startsWith(MetricName, 'load.')
    OR startsWith(MetricName, 'disk.')
    OR startsWith(MetricName, 'filesystem.')
    OR startsWith(MetricName, 'network.')
    OR startsWith(MetricName, 'paging.')
    OR startsWith(MetricName, 'container.')
    OR startsWith(MetricName, 'postgresql.')
    OR startsWith(MetricName, 'redis.')
    OR startsWith(MetricName, 'stealth_'))
  AND ({scope:String} = '' OR Scope = {scope:String})
ORDER BY TimeUnix DESC, Scope ASC, ServiceName ASC, MetricName ASC
LIMIT {limit:UInt32}`

const logVolumeMinuteQuery = `
SELECT toStartOfInterval(Timestamp, INTERVAL 1 MINUTE) AS Bucket, count() AS Count
FROM otel_logs
WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
GROUP BY Bucket ORDER BY Bucket ASC LIMIT {limit:UInt32}`

const logVolumeFiveMinuteQuery = `
SELECT toStartOfInterval(Timestamp, INTERVAL 5 MINUTE) AS Bucket, count() AS Count
FROM otel_logs
WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
GROUP BY Bucket ORDER BY Bucket ASC LIMIT {limit:UInt32}`

const logVolumeQuarterHourQuery = `
SELECT toStartOfInterval(Timestamp, INTERVAL 15 MINUTE) AS Bucket, count() AS Count
FROM otel_logs
WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
GROUP BY Bucket ORDER BY Bucket ASC LIMIT {limit:UInt32}`

const logVolumeHourQuery = `
SELECT toStartOfInterval(Timestamp, INTERVAL 1 HOUR) AS Bucket, count() AS Count
FROM otel_logs
WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
GROUP BY Bucket ORDER BY Bucket ASC LIMIT {limit:UInt32}`

const logVolumeDayQuery = `
SELECT toStartOfInterval(Timestamp, INTERVAL 1 DAY) AS Bucket, count() AS Count
FROM otel_logs
WHERE Timestamp >= {from:DateTime64(9)} AND Timestamp < {to:DateTime64(9)}
  AND ({service:String} = '' OR ServiceName = {service:String})
  AND ({level:String} = '' OR SeverityText = {level:String})
  AND ({search:String} = '' OR positionCaseInsensitiveUTF8(Body, {search:String}) > 0)
GROUP BY Bucket ORDER BY Bucket ASC LIMIT {limit:UInt32}`

func (s *ClickHouseStore) QueryLogVolume(ctx context.Context, query LogVolumeQuery) (LogVolumeResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return LogVolumeResult{}, err
	}
	statement := logVolumeQueryFor(query.Range)
	rows, err := s.query(ctx, statement,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.Named("service", boundedFilter(query.Service, 128)),
		clickhouse.Named("level", boundedFilter(query.Level, 64)),
		clickhouse.Named("search", boundedFilter(query.Search, 256)),
		clickhouse.Named("limit", uint32(query.Limit)),
	)
	if err != nil {
		return LogVolumeResult{}, err
	}
	defer rows.Close()
	result := LogVolumeResult{Items: make([]LogVolumeBucket, 0, query.Limit)}
	for rows.Next() {
		var item LogVolumeBucket
		if err := rows.Scan(&item.Timestamp, &item.Count); err != nil {
			return LogVolumeResult{}, fmt.Errorf("scan log volume: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return LogVolumeResult{}, fmt.Errorf("read log volume: %w", err)
	}
	return result, nil
}

func logVolumeQueryFor(queryRange TimeRange) string {
	duration := queryRange.To.Sub(queryRange.From)
	switch {
	case duration <= time.Hour:
		return logVolumeMinuteQuery
	case duration <= 6*time.Hour:
		return logVolumeFiveMinuteQuery
	case duration <= 24*time.Hour:
		return logVolumeQuarterHourQuery
	case duration <= 7*24*time.Hour:
		return logVolumeHourQuery
	default:
		return logVolumeDayQuery
	}
}

func (s *ClickHouseStore) QueryErrorGroups(ctx context.Context, query ErrorGroupsQuery) (ErrorGroupsResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return ErrorGroupsResult{}, err
	}
	rows, err := s.query(ctx, errorGroupsQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.Named("service", boundedFilter(query.Service, 128)),
		clickhouse.Named("search", boundedFilter(query.Search, 256)),
		clickhouse.Named("limit", uint32(query.Limit)),
	)
	if err != nil {
		return ErrorGroupsResult{}, err
	}
	defer rows.Close()
	result := ErrorGroupsResult{Items: make([]ErrorGroup, 0, query.Limit)}
	for rows.Next() {
		var service, severity, message, traceID string
		var firstSeen, lastSeen time.Time
		var count uint64
		if err := rows.Scan(&service, &severity, &message, &firstSeen, &lastSeen, &count, &traceID); err != nil {
			return ErrorGroupsResult{}, fmt.Errorf("scan error group: %w", err)
		}
		message = redactText(message)
		fingerprintInput := service + "\x00" + severity + "\x00" + message
		fingerprint := sha256.Sum256([]byte(fingerprintInput))
		result.Items = append(result.Items, ErrorGroup{
			Fingerprint: hex.EncodeToString(fingerprint[:]), Service: service,
			ErrorType: normalizedErrorType(severity), Message: message,
			FirstSeen: firstSeen, LastSeen: lastSeen, OccurrenceCount: count,
			TraceID: traceID,
		})
	}
	if err := rows.Err(); err != nil {
		return ErrorGroupsResult{}, fmt.Errorf("read error groups: %w", err)
	}
	return result, nil
}

func normalizedErrorType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "error"
	}
	return value
}

func (s *ClickHouseStore) QueryServiceMap(ctx context.Context, query ServiceMapQuery) (ServiceMapResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return ServiceMapResult{}, err
	}
	rows, err := s.query(ctx, serviceMapQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.NanoSeconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.NanoSeconds),
		clickhouse.Named("limit", uint32(query.Limit)),
	)
	if err != nil {
		return ServiceMapResult{}, err
	}
	defer rows.Close()
	result := ServiceMapResult{Items: make([]ServiceMapEdge, 0, query.Limit)}
	for rows.Next() {
		var item ServiceMapEdge
		if err := rows.Scan(&item.Source, &item.Target, &item.RequestCount, &item.ErrorCount, &item.P95LatencyMS); err != nil {
			return ServiceMapResult{}, fmt.Errorf("scan service map edge: %w", err)
		}
		if item.RequestCount > 0 {
			item.ErrorRate = float64(item.ErrorCount) / float64(item.RequestCount)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ServiceMapResult{}, fmt.Errorf("read service map: %w", err)
	}
	return result, nil
}

func (s *ClickHouseStore) QueryInfrastructure(ctx context.Context, query InfrastructureQuery) (InfrastructureResult, error) {
	if err := s.validate(query.Range, query.Limit); err != nil {
		return InfrastructureResult{}, err
	}
	query.Scope = strings.TrimSpace(strings.ToLower(query.Scope))
	if query.Scope != "" && query.Scope != "host" && query.Scope != "containers" && query.Scope != "postgres" && query.Scope != "redis" && query.Scope != "services" {
		return InfrastructureResult{}, fmt.Errorf("%w: infrastructure scope is unsupported", ErrInvalidQuery)
	}
	rows, err := s.query(ctx, infrastructureQuery,
		clickhouse.DateNamed("from", query.Range.From.UTC(), clickhouse.Seconds),
		clickhouse.DateNamed("to", query.Range.To.UTC(), clickhouse.Seconds),
		clickhouse.Named("scope", query.Scope),
		clickhouse.Named("limit", uint32(query.Limit)),
	)
	if err != nil {
		return InfrastructureResult{}, err
	}
	defer rows.Close()
	result := InfrastructureResult{Items: make([]InfrastructureMetric, 0, query.Limit)}
	for rows.Next() {
		var item InfrastructureMetric
		var attributes, resourceAttributes map[string]string
		if err := rows.Scan(&item.Timestamp, &item.Scope, &item.Name, &item.Service, &item.Value, &attributes, &resourceAttributes); err != nil {
			return InfrastructureResult{}, fmt.Errorf("scan infrastructure metric: %w", err)
		}
		item.Attributes = redactAttributes(attributes)
		item.ResourceAttributes = redactAttributes(resourceAttributes)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return InfrastructureResult{}, fmt.Errorf("read infrastructure metrics: %w", err)
	}
	return result, nil
}
