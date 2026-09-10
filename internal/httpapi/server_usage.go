package httpapi

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func (s *Server) getProjectUsage(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	item, err := s.repo.ProjectUsage(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project was not found")
		return
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have access to this project")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": item})
}

func (s *Server) getProjectUsageMetering(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	now := time.Now().UTC()
	from, err := parseUsageDate(r.URL.Query().Get("from"), now.AddDate(0, 0, -29))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "from must use YYYY-MM-DD")
		return
	}
	to, err := parseUsageDate(r.URL.Query().Get("to"), now)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "to must use YYYY-MM-DD")
		return
	}
	item, err := s.repo.ProjectUsageMetering(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), from, to)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project was not found")
		return
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have access to this project")
		return
	}
	if errors.Is(err, repository.ErrInvalidUsageWindow) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "usage window must be between one and 367 calendar days")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format != "" && format != "json" && format != "csv" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "format must be json or csv")
		return
	}
	if format == "csv" {
		body, err := usageMeteringCSV(item)
		if err != nil {
			internalError(s, w, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="stealth-usage-%s-%s-to-%s.csv"`, item.ProjectID, item.From, item.To))
		_, _ = w.Write(body)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metering": item})
}

func usageMeteringCSV(item domain.ProjectUsageMetering) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{"date", "api_request_count", "api_egress_bytes", "function_invocation_count", "function_failure_count", "function_compute_ms"}); err != nil {
		return nil, err
	}
	for _, day := range item.Days {
		if err := writer.Write([]string{
			day.Date,
			strconv.FormatInt(day.APIRequestCount, 10),
			strconv.FormatInt(day.APIEgressBytes, 10),
			strconv.FormatInt(day.FunctionInvocationCount, 10),
			strconv.FormatInt(day.FunctionFailureCount, 10),
			strconv.FormatInt(day.FunctionComputeMS, 10),
		}); err != nil {
			return nil, err
		}
	}
	totals := item.Totals
	if err := writer.Write([]string{
		"TOTAL",
		strconv.FormatInt(totals.APIRequestCount, 10),
		strconv.FormatInt(totals.APIEgressBytes, 10),
		strconv.FormatInt(totals.FunctionInvocationCount, 10),
		strconv.FormatInt(totals.FunctionFailureCount, 10),
		strconv.FormatInt(totals.FunctionComputeMS, 10),
	}); err != nil {
		return nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func parseUsageDate(raw string, fallback time.Time) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
