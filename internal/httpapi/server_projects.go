package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/google/uuid"
)

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, canManage, err := s.repo.ListProjects(r.Context(), orgID, uuid.Must(uuid.Parse(accountFrom(r).ID)), limit, cursor)
	if authzError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

type projectRequest struct {
	Name string `json:"name"`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	var req projectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := validate.Slug(req.Name, "name")
	if err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateProject(r.Context(), uuid.Must(uuid.NewV7()), orgID, uuid.Must(uuid.Parse(accountFrom(r).ID)), name)
	if planLimitError(w, err) {
		return
	}
	if authzError(w, err) {
		return
	}
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, 409, "conflict", "a project with this name already exists in the organization")
			return
		}
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.Project{"project": item})
}
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	item, err := s.repo.ProjectByID(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "project was not found")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Project{"project": item})
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req projectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := validate.Slug(req.Name, "name")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.UpdateProject(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), name)
	if projectUpdateError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "a project with this name already exists in the organization")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Project{"project": item})
}

type deleteProjectRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// deleteProject permanently removes a project. Requiring the exact current
// project name in the request body makes destructive automation explicit while
// keeping the authorization decision in the repository transaction.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req deleteProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ConfirmName) == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "confirm_name must be the exact project name")
		return
	}
	accountID := uuid.Must(uuid.Parse(accountFrom(r).ID))
	if err := s.repo.DeleteProject(r.Context(), projectID, accountID, req.ConfirmName); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "project was not found")
		case errors.Is(err, repository.ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden", "only the project owner can delete this project")
		case errors.Is(err, repository.ErrConfirmationRequired):
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "confirm_name must be the exact project name")
		default:
			internalError(s, w, err)
		}
		return
	}
	// Database deletion is already committed. Filesystem cleanup is deliberately
	// best-effort: an orphaned opaque artifact is unreachable without a live
	// project row, while returning a 500 would make clients retry a completed
	// destructive operation and obscure the actual state.
	if s.storage != nil {
		if err := s.storage.RemoveProject(projectID); err != nil {
			s.logger.Warn("project storage cleanup failed", "project_id", projectID, "error", err)
		}
	}
	if s.functions != nil {
		if err := s.functions.RemoveProject(projectID); err != nil {
			s.logger.Warn("project function artifact cleanup failed", "project_id", projectID, "error", err)
		}
	}
	if s.siteArchives != nil {
		if err := s.siteArchives.RemoveProject(projectID); err != nil {
			s.logger.Warn("project site source cleanup failed", "project_id", projectID, "error", err)
		}
	}
	if s.sites != nil {
		if err := s.sites.RemoveProject(projectID); err != nil {
			s.logger.Warn("project site artifact cleanup failed", "project_id", projectID, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
