package httpapi

import (
	"errors"
	"net/http"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, err := s.repo.ListAuditEvents(r.Context(), organizationID, uuid.Must(uuid.Parse(accountFrom(r).ID)), limit, cursor)
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have access to this organization")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items, "pagination": paginationOf(limit, next)})
}

func (s *Server) listProjectAuditEvents(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, err := s.repo.ListProjectAuditEvents(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), limit, cursor)
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
	writeJSON(w, http.StatusOK, map[string]any{"events": items, "pagination": paginationOf(limit, next)})
}
