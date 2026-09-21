package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type adminComponentStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type adminTelemetryStatus struct {
	Status string `json:"status"`
}

type adminHTTPOverview struct {
	RequestRate  float64 `json:"request_rate"`
	ErrorRate    float64 `json:"error_rate"`
	P50LatencyMS float64 `json:"p50_latency_ms"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
	P99LatencyMS float64 `json:"p99_latency_ms"`
	SampleCount  uint64  `json:"sample_count"`
}

type adminOverviewResponse struct {
	InstanceStatus string                        `json:"instance_status"`
	CheckedAt      time.Time                     `json:"checked_at"`
	Components     []adminComponentStatus        `json:"components"`
	Telemetry      adminTelemetryStatus          `json:"telemetry"`
	HTTP           *adminHTTPOverview            `json:"http,omitempty"`
	Operations     *domain.AdminOperationSummary `json:"operations,omitempty"`
}

type adminOperationsResponse struct {
	Items []domain.AdminOperation `json:"items"`
}

type adminAuditResponse struct {
	Items      []domain.AuditEvent `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

type adminErrorGroupStatusRequest struct {
	Status string `json:"status"`
}

type adminErrorGroupStatusResponse struct {
	Fingerprint string    `json:"fingerprint"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Server) requireInstanceAdmin(next http.Handler) http.Handler {
	return s.requireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.repo == nil {
			internalError(s, w, errors.New("repository is unavailable"))
			return
		}
		allowed, err := s.repo.IsInstanceAdmin(r.Context(), mustUUID(accountFrom(r).ID))
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			internalError(s, w, err)
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "forbidden", "instance owner or admin permission is required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	checkedAt := time.Now().UTC()
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	healthContext, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	components := make([]adminComponentStatus, 0, 6)
	coreHealthy := true

	apiStatus := "healthy"
	components = append(components, adminComponentStatus{Name: "api", Status: apiStatus})

	databaseStatus := "healthy"
	if s.repo == nil || s.repo.Ping(healthContext) != nil {
		databaseStatus = "unavailable"
		coreHealthy = false
	}
	components = append(components, adminComponentStatus{Name: "postgres", Status: databaseStatus})

	redisStatus := "unavailable"
	if s.redis != nil {
		if err := s.redis.Ping(healthContext).Err(); err == nil {
			redisStatus = "healthy"
		} else {
			coreHealthy = false
		}
	} else {
		coreHealthy = false
	}
	components = append(components, adminComponentStatus{Name: "redis", Status: redisStatus})

	clickhouseStatus := "unavailable"
	if s.telemetry != nil {
		if err := s.telemetry.Ping(healthContext); err == nil {
			clickhouseStatus = "healthy"
		}
	}
	components = append(components, adminComponentStatus{Name: "clickhouse", Status: clickhouseStatus})
	collectorStatus := "unknown"
	if s.config.TelemetryCollectorHealthURL != "" {
		collectorStatus = "unavailable"
		if s.collectorHealthy(r.WithContext(healthContext)) {
			collectorStatus = "healthy"
		}
	}
	components = append(components, adminComponentStatus{Name: "otel-collector", Status: collectorStatus})
	telemetryStatus := "unavailable"
	if clickhouseStatus == "healthy" && (collectorStatus == "healthy" || collectorStatus == "unknown") {
		telemetryStatus = "healthy"
	}

	instanceStatus := "degraded"
	if coreHealthy {
		instanceStatus = "healthy"
	}
	var operations *domain.AdminOperationSummary
	if s.repo != nil {
		if summary, err := s.repo.AdminOperationSummary(healthContext); err == nil {
			operations = &summary
		}
	}
	var httpOverview *adminHTTPOverview
	if explorer, ok := s.telemetry.(telemetry.OverviewExplorer); ok {
		if result, err := explorer.QueryHTTPOverview(healthContext, telemetry.HTTPOverviewQuery{Range: queryRange}); err == nil {
			if result.SampleCount > 0 {
				httpOverview = &adminHTTPOverview{RequestRate: result.RequestRate, ErrorRate: result.ErrorRate, P50LatencyMS: result.P50LatencyMS, P95LatencyMS: result.P95LatencyMS, P99LatencyMS: result.P99LatencyMS, SampleCount: result.SampleCount}
			}
		}
	}
	writeJSON(w, http.StatusOK, adminOverviewResponse{
		InstanceStatus: instanceStatus,
		CheckedAt:      checkedAt,
		Components:     components,
		Telemetry:      adminTelemetryStatus{Status: telemetryStatus},
		HTTP:           httpOverview,
		Operations:     operations,
	})
}

func (s *Server) collectorHealthy(r *http.Request) bool {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.config.TelemetryCollectorHealthURL, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
}

func (s *Server) adminTelemetryLogs(w http.ResponseWriter, r *http.Request) {
	if s.telemetry == nil {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	result, err := s.telemetry.QueryLogs(r.Context(), telemetry.LogsQuery{
		Range:   queryRange,
		Service: r.URL.Query().Get("service"),
		Level:   r.URL.Query().Get("level"),
		Search:  r.URL.Query().Get("query"),
		Limit:   limit,
	})
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

// adminTelemetryLogTail keeps the transport streaming and the query bounded.
// It deliberately does not expose ClickHouse's native stream or credentials:
// every poll is still an authenticated, parameterized domain query, and the
// browser receives only redacted log records. The short-lived connection is
// also safe to cancel when the operator navigates away.
func (s *Server) adminTelemetryLogTail(w http.ResponseWriter, r *http.Request) {
	if s.telemetry == nil {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	if limit > 250 {
		limit = 250
	}
	service := r.URL.Query().Get("service")
	level := r.URL.Query().Get("level")
	search := r.URL.Query().Get("query")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		writeError(w, http.StatusInternalServerError, "stream_unavailable", "live log streaming is unavailable")
		return
	}

	var cursor *telemetry.LogCursor
	if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
		if decoded, err := telemetry.DecodeLogCursor(raw); err == nil {
			cursor = &decoded
		}
	}
	maxRange := s.config.TelemetryMaxQueryRange
	if maxRange <= 0 {
		maxRange = 30 * 24 * time.Hour
	}
	lookback := 5 * time.Minute
	if maxRange < lookback {
		lookback = maxRange
	}
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()

	writeEvent := func(event string, value any) bool {
		payload, err := json.Marshal(value)
		if err != nil {
			return false
		}
		if _, err := io.WriteString(w, "event: "+event+"\ndata: "+string(payload)+"\n\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	writeLogEvent := func(item telemetry.LogRecord) bool {
		payload, err := json.Marshal(item)
		if err != nil {
			return false
		}
		id := telemetry.EncodeLogCursor(telemetry.LogCursor{
			Timestamp: item.Timestamp,
			TraceID:   item.TraceID,
			SpanID:    item.SpanID,
			Tie:       item.CursorKey,
		})
		if _, err := io.WriteString(w, "id: "+id+"\nevent: log\ndata: "+string(payload)+"\n\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	writeHeartbeat := func() bool {
		if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		to := time.Now().UTC()
		from := to.Add(-lookback)
		query := telemetry.LogsQuery{
			Range:   telemetry.TimeRange{From: from, To: to},
			Service: service,
			Level:   level,
			Search:  search,
			Limit:   limit,
		}
		if cursor == nil {
			if queryRange.From.After(from) {
				query.Range.From = queryRange.From
			}
		} else {
			query.After = cursor
			query.Range.From = cursor.Timestamp
			if query.Range.From.Before(to.Add(-maxRange)) {
				query.Range.From = to.Add(-maxRange)
			}
			if !to.After(query.Range.From) {
				query.Range.From = to.Add(-time.Nanosecond)
			}
		}
		result, err := s.telemetry.QueryLogs(r.Context(), query)
		if err != nil {
			_ = writeEvent("stream_error", map[string]string{"message": "telemetry backend is unavailable"})
			return
		}
		if cursor == nil {
			// The normal query is newest-first. Emit the initial bounded window
			// oldest-first so the cursor advances monotonically to its tail.
			for index := len(result.Items) - 1; index >= 0; index-- {
				item := result.Items[index]
				itemCursor := telemetry.LogCursor{Timestamp: item.Timestamp, TraceID: item.TraceID, SpanID: item.SpanID, Tie: item.CursorKey}
				cursor = &itemCursor
				if !writeLogEvent(item) {
					return
				}
			}
		} else {
			// Cursor queries are already chronological. The comparison is kept
			// here as a defense-in-depth guard for test doubles and future store
			// implementations.
			for _, item := range result.Items {
				itemCursor := telemetry.LogCursor{Timestamp: item.Timestamp, TraceID: item.TraceID, SpanID: item.SpanID, Tie: item.CursorKey}
				if !itemCursor.After(*cursor) {
					continue
				}
				cursor = &itemCursor
				if !writeLogEvent(item) {
					return
				}
			}
		}
		if !writeHeartbeat() {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-poll.C:
		}
	}
}

func (s *Server) adminTelemetryLogVolume(w http.ResponseWriter, r *http.Request) {
	explorer, ok := s.telemetry.(telemetry.Explorer)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	result, err := explorer.QueryLogVolume(r.Context(), telemetry.LogVolumeQuery{
		Range:   queryRange,
		Service: r.URL.Query().Get("service"),
		Level:   r.URL.Query().Get("level"),
		Search:  r.URL.Query().Get("query"),
		Limit:   limit,
	})
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) adminTelemetryTraces(w http.ResponseWriter, r *http.Request) {
	if s.telemetry == nil {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	minMS, err := parseFloatQuery(r, "min_duration_ms", 0, 24*60*60*1000)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "min_duration_ms must be a non-negative number within one day")
		return
	}
	result, err := s.telemetry.QueryTraces(r.Context(), telemetry.TracesQuery{
		Range:   queryRange,
		Service: r.URL.Query().Get("service"),
		TraceID: r.URL.Query().Get("trace_id"),
		MinMs:   minMS,
		Limit:   limit,
	})
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) adminTelemetryErrors(w http.ResponseWriter, r *http.Request) {
	explorer, ok := s.telemetry.(telemetry.Explorer)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	result, err := explorer.QueryErrorGroups(r.Context(), telemetry.ErrorGroupsQuery{
		Range:   queryRange,
		Service: r.URL.Query().Get("service"),
		Search:  r.URL.Query().Get("query"),
		Limit:   limit,
	})
	if err == nil && s.repo != nil && len(result.Items) > 0 {
		fingerprints := make([]string, 0, len(result.Items))
		for _, item := range result.Items {
			fingerprints = append(fingerprints, item.Fingerprint)
		}
		statuses, statusErr := s.repo.ListAdminErrorGroupStatuses(r.Context(), fingerprints)
		if statusErr != nil {
			internalError(s, w, statusErr)
			return
		}
		for index := range result.Items {
			if status, exists := statuses[result.Items[index].Fingerprint]; exists {
				result.Items[index].Status = status
			}
		}
	}
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) updateAdminTelemetryErrorStatus(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		internalError(s, w, errors.New("repository is unavailable"))
		return
	}
	fingerprint := chi.URLParam(r, "fingerprint")
	var request adminErrorGroupStatusRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	state, err := s.repo.UpdateAdminErrorGroupStatus(r.Context(), mustUUID(accountFrom(r).ID), fingerprint, request.Status)
	if errors.Is(err, repository.ErrInvalidAdminErrorGroup) {
		writeError(w, http.StatusBadRequest, "validation_error", "error group status is invalid")
		return
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner or admin permission is required")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminErrorGroupStatusResponse{
		Fingerprint: state.Fingerprint,
		Status:      state.Status,
		UpdatedAt:   state.UpdatedAt,
	})
}

func (s *Server) adminTelemetryServices(w http.ResponseWriter, r *http.Request) {
	explorer, ok := s.telemetry.(telemetry.Explorer)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	result, err := explorer.QueryServiceMap(r.Context(), telemetry.ServiceMapQuery{Range: queryRange, Limit: limit})
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) adminInfrastructureMetrics(w http.ResponseWriter, r *http.Request) {
	explorer, ok := s.telemetry.(telemetry.InfrastructureExplorer)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	result, err := explorer.QueryInfrastructure(r.Context(), telemetry.InfrastructureQuery{
		Range: queryRange,
		Scope: r.URL.Query().Get("scope"),
		Limit: limit,
	})
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) adminTelemetryMetrics(w http.ResponseWriter, r *http.Request) {
	if s.telemetry == nil {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	result, err := s.telemetry.QueryMetrics(r.Context(), telemetry.MetricsQuery{
		Range:   queryRange,
		Service: r.URL.Query().Get("service"),
		Name:    r.URL.Query().Get("name"),
		Limit:   limit,
	})
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) adminTelemetrySources(w http.ResponseWriter, r *http.Request) {
	if s.telemetry == nil {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	result, err := s.telemetry.ListSources(r.Context(), telemetry.SourcesQuery{Range: queryRange, Limit: limit})
	if !s.writeTelemetryResultError(w, err) {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) adminOperations(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		internalError(s, w, errors.New("repository is unavailable"))
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	items, err := s.repo.ListAdminOperations(r.Context(), queryRange.From, queryRange.To, limit)
	if err != nil {
		if errors.Is(err, repository.ErrInvalidQuery) {
			writeError(w, http.StatusBadRequest, "validation_error", "operations query is invalid")
			return
		}
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminOperationsResponse{Items: items})
}

func (s *Server) adminAuditEvents(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		internalError(s, w, errors.New("repository is unavailable"))
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	var before *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "before must be a UUID cursor")
			return
		}
		before = &parsed
	}
	items, next, err := s.repo.ListInstanceAuditEvents(r.Context(), limit, before)
	if err != nil {
		if errors.Is(err, repository.ErrInvalidQuery) {
			writeError(w, http.StatusBadRequest, "validation_error", "audit query is invalid")
			return
		}
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminAuditResponse{Items: items, NextCursor: next})
}

func (s *Server) adminTimeRange(w http.ResponseWriter, r *http.Request) (telemetry.TimeRange, bool) {
	to := time.Now().UTC()
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "to must be an RFC3339 timestamp")
			return telemetry.TimeRange{}, false
		}
		to = parsed.UTC()
	}
	from := to.Add(-time.Hour)
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "from must be an RFC3339 timestamp")
			return telemetry.TimeRange{}, false
		}
		from = parsed.UTC()
	}
	queryRange := telemetry.TimeRange{From: from, To: to}
	if !to.After(from) || to.Sub(from) > s.config.TelemetryMaxQueryRange {
		writeError(w, http.StatusBadRequest, "validation_error", "time range is invalid or exceeds the configured limit")
		return telemetry.TimeRange{}, false
	}
	return queryRange, true
}

func (s *Server) adminLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer")
			return 0, false
		}
		limit = parsed
	}
	if limit < 1 || limit > s.config.TelemetryMaxQueryRows {
		writeError(w, http.StatusBadRequest, "validation_error", "limit exceeds the configured telemetry query limit")
		return 0, false
	}
	return limit, true
}

func parseFloatQuery(r *http.Request, key string, minimum, maximum float64) (float64, error) {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New("invalid number")
	}
	return parsed, nil
}

func (s *Server) writeTelemetryResultError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, telemetry.ErrInvalidQuery) {
		writeError(w, http.StatusBadRequest, "validation_error", "telemetry query is invalid")
		return true
	}
	// Backend failures are intentionally generic. ClickHouse errors can contain
	// query fragments or deployment-specific details and must not reach the
	// browser.
	writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry backend is unavailable")
	return true
}
