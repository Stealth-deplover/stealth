package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/repository"
)

type agentRequest struct {
	ProjectID    string   `json:"project_id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Role         string   `json:"role"`
	Branch       string   `json:"branch"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	CurrentTask  *string  `json:"current_task"`
	Tools        []string `json:"tools"`
	Instructions *string  `json:"instructions"`
}

type agentPatchRequest struct {
	Name         *string   `json:"name"`
	Description  *string   `json:"description"`
	Role         *string   `json:"role"`
	Branch       *string   `json:"branch"`
	Provider     *string   `json:"provider"`
	Model        *string   `json:"model"`
	CurrentTask  *string   `json:"current_task"`
	Tools        *[]string `json:"tools"`
	Instructions *string   `json:"instructions"`
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursorID *uuid.UUID
	if cursor != "" {
		parsed := mustUUID(cursor)
		cursorID = &parsed
	}
	var projectID *uuid.UUID
	if raw := r.URL.Query().Get("project_id"); raw != "" {
		parsed, err := repository.ParseUUID(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "project_id must be a UUID")
			return
		}
		projectID = &parsed
	}
	items, next, canManage, err := s.repo.ListAgents(r.Context(), mustUUID(accountFrom(r).ID), limit, cursorID, projectID)
	if agentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var req agentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	projectID, err := repository.ParseUUID(req.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "project_id must be a UUID")
		return
	}
	if !s.validAgentProviderModel(req.Provider, req.Model) {
		writeAgentProviderModelError(w)
		return
	}
	item, err := s.repo.CreateAgent(r.Context(), uuid.Must(uuid.NewV7()), mustUUID(accountFrom(r).ID), repository.AgentInput{
		ProjectID: projectID, Name: req.Name, Description: req.Description, Role: req.Role,
		Branch: req.Branch, Provider: req.Provider, Model: req.Model, CurrentTask: req.CurrentTask,
		Tools: req.Tools, Instructions: req.Instructions,
	})
	if agentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": item, "can_manage": true})
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	agentID, ok := pathUUID(w, r, "agentID")
	if !ok {
		return
	}
	item, err := s.repo.AgentByID(r.Context(), mustUUID(accountFrom(r).ID), agentID)
	if agentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	projectID, err := repository.ParseUUID(item.ProjectID)
	if err != nil {
		internalError(s, w, err)
		return
	}
	canManage, err := s.repo.AgentProjectCanManage(r.Context(), mustUUID(accountFrom(r).ID), projectID)
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": item, "can_manage": canManage})
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	agentID, ok := pathUUID(w, r, "agentID")
	if !ok {
		return
	}
	var req agentPatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.Description == nil && req.Role == nil && req.Branch == nil && req.Provider == nil && req.Model == nil && req.CurrentTask == nil && req.Tools == nil && req.Instructions == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "at least one agent field must be provided")
		return
	}
	current, err := s.repo.AgentByID(r.Context(), mustUUID(accountFrom(r).ID), agentID)
	if agentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	provider, model := current.Provider, current.Model
	if req.Provider != nil {
		provider = *req.Provider
	}
	if req.Model != nil {
		model = *req.Model
	}
	if !s.validAgentProviderModel(provider, model) {
		writeAgentProviderModelError(w)
		return
	}
	item, err := s.repo.UpdateAgent(r.Context(), mustUUID(accountFrom(r).ID), agentID, repository.AgentPatch{
		Name: req.Name, Description: req.Description, Role: req.Role, Branch: req.Branch,
		Provider: req.Provider, Model: req.Model, CurrentTask: req.CurrentTask, Tools: req.Tools,
		Instructions: req.Instructions,
	})
	if agentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": item, "can_manage": true})
}

func (s *Server) validAgentProviderModel(provider, model string) bool {
	catalog := config.AgentProviderCatalogOrDefault(s.config.AgentProviderCatalog)
	return config.AgentProviderModelValid(catalog, provider, model)
}

func writeAgentProviderModelError(w http.ResponseWriter) {
	writeError(w, http.StatusUnprocessableEntity, "provider_model_invalid", "model is not supported by the selected provider")
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	agentID, ok := pathUUID(w, r, "agentID")
	if !ok {
		return
	}
	if err := s.repo.DeleteAgent(r.Context(), mustUUID(accountFrom(r).ID), agentID); agentResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func agentResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project or agent was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "only project owners and admins can manage agents")
		return true
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "an agent with this name already exists in the project")
		return true
	case errors.Is(err, repository.ErrInvalidAgent):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "agent settings are invalid")
		return true
	default:
		return false
	}
}
