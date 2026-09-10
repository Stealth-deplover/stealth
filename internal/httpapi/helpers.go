package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorEnvelope struct {
	Error apiError `json:"error"`
}
type pagination struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"next_cursor"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	de := json.NewDecoder(r.Body)
	de.DisallowUnknownFields()
	if err := de.Decode(target); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 1 MiB limit")
			return false
		}
		writeError(w, 400, "invalid_request", "request body must be valid JSON")
		return false
	}
	if err := de.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}
func page(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer between 1 and 100")
			return 0, "", false
		}
		limit = parsed
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		if _, err := repository.ParseUUID(cursor); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "cursor must be a UUID")
			return 0, "", false
		}
	}
	return limit, cursor, true
}
func paginationOf(limit int, next string) pagination {
	if next == "" {
		return pagination{Limit: limit}
	}
	return pagination{Limit: limit, NextCursor: &next}
}
func pathUUID(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	id, err := repository.ParseUUID(chi.URLParam(r, key))
	if err != nil {
		writeError(w, 400, "validation_error", fmt.Sprintf("%s must be a UUID", key))
		return uuid.Nil, false
	}
	return id, true
}
func authzError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, 403, "forbidden", "you do not have access to this organization")
		return true
	}
	return false
}

func planLimitError(w http.ResponseWriter, err error) bool {
	var limitErr *repository.PlanLimitError
	if !errors.As(err, &limitErr) {
		return false
	}
	writeError(w, http.StatusConflict, "plan_limit_exceeded", limitErr.Error())
	return true
}

func projectResourceError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project or user was not found")
		return true
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage project users")
		return true
	}
	return false
}

func projectAPIKeyResourceError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project or API key was not found")
		return true
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage project API keys")
		return true
	}
	return false
}

func projectSettingsError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project was not found")
		return true
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to change project Auth settings")
		return true
	}
	return false
}

func projectUpdateError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project was not found")
		return true
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "only project owners and admins can change project settings")
		return true
	}
	return false
}

func internalError(s *Server, w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "request_id", w.Header().Get(requestIDHeader), "error", err)
	writeError(w, 500, "internal_error", "internal server error")
}
