package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/validate"
)

func (s *Server) listOrganizations(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, err := s.repo.ListOrganizations(r.Context(), uuid.Must(uuid.Parse(accountFrom(r).ID)), limit, cursor)
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": items, "pagination": paginationOf(limit, next)})
}

type organizationRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request) {
	var req organizationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := validate.Name(req.Name, "name")
	if err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	slug, err := validate.Slug(req.Slug, "slug")
	if err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateOrganization(r.Context(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.Parse(accountFrom(r).ID)), name, slug)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, 409, "conflict", "organization slug is already in use")
			return
		}
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.Organization{"organization": item})
}

func (s *Server) updateOrganization(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	var req organizationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := validate.Name(req.Name, "name")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	slug, err := validate.Slug(req.Slug, "slug")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.UpdateOrganization(r.Context(), organizationID, mustUUID(accountFrom(r).ID), name, slug)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "organization was not found")
		return
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "only organization owners and admins can change organization settings")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "organization slug is already in use")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Organization{"organization": item})
}
func (s *Server) listMemberships(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, canManage, err := s.repo.ListMemberships(r.Context(), orgID, uuid.Must(uuid.Parse(accountFrom(r).ID)), limit, cursor)
	if authzError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberships": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}
