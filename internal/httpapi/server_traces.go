package httpapi

import (
	"errors"
	"net/http"

	"github.com/Stealth-deplover/stealth/internal/repository"
)

func (s *Server) listOrganizationTraces(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, err := s.repo.ListOrganizationHTTPTraces(r.Context(), organizationID, mustUUID(accountFrom(r).ID), limit, cursor)
	if organizationTraceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": items, "pagination": paginationOf(limit, next)})
}

func (s *Server) listProjectTraces(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, err := s.repo.ListProjectHTTPTraces(r.Context(), projectID, mustUUID(accountFrom(r).ID), limit, cursor)
	if projectTraceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": items, "pagination": paginationOf(limit, next)})
}

func organizationTraceError(w http.ResponseWriter, err error) bool {
	return traceQueryError(w, err, "you do not have access to this organization")
}

func projectTraceError(w http.ResponseWriter, err error) bool {
	return traceQueryError(w, err, "you do not have access to this project")
}

func traceQueryError(w http.ResponseWriter, err error, forbiddenMessage string) bool {
	switch {
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", forbiddenMessage)
		return true
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "trace scope was not found")
		return true
	case errors.Is(err, repository.ErrInvalidHTTPTrace):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "trace query is invalid")
		return true
	default:
		return false
	}
}
