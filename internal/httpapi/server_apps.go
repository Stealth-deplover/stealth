package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

type appRequest struct {
	Name     *string         `json:"name"`
	Enabled  *bool           `json:"enabled"`
	Workload json.RawMessage `json:"workload"`
}

func appActorFrom(r *http.Request) repository.AppActor {
	actor, ok := r.Context().Value(projectActorContextKey).(projectActor)
	if !ok {
		return repository.AppActor{}
	}
	if actor.kind == apiKeyProjectActor {
		return repository.AppActor{Kind: repository.AppAPIKeyActor, APIKeyID: actor.apiKeyID, APIKeyScopes: actor.scopes}
	}
	account, ok := r.Context().Value(accountContextKey).(domain.Account)
	if !ok {
		return repository.AppActor{}
	}
	return repository.AppActor{Kind: repository.AppConsoleActor, AccountID: mustUUID(account.ID)}
}

func parseAppName(value string) (string, error) {
	return validate.Slug(value, "name")
}

func parseCreateAppRequest(req appRequest) (repository.AppInput, error) {
	if req.Name == nil {
		return repository.AppInput{}, errors.New("name is required")
	}
	name, err := parseAppName(*req.Name)
	if err != nil {
		return repository.AppInput{}, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	workload := workloadspec.Default()
	if req.Workload != nil {
		workload, err = workloadspec.Decode(req.Workload)
		if err != nil {
			return repository.AppInput{}, err
		}
	}
	return repository.AppInput{Name: name, Enabled: enabled, Workload: workload}, nil
}

func parseUpdateAppRequest(req appRequest) (repository.AppPatch, error) {
	patch := repository.AppPatch{}
	changed := false
	if req.Name != nil {
		name, err := parseAppName(*req.Name)
		if err != nil {
			return patch, err
		}
		patch.Name = &name
		changed = true
	}
	if req.Enabled != nil {
		patch.Enabled = req.Enabled
		changed = true
	}
	if req.Workload != nil {
		workload, err := workloadspec.Decode(req.Workload)
		if err != nil {
			return patch, err
		}
		patch.Workload = &workload
		changed = true
	}
	if !changed {
		return patch, errors.New("at least one app setting is required")
	}
	return patch, nil
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
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
		parsed := mustUUID(cursor)
		cursorID = &parsed
	}
	items, next, canManage, err := s.repo.ListApps(r.Context(), projectID, appActorFrom(r), limit, cursorID)
	if appResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req appRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	input, err := parseCreateAppRequest(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateApp(r.Context(), uuid.Must(uuid.NewV7()), projectID, appActorFrom(r), input)
	if planLimitError(w, err) {
		return
	}
	if appResourceError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "an App with this name already exists or its platform hostname could not be reserved")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.App{"app": item})
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetApp(r.Context(), projectID, appID, appActorFrom(r))
	if appResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.App{"app": item})
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	var req appRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	patch, err := parseUpdateAppRequest(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.UpdateApp(r.Context(), projectID, appID, appActorFrom(r), patch)
	if appResourceError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "an App with this name already exists")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.App{"app": item})
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeleteApp(r.Context(), projectID, appID, appActorFrom(r)); appResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func appPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	appID, ok := pathUUID(w, r, "appID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, appID, true
}

func appResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project or App was not found")
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage Apps in this project")
	case errors.Is(err, repository.ErrInvalidAppSettings), errors.Is(err, workloadspec.ErrInvalidSpec):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	default:
		return false
	}
	return true
}
