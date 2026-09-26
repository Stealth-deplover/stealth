package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

var appEnvironmentVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,119}$`)

type appEnvironmentVariableRequest struct {
	Key         string  `json:"key"`
	IsSecret    bool    `json:"is_secret"`
	Value       *string `json:"value"`
	Description *string `json:"description"`
}

type appEnvironmentVariablePatchRequest struct {
	Key            *string `json:"key"`
	IsSecret       *bool   `json:"is_secret"`
	Value          *string `json:"value"`
	ClearValue     bool    `json:"clear_value"`
	Description    *string `json:"description"`
	SetDescription bool    `json:"-"`
}

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
	input.ArtifactQuotaBytes = s.config.AppsDefaultArtifactQuotaBytes
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

func (s *Server) listAppEnvironmentVariables(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
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
	items, next, canManage, err := s.repo.ListAppEnvironmentVariables(r.Context(), projectID, appID, appActorFrom(r), limit, cursorID)
	if appEnvironmentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createAppEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	var req appEnvironmentVariableRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !appEnvironmentVariablePattern.MatchString(req.Key) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "key must start with a letter or underscore and contain only letters, numbers, and underscores")
		return
	}
	if req.Value != nil && (len(*req.Value) > repository.AppEnvironmentVariableMaxValueBytes || strings.ContainsAny(*req.Value, "\x00\r\n")) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "value must be at most 65536 bytes and cannot contain NUL or line breaks")
		return
	}
	if req.Description != nil && (len(*req.Description) > repository.AppEnvironmentVariableMaxDescriptionBytes || strings.ContainsRune(*req.Description, '\x00')) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "description must be at most 2000 bytes and cannot contain NUL")
		return
	}
	if req.Value != nil && s.appSecretCipher == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "App environment encryption is not ready")
		return
	}
	item, err := s.repo.CreateAppEnvironmentVariable(r.Context(), uuid.Must(uuid.NewV7()), projectID, appID, appActorFrom(r), repository.AppEnvironmentVariableInput{
		Key: req.Key, IsSecret: req.IsSecret, Value: req.Value, Description: req.Description, Cipher: s.appSecretCipher,
	})
	if appEnvironmentResourceError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "an App environment variable with this key already exists")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.AppEnvironmentVariable{"variable": item})
}

func (s *Server) updateAppEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	variableID, ok := pathUUID(w, r, "variableID")
	if !ok {
		return
	}
	var req appEnvironmentVariablePatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Key != nil && !appEnvironmentVariablePattern.MatchString(*req.Key) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "key must start with a letter or underscore and contain only letters, numbers, and underscores")
		return
	}
	if req.Value != nil && (len(*req.Value) > repository.AppEnvironmentVariableMaxValueBytes || strings.ContainsAny(*req.Value, "\x00\r\n")) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "value must be at most 65536 bytes and cannot contain NUL or line breaks")
		return
	}
	if req.Description != nil && (len(*req.Description) > repository.AppEnvironmentVariableMaxDescriptionBytes || strings.ContainsRune(*req.Description, '\x00')) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "description must be at most 2000 bytes and cannot contain NUL")
		return
	}
	if req.Value != nil && req.ClearValue {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "value and clear_value cannot be used together")
		return
	}
	if req.Value != nil && s.appSecretCipher == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "App environment encryption is not ready")
		return
	}
	patch := repository.AppEnvironmentVariablePatch{
		Key: req.Key, IsSecret: req.IsSecret, Value: req.Value, ClearValue: req.ClearValue,
		Description: req.Description, SetDescription: req.Description != nil, Cipher: s.appSecretCipher,
	}
	if patch.Key == nil && patch.IsSecret == nil && !patch.SetDescription && patch.Value == nil && !patch.ClearValue {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "at least one environment variable setting is required")
		return
	}
	item, err := s.repo.UpdateAppEnvironmentVariable(r.Context(), projectID, appID, variableID, appActorFrom(r), patch)
	if appEnvironmentResourceError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "an App environment variable with this key already exists")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.AppEnvironmentVariable{"variable": item})
}

func (s *Server) deleteAppEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	variableID, ok := pathUUID(w, r, "variableID")
	if !ok {
		return
	}
	err := s.repo.DeleteAppEnvironmentVariable(r.Context(), projectID, appID, variableID, appActorFrom(r))
	if appEnvironmentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func appEnvironmentResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project or App environment variable was not found")
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage App environment variables in this project")
	case errors.Is(err, repository.ErrInvalidAppEnvironmentVariable):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "App environment variable configuration is invalid")
	case errors.Is(err, repository.ErrAppEnvironmentVariableLimit):
		writeError(w, http.StatusConflict, "limit_exceeded", "App environment variable limit reached")
	case errors.Is(err, repository.ErrAppSecretUnavailable):
		writeError(w, http.StatusServiceUnavailable, "not_ready", "App environment encryption is not ready")
	default:
		return false
	}
	return true
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
	case errors.Is(err, repository.ErrAppArtifactPublishInProgress):
		writeError(w, http.StatusConflict, "conflict", "wait for in-flight App artifact uploads or builds to finish before deleting this App")
	case errors.Is(err, repository.ErrInvalidAppSettings), errors.Is(err, workloadspec.ErrInvalidSpec):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	default:
		return false
	}
	return true
}
