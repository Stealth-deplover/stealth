package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/apikey"
	"github.com/nazxf/stealth-api/internal/auth"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/validate"
)

func (s *Server) listProjectUsers(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursorID *uuid.UUID
	if cursor != "" {
		parsed := uuid.Must(uuid.Parse(cursor))
		cursorID = &parsed
	}
	actor := projectActorFrom(r)
	if actor.kind == apiKeyProjectActor && !apikey.HasScope(actor.scopes, "users.read") {
		writeError(w, http.StatusForbidden, "forbidden", "API key is missing the users.read scope")
		return
	}
	var items []domain.ApplicationUser
	var next string
	var canManage bool
	var err error
	if actor.kind == apiKeyProjectActor {
		items, next, err = s.repo.ListProjectUsersByAPIKey(r.Context(), projectID, limit, cursorID)
		canManage = apikey.HasScope(actor.scopes, "users.write")
	} else {
		items, next, canManage, err = s.repo.ListProjectUsers(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), limit, cursorID)
	}
	if projectResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

type projectUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (s *Server) createProjectUser(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req projectUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := validate.Email(req.Email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	var name *string
	if strings.TrimSpace(req.Name) != "" {
		validated, err := validate.Name(req.Name, "name")
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		name = &validated
	}
	actor := projectActorFrom(r)
	if actor.kind == apiKeyProjectActor {
		if !apikey.HasScope(actor.scopes, "users.write") {
			writeError(w, http.StatusForbidden, "forbidden", "API key is missing the users.write scope")
			return
		}
	} else {
		if err := s.repo.AuthorizeProjectUserWrite(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID))); projectResourceError(w, err) {
			return
		} else if err != nil {
			internalError(s, w, err)
			return
		}
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to create application user")
		return
	}
	var item domain.ApplicationUser
	if actor.kind == apiKeyProjectActor {
		item, err = s.repo.CreateProjectUserByAPIKey(r.Context(), uuid.Must(uuid.NewV7()), projectID, actor.apiKeyID, email, passwordHash, name)
	} else {
		item, err = s.repo.CreateProjectUser(r.Context(), uuid.Must(uuid.NewV7()), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), email, passwordHash, name)
	}
	if projectResourceError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "an application user with this email already exists in the project")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.ApplicationUser{"user": item})
}

func (s *Server) getProjectUser(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	userID, ok := pathUUID(w, r, "userID")
	if !ok {
		return
	}
	actor := projectActorFrom(r)
	if actor.kind == apiKeyProjectActor && !apikey.HasScope(actor.scopes, "users.read") {
		writeError(w, http.StatusForbidden, "forbidden", "API key is missing the users.read scope")
		return
	}
	var item domain.ApplicationUser
	var err error
	if actor.kind == apiKeyProjectActor {
		item, err = s.repo.ProjectUserByIDForAPIKey(r.Context(), projectID, userID)
	} else {
		item, err = s.repo.ProjectUserByID(r.Context(), projectID, userID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	}
	if projectResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.ApplicationUser{"user": item})
}

type projectUserStatusRequest struct {
	Status string `json:"status"`
}

func (s *Server) updateProjectUserStatus(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	userID, ok := pathUUID(w, r, "userID")
	if !ok {
		return
	}
	var req projectUserStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Status != "active" && req.Status != "blocked" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "status must be active or blocked")
		return
	}
	actor := projectActorFrom(r)
	if actor.kind == apiKeyProjectActor && !apikey.HasScope(actor.scopes, "users.write") {
		writeError(w, http.StatusForbidden, "forbidden", "API key is missing the users.write scope")
		return
	}
	var item domain.ApplicationUser
	var err error
	if actor.kind == apiKeyProjectActor {
		item, err = s.repo.UpdateProjectUserStatusByAPIKey(r.Context(), projectID, userID, actor.apiKeyID, req.Status)
	} else {
		item, err = s.repo.UpdateProjectUserStatus(r.Context(), projectID, userID, uuid.Must(uuid.Parse(accountFrom(r).ID)), req.Status)
	}
	if projectResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.ApplicationUser{"user": item})
}

func (s *Server) deleteProjectUser(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	userID, ok := pathUUID(w, r, "userID")
	if !ok {
		return
	}
	actor := projectActorFrom(r)
	var err error
	if actor.kind == apiKeyProjectActor {
		if !apikey.HasScope(actor.scopes, "users.write") {
			writeError(w, http.StatusForbidden, "forbidden", "API key is missing the users.write scope")
			return
		}
		err = s.repo.DeleteProjectUserByAPIKey(r.Context(), projectID, userID, actor.apiKeyID)
	} else {
		err = s.repo.DeleteProjectUser(r.Context(), projectID, userID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	}
	if projectResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func optionalProjectUserName(value string) (*string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	validated, err := validate.Name(value, "name")
	if err != nil {
		return nil, err
	}
	return &validated, nil
}
