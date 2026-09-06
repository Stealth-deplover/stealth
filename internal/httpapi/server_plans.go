package httpapi

import (
	"errors"
	"net/http"

	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
)

func (s *Server) getOrganizationPlan(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	item, err := s.repo.OrganizationPlan(r.Context(), organizationID, mustUUID(accountFrom(r).ID))
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have access to this organization")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "organization plan was not found")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.OrganizationPlan{"plan": item})
}
