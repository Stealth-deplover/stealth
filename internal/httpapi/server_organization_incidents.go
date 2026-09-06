package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
)

type organizationIncidentRequest struct {
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Status   string   `json:"status"`
	Services []string `json:"services"`
	Message  string   `json:"message"`
}

type organizationIncidentPatchRequest struct {
	Title    *string   `json:"title"`
	Severity *string   `json:"severity"`
	Status   *string   `json:"status"`
	Services *[]string `json:"services"`
	Message  *string   `json:"message"`
}

func (s *Server) listOrganizationIncidents(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, canManage, err := s.repo.ListOrganizationIncidents(r.Context(), organizationID, mustUUID(accountFrom(r).ID), limit, cursor)
	if organizationIncidentError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) getOrganizationIncident(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	incidentID, ok := pathUUID(w, r, "incidentID")
	if !ok {
		return
	}
	item, canManage, err := s.repo.GetOrganizationIncident(r.Context(), organizationID, mustUUID(accountFrom(r).ID), incidentID)
	if organizationIncidentError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incident": item, "can_manage": canManage})
}

func (s *Server) createOrganizationIncident(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	var req organizationIncidentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.repo.CreateOrganizationIncident(r.Context(), uuid.Must(uuid.NewV7()), organizationID, mustUUID(accountFrom(r).ID), repository.OrganizationIncidentInput{
		Title: req.Title, Severity: req.Severity, Status: req.Status, Services: req.Services, Message: req.Message,
	})
	if organizationIncidentError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.OrganizationIncident{"incident": item})
}

func (s *Server) updateOrganizationIncident(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	incidentID, ok := pathUUID(w, r, "incidentID")
	if !ok {
		return
	}
	var req organizationIncidentPatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.repo.UpdateOrganizationIncident(r.Context(), organizationID, incidentID, mustUUID(accountFrom(r).ID), repository.OrganizationIncidentPatch{
		Title: req.Title, Severity: req.Severity, Status: req.Status, Services: req.Services, Message: req.Message,
	})
	if organizationIncidentError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.OrganizationIncident{"incident": item})
}

func organizationIncidentError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have access to this organization or cannot manage incidents")
		return true
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "organization incident was not found")
		return true
	case errors.Is(err, repository.ErrInvalidOrganizationIncident):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return true
	case errors.Is(err, repository.ErrInvalidOrganizationIncidentTransition):
		writeError(w, http.StatusConflict, "conflict", "incident cannot transition from its current status")
		return true
	default:
		return false
	}
}
