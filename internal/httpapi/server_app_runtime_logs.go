package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
)

const (
	defaultAppRuntimeLogsLimit = 100
	maxAppRuntimeLogsLimit     = 250
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
	if len(containerIDs) == 0 {
		writeJSON(w, http.StatusOK, domain.AppRuntimeLogsResponse{Logs: []domain.AppRuntimeLog{}})
		return
	}
	reader, ok := s.telemetry.(telemetry.ContainerLogReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "App runtime logs are temporarily unavailable")
		return
	}
	queryRange, ok := s.adminTimeRange(w, r)
	if !ok {
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
	rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	var cursor *telemetry.LogCursor
	if rawCursor != "" {
		if len(rawCursor) > 2048 {
			writeError(w, http.StatusBadRequest, "validation_error", "runtime log cursor is invalid")
			return
		}
		decoded, err := telemetry.DecodeLogCursor(rawCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "runtime log cursor is invalid")
			return
		}
		cursor = &decoded
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
