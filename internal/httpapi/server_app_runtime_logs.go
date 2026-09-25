package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
)

const (
	defaultAppRuntimeLogsLimit = 100
	maxAppRuntimeLogsLimit     = 250
)

var (
	errInvalidAppRuntimeLogRange       = errors.New("time range is invalid or exceeds the configured limit")
	errInvalidAppRuntimeLogCursor      = errors.New("runtime log cursor is invalid")
	errAppRuntimeLogCursorOutsideRange = errors.New("runtime log cursor is outside the allowed query window")
	errAppRuntimeLogFromAfterCursor    = errors.New("from cannot be after the runtime log cursor")
	errAppRuntimeLogToBeforeCursor     = errors.New("to must be after the runtime log cursor")
	errAppRuntimeLogToTooFarFuture     = errors.New("to cannot be more than five minutes in the future")
)

func (s *Server) listAppRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	actor := appActorFrom(r)
	containerIDs, err := s.repo.ListAppRuntimeLogSources(r.Context(), projectID, appID, actor)
	if appResourceError(w, err) {
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "logs_unavailable", "App runtime logs are temporarily unavailable")
		return
	}
	limit, ok := appRuntimeLogsLimit(w, r)
	if !ok {
		return
	}
	level := strings.TrimSpace(r.URL.Query().Get("level"))
	search := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(level) > 64 || len(search) > 256 {
		writeError(w, http.StatusBadRequest, "validation_error", "runtime log filters exceed the allowed length")
		return
	}
	from, err := appRuntimeLogsQueryTime(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	to, err := appRuntimeLogsQueryTime(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	var cursor *telemetry.LogCursor
	if rawCursor != "" {
		if len(rawCursor) > 2048 {
			writeError(w, http.StatusBadRequest, "validation_error", "runtime log cursor is invalid")
			return
		}
		decoded, err := telemetry.DecodeLogCursor(rawCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", errInvalidAppRuntimeLogCursor.Error())
			return
		}
		cursor = &decoded
	}
	queryRange, err := resolveAppRuntimeLogRange(time.Now().UTC(), from, to, cursor, s.config.TelemetryMaxQueryRange)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if len(containerIDs) == 0 {
		writeJSON(w, http.StatusOK, domain.AppRuntimeLogsResponse{Logs: []domain.AppRuntimeLog{}})
		return
	}
	reader, ok := s.telemetry.(telemetry.ContainerLogReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "App runtime logs are temporarily unavailable")
		return
	}
	result, err := reader.QueryContainerLogs(r.Context(), telemetry.ContainerLogsQuery{
		ContainerIDs: containerIDs,
		Range:        queryRange,
		Level:        level,
		Search:       search,
		Limit:        limit,
		After:        cursor,
	})
	if s.writeTelemetryResultError(w, err) {
		return
	}
	response := domain.AppRuntimeLogsResponse{Logs: make([]domain.AppRuntimeLog, 0, len(result.Items))}
	if rawCursor != "" {
		response.NextCursor = rawCursor
	}
	for _, item := range result.Items {
		identity := telemetry.EncodeLogCursor(telemetry.LogCursor{Timestamp: item.Timestamp, EventID: item.EventID})
		response.Logs = append(response.Logs, domain.AppRuntimeLog{
			ID: identity, CreatedAt: item.Timestamp.UTC(), Level: item.Severity, Message: item.Body,
		})
		response.NextCursor = identity
	}
	writeJSON(w, http.StatusOK, response)
}

func appRuntimeLogsQueryTime(r *http.Request, name string) (*time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, errors.New(name + " must be an RFC3339 timestamp")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func resolveAppRuntimeLogRange(now time.Time, explicitFrom, explicitTo *time.Time, cursor *telemetry.LogCursor, maxRange time.Duration) (telemetry.TimeRange, error) {
	if now.IsZero() || maxRange <= 0 {
		return telemetry.TimeRange{}, errInvalidAppRuntimeLogRange
	}
	now = now.UTC()
	to := now
	if explicitTo != nil {
		to = explicitTo.UTC()
	}
	if to.After(now.Add(5 * time.Minute)) {
		return telemetry.TimeRange{}, errAppRuntimeLogToTooFarFuture
	}

	lookback := time.Hour
	if maxRange < lookback {
		lookback = maxRange
	}
	from := to.Add(-lookback)
	if explicitFrom != nil {
		from = explicitFrom.UTC()
	}
	if cursor != nil {
		cursorTime := cursor.Timestamp.UTC()
		if cursor.Timestamp.IsZero() || strings.TrimSpace(cursor.EventID) == "" || len(cursor.EventID) > 256 || cursorTime.After(now) {
			return telemetry.TimeRange{}, errInvalidAppRuntimeLogCursor
		}
		if explicitFrom != nil && from.After(cursorTime) {
			return telemetry.TimeRange{}, errAppRuntimeLogFromAfterCursor
		}
		if explicitFrom == nil {
			from = cursorTime
		}
		if !to.After(cursorTime) {
			return telemetry.TimeRange{}, errAppRuntimeLogToBeforeCursor
		}
		if to.Sub(cursorTime) > maxRange {
			return telemetry.TimeRange{}, errAppRuntimeLogCursorOutsideRange
		}
	}
	if !to.After(from) || to.Sub(from) > maxRange {
		return telemetry.TimeRange{}, errInvalidAppRuntimeLogRange
	}
	return telemetry.TimeRange{From: from, To: to}, nil
}

func appRuntimeLogsLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := defaultAppRuntimeLogsLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer")
			return 0, false
		}
		limit = parsed
	}
	if limit < 1 || limit > maxAppRuntimeLogsLimit {
		writeError(w, http.StatusBadRequest, "validation_error", "limit must be between 1 and 250")
		return 0, false
	}
	return limit, true
}
