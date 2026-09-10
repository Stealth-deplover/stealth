package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionstore"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

var (
	functionRuntimePattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,31}$`)
	functionVariablePattern         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,119}$`)
	functionExecutionTriggerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

var supportedFunctionRuntimes = map[string]struct{}{
	"node-22":     {},
	"python-3.13": {},
	"go-1.24":     {},
}

const (
	functionVariableMaxValueBytes       = 64 * 1024
	functionVariableMaxDescriptionBytes = 2000
	functionMaxDescriptionBytes         = 2000
)

// functionRequest is shared by create and patch. Pointer fields preserve the
// distinction between an omitted setting and a false/zero value on PATCH.
type functionRequest struct {
	Name               *string   `json:"name"`
	Runtime            *string   `json:"runtime"`
	Entrypoint         *string   `json:"entrypoint"`
	Commands           *string   `json:"commands"`
	TimeoutSeconds     *int      `json:"timeout_seconds"`
	Enabled            *bool     `json:"enabled"`
	Logging            *bool     `json:"logging"`
	ExecutePermissions *[]string `json:"execute_permissions"`
	Description        *string   `json:"description"`
	ArtifactQuotaBytes *int64    `json:"artifact_quota_bytes"`
}

type functionVariableRequest struct {
	Key         string  `json:"key"`
	Kind        string  `json:"kind"`
	IsSecret    *bool   `json:"is_secret"`
	Value       *string `json:"value"`
	Description *string `json:"description"`
}

type functionVariablePatchRequest struct {
	Key         *string `json:"key"`
	Value       *string `json:"value"`
	Description *string `json:"description"`
}

type functionExecutionRequest struct {
	Trigger string          `json:"trigger"`
	Input   json.RawMessage `json:"input"`
}

func functionActorFrom(r *http.Request) repository.FunctionActor {
	actor, ok := r.Context().Value(projectActorContextKey).(projectActor)
	if !ok {
		return repository.FunctionActor{}
	}
	if actor.kind == apiKeyProjectActor {
		return repository.FunctionActor{Kind: repository.FunctionAPIKeyActor, APIKeyID: actor.apiKeyID, APIKeyScopes: actor.scopes}
	}
	account, ok := r.Context().Value(accountContextKey).(domain.Account)
	if !ok {
		return repository.FunctionActor{}
	}
	return repository.FunctionActor{Kind: repository.FunctionConsoleActor, AccountID: mustUUID(account.ID)}
}

func parseFunctionCreateRequest(s *Server, req functionRequest) (repository.FunctionInput, error) {
	nameValue := ""
	if req.Name != nil {
		nameValue = *req.Name
	}
	name, err := validateFunctionName(nameValue)
	if err != nil {
		return repository.FunctionInput{}, err
	}
	runtimeValue := "node-22"
	if req.Runtime != nil {
		runtimeValue = *req.Runtime
	}
	runtime, err := validateFunctionRuntime(runtimeValue)
	if err != nil {
		return repository.FunctionInput{}, err
	}
	entrypointValue := "src/main.js"
	if req.Entrypoint != nil {
		entrypointValue = *req.Entrypoint
	}
	entrypoint, err := validateFunctionEntrypoint(entrypointValue)
	if err != nil {
		return repository.FunctionInput{}, err
	}
	commandsValue := ""
	if req.Commands != nil {
		commandsValue = *req.Commands
	}
	commands, err := validateFunctionCommands(commandsValue)
	if err != nil {
		return repository.FunctionInput{}, err
	}
	timeout := 15
	if req.TimeoutSeconds != nil {
		timeout = *req.TimeoutSeconds
	}
	if timeout < 1 || timeout > 900 {
		return repository.FunctionInput{}, errors.New("timeout_seconds must be between 1 and 900")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	status := "active"
	if !enabled {
		status = "disabled"
	}
	logging := true
	if req.Logging != nil {
		logging = *req.Logging
	}
	permissions := []string{}
	if req.ExecutePermissions != nil {
		permissions = append([]string(nil), (*req.ExecutePermissions)...)
	}
	quota := s.config.FunctionsDefaultQuotaBytes
	if req.ArtifactQuotaBytes != nil {
		quota = *req.ArtifactQuotaBytes
	}
	if quota <= 0 {
		return repository.FunctionInput{}, errors.New("artifact_quota_bytes must be positive")
	}
	if err := validateFunctionDescription(req.Description); err != nil {
		return repository.FunctionInput{}, err
	}
	return repository.FunctionInput{
		Name:               name,
		Runtime:            runtime,
		Entrypoint:         entrypoint,
		Commands:           commands,
		TimeoutSeconds:     timeout,
		Enabled:            enabled,
		Logging:            logging,
		ExecutePermissions: permissions,
		Description:        req.Description,
		Status:             status,
		ArtifactQuotaBytes: quota,
	}, nil
}

func parseFunctionPatchRequest(req functionRequest) (repository.FunctionPatch, error) {
	patch := repository.FunctionPatch{}
	changed := false
	if req.Name != nil {
		value, err := validateFunctionName(*req.Name)
		if err != nil {
			return patch, err
		}
		patch.Name = &value
		changed = true
	}
	if req.Runtime != nil {
		value, err := validateFunctionRuntime(*req.Runtime)
		if err != nil {
			return patch, err
		}
		patch.Runtime = &value
		changed = true
	}
	if req.Entrypoint != nil {
		value, err := validateFunctionEntrypoint(*req.Entrypoint)
		if err != nil {
			return patch, err
		}
		patch.Entrypoint = &value
		changed = true
	}
	if req.Commands != nil {
		value, err := validateFunctionCommands(*req.Commands)
		if err != nil {
			return patch, err
		}
		patch.Commands = &value
		changed = true
	}
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds < 1 || *req.TimeoutSeconds > 900 {
			return patch, errors.New("timeout_seconds must be between 1 and 900")
		}
		patch.TimeoutSeconds = req.TimeoutSeconds
		changed = true
	}
	if req.Enabled != nil {
		patch.Enabled = req.Enabled
		changed = true
	}
	if req.Logging != nil {
		patch.Logging = req.Logging
		changed = true
	}
	if req.ExecutePermissions != nil {
		value := append([]string(nil), (*req.ExecutePermissions)...)
		patch.ExecutePermissions = &value
		changed = true
	}
	if req.Description != nil {
		if err := validateFunctionDescription(req.Description); err != nil {
			return patch, err
		}
		patch.Description = req.Description
		changed = true
	}
	if req.ArtifactQuotaBytes != nil {
		if *req.ArtifactQuotaBytes <= 0 {
			return patch, errors.New("artifact_quota_bytes must be positive")
		}
		patch.ArtifactQuotaBytes = req.ArtifactQuotaBytes
		changed = true
	}
	if !changed {
		return patch, errors.New("at least one function setting is required")
	}
	return patch, nil
}

func validateFunctionName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`).MatchString(value) {
		return "", errors.New("name must use lowercase letters, numbers, and hyphens and be 2 to 63 characters")
	}
	return value, nil
}

func validateFunctionRuntime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !functionRuntimePattern.MatchString(value) {
		return "", errors.New("runtime is invalid")
	}
	if _, ok := supportedFunctionRuntimes[value]; !ok {
		return "", errors.New("runtime must be one of node-22, python-3.13, or go-1.24")
	}
	return value, nil
}

func validateFunctionEntrypoint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || value == "." || value == ".." || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") {
		return "", errors.New("entrypoint must be a non-empty safe path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("entrypoint must be a non-empty safe path")
		}
	}
	return value, nil
}

func validateFunctionCommands(value string) (string, error) {
	if len(value) > 4000 || strings.ContainsRune(value, '\x00') {
		return "", errors.New("commands must be at most 4000 bytes and cannot contain NUL")
	}
	return value, nil
}

func validateFunctionDescription(value *string) error {
	if value == nil {
		return nil
	}
	if len(*value) > functionMaxDescriptionBytes || strings.ContainsRune(*value, '\x00') {
		return errors.New("description must be at most 2000 bytes and cannot contain NUL")
	}
	return nil
}

func (s *Server) listFunctions(w http.ResponseWriter, r *http.Request) {
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
	items, next, canManage, err := s.repo.ListFunctions(r.Context(), projectID, functionActorFrom(r), limit, cursorID)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"functions": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) getFunction(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	functionID, ok := pathUUID(w, r, "functionID")
	if !ok {
		return
	}
	item, err := s.repo.GetFunction(r.Context(), projectID, functionID, functionActorFrom(r))
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Function{"function": item})
}

func (s *Server) createFunction(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req functionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	input, err := parseFunctionCreateRequest(s, req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateFunction(r.Context(), uuid.Must(uuid.NewV7()), projectID, functionActorFrom(r), input)
	if planLimitError(w, err) {
		return
	}
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.Function{"function": item})
}

func (s *Server) updateFunction(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	functionID, ok := pathUUID(w, r, "functionID")
	if !ok {
		return
	}
	var req functionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	patch, err := parseFunctionPatchRequest(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.UpdateFunction(r.Context(), projectID, functionID, functionActorFrom(r), patch)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Function{"function": item})
}

func (s *Server) deleteFunction(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	functionID, ok := pathUUID(w, r, "functionID")
	if !ok {
		return
	}
	paths, err := s.repo.DeleteFunction(r.Context(), projectID, functionID, functionActorFrom(r))
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if s.functions == nil {
		internalError(s, w, errors.New("function artifact storage is unavailable"))
		return
	}
	for _, path := range paths {
		if err := s.functions.RemoveRelative(path); err != nil {
			internalError(s, w, fmt.Errorf("remove deleted function artifact: %w", err))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFunctionVariables(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, ok := functionPathIDs(w, r)
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
	items, next, canManage, err := s.repo.ListFunctionVariables(r.Context(), projectID, functionID, functionActorFrom(r), limit, cursorID)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) getFunctionVariable(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, variableID, ok := functionVariablePathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetFunctionVariable(r.Context(), projectID, functionID, variableID, functionActorFrom(r))
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.FunctionVariable{"variable": item})
}

func (s *Server) createFunctionVariable(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, ok := functionPathIDs(w, r)
	if !ok {
		return
	}
	var req functionVariableRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !functionVariablePattern.MatchString(req.Key) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "key must start with a letter or underscore and contain only letters, numbers, and underscores")
		return
	}
	if req.Value == nil || len(*req.Value) == 0 || len(*req.Value) > functionVariableMaxValueBytes {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "value must contain between 1 and 65536 bytes")
		return
	}
	if req.Description != nil && (len(*req.Description) > functionVariableMaxDescriptionBytes || strings.ContainsRune(*req.Description, '\x00')) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "description must be at most 2000 bytes and cannot contain NUL")
		return
	}
	kind, secret, err := functionVariableKind(req.Kind, req.IsSecret)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if s.functionCipher == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "function secret encryption is not ready")
		return
	}
	item, err := s.repo.CreateFunctionVariable(r.Context(), uuid.Must(uuid.NewV7()), projectID, functionID, functionActorFrom(r), repository.FunctionVariableInput{Key: req.Key, Kind: kind, IsSecret: &secret, Value: req.Value, Description: req.Description, Cipher: s.functionCipher})
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.FunctionVariable{"variable": item})
}

func (s *Server) updateFunctionVariable(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, variableID, ok := functionVariablePathIDs(w, r)
	if !ok {
		return
	}
	var req functionVariablePatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	patch := repository.FunctionVariablePatch{Key: req.Key, Value: req.Value, Cipher: s.functionCipher}
	patch.SetValue = req.Value != nil
	if req.Description != nil {
		patch.SetDescription = true
	}
	if patch.Key != nil && !functionVariablePattern.MatchString(*patch.Key) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "key must start with a letter or underscore and contain only letters, numbers, and underscores")
		return
	}
	if patch.Value != nil && (len(*patch.Value) == 0 || len(*patch.Value) > functionVariableMaxValueBytes) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "value must contain between 1 and 65536 bytes")
		return
	}
	if patch.Description != nil && (len(*patch.Description) > functionVariableMaxDescriptionBytes || strings.ContainsRune(*patch.Description, '\x00')) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "description must be at most 2000 bytes and cannot contain NUL")
		return
	}
	if patch.SetValue || patch.ClearValue {
		if s.functionCipher == nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "function secret encryption is not ready")
			return
		}
	}
	if patch.Key == nil && !patch.SetValue && !patch.SetDescription {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "at least one variable setting is required")
		return
	}
	item, err := s.repo.UpdateFunctionVariable(r.Context(), projectID, functionID, variableID, functionActorFrom(r), patch)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.FunctionVariable{"variable": item})
}

func (s *Server) deleteFunctionVariable(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, variableID, ok := functionVariablePathIDs(w, r)
	if !ok {
		return
	}
	err := s.repo.DeleteFunctionVariable(r.Context(), projectID, functionID, variableID, functionActorFrom(r))
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFunctionDeployments(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, ok := functionPathIDs(w, r)
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
	items, next, canManage, err := s.repo.ListFunctionDeployments(r.Context(), projectID, functionID, functionActorFrom(r), limit, cursorID)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) getFunctionDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, deploymentID, ok := functionDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetFunctionDeployment(r.Context(), projectID, functionID, deploymentID, functionActorFrom(r))
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.FunctionDeployment{"deployment": item})
}

func (s *Server) uploadFunctionDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, ok := functionPathIDs(w, r)
	if !ok {
		return
	}
	if s.functions == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "function artifact storage is not ready")
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be multipart/form-data")
		return
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	deploymentID := uuid.Must(uuid.NewV7())
	var prepared functionstore.PreparedArtifact
	var sourceName string
	var activate bool
	haveSource, haveActivate := false, false
	cleanup := func() { s.functions.Cleanup(&prepared) }
	defer cleanup()
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			if isMaxBytesError(nextErr) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the configured upload limit")
			} else {
				writeError(w, http.StatusBadRequest, "invalid_request", "invalid multipart upload")
			}
			return
		}
		field := part.FormName()
		switch field {
		case "source":
			if haveSource {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "source may only be provided once")
				return
			}
			haveSource = true
			sourceName = part.FileName()
			if sourceName != "" {
				if err := storage.ValidateFilename(sourceName); err != nil {
					_ = part.Close()
					writeError(w, http.StatusUnprocessableEntity, "validation_error", "source filename is invalid")
					return
				}
			}
			prepared, err = s.functions.BeginUploadWithLimit(r.Context(), projectID, functionID, deploymentID, part, s.config.FunctionsMaxArtifactSize)
			_ = part.Close()
			if errors.Is(err, functionstore.ErrTooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "source artifact exceeds the configured maximum size")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
		case "source_name":
			value, readErr := readFunctionMultipartField(part, 512)
			_ = part.Close()
			if readErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "source_name is invalid")
				return
			}
			sourceName = value
			if err := storage.ValidateFilename(sourceName); err != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "source_name is invalid")
				return
			}
		case "activate":
			if haveActivate {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "activate may only be provided once")
				return
			}
			haveActivate = true
			value, readErr := readFunctionMultipartField(part, 16)
			_ = part.Close()
			if readErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "activate must be true or false")
				return
			}
			activate, err = strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "activate must be true or false")
				return
			}
		default:
			_ = part.Close()
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "unsupported deployment field")
			return
		}
	}
	if !haveSource || prepared.TempPath == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source is required")
		return
	}
	if sourceName == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source filename is required")
		return
	}
	if err := storage.ValidateFilename(sourceName); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source filename is invalid")
		return
	}
	if err := s.functions.Commit(&prepared); err != nil {
		internalError(s, w, err)
		return
	}
	name := sourceName
	actor := functionActorFrom(r)
	var createdBy *uuid.UUID
	if actor.Kind == repository.FunctionConsoleActor && actor.AccountID != uuid.Nil {
		createdBy = &actor.AccountID
	}
	item, err := s.repo.CreateFunctionDeployment(r.Context(), deploymentID, projectID, functionID, actor, repository.FunctionDeploymentInput{Source: "upload", SourceName: &name, SizeBytes: prepared.Size, ChecksumSHA256: prepared.Checksum, SourcePath: prepared.RelativePath, CreatedByAccountID: createdBy, Activate: activate})
	if functionResourceError(w, err) {
		_ = s.functions.RemoveRelative(prepared.RelativePath)
		return
	}
	if err != nil {
		_ = s.functions.RemoveRelative(prepared.RelativePath)
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.FunctionDeployment{"deployment": item})
}

func readFunctionMultipartField(part *multipart.Part, max int64) (string, error) {
	if max <= 0 {
		return "", errors.New("invalid multipart field limit")
	}
	data, err := io.ReadAll(io.LimitReader(part, max+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > max {
		return "", errors.New("multipart field is too large")
	}
	return string(data), nil
}

func (s *Server) deleteFunctionDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, deploymentID, ok := functionDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	paths, err := s.repo.DeleteFunctionDeploymentWithArtifacts(r.Context(), projectID, functionID, deploymentID, functionActorFrom(r))
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if s.functions == nil {
		internalError(s, w, errors.New("function artifact storage is unavailable"))
		return
	}
	for _, path := range paths {
		if err := s.functions.RemoveRelative(path); err != nil {
			internalError(s, w, fmt.Errorf("remove deleted function artifact: %w", err))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) activateFunctionDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, deploymentID, ok := functionDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	actor := functionActorFrom(r)
	item, function, err := s.repo.ActivateFunctionDeployment(r.Context(), projectID, functionID, deploymentID, actor)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"function": function, "deployment": item})
}

func (s *Server) listFunctionExecutions(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, ok := functionPathIDs(w, r)
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
	items, next, err := s.repo.ListFunctionExecutions(r.Context(), projectID, functionID, functionActorFrom(r), limit, cursorID)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": items, "pagination": paginationOf(limit, next)})
}

// createFunctionExecution accepts a bounded JSON payload and only enqueues
// work. The worker is the sole component allowed to read source artifacts or
// start a runtime container; this handler never executes user code inline.
func (s *Server) createFunctionExecution(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, ok := functionPathIDs(w, r)
	if !ok {
		return
	}
	var req functionExecutionRequest
	if r.ContentLength == 0 {
		req.Input = json.RawMessage(`{}`)
	} else if !decodeJSON(w, r, &req) {
		return
	}
	trigger := strings.TrimSpace(req.Trigger)
	if trigger == "" {
		trigger = "manual"
	}
	if !functionExecutionTriggerPattern.MatchString(trigger) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "trigger must start with a letter or number and contain only letters, numbers, dots, underscores, or hyphens")
		return
	}
	input := req.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if len(input) > 65536 {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "input must be valid JSON no larger than 65536 bytes")
		return
	}
	if !json.Valid(input) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "input must be valid JSON")
		return
	}
	var item domain.FunctionExecution
	var err error
	if _, management := r.Context().Value(projectActorContextKey).(projectActor); management {
		item, err = s.repo.CreateFunctionExecutionForActor(r.Context(), uuid.Must(uuid.NewV7()), projectID, functionID, functionActorFrom(r), trigger, input)
	} else {
		var projectUserID *uuid.UUID
		if user, ok := r.Context().Value(projectUserContextKey).(domain.ApplicationUser); ok {
			parsed := mustUUID(user.ID)
			projectUserID = &parsed
		}
		item, err = s.repo.CreateFunctionExecutionForApplication(r.Context(), uuid.Must(uuid.NewV7()), projectID, functionID, projectUserID, trigger, input)
	}
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]domain.FunctionExecution{"execution": item})
}

func (s *Server) getFunctionExecution(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, executionID, ok := functionExecutionPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetFunctionExecution(r.Context(), projectID, functionID, executionID, functionActorFrom(r))
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.FunctionExecution{"execution": item})
}

func (s *Server) listFunctionExecutionLogs(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, executionID, ok := functionExecutionPathIDs(w, r)
	if !ok {
		return
	}
	limit, _, ok := page(w, r)
	if !ok {
		return
	}
	after := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("after")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "after must be a non-negative integer")
			return
		}
		after = parsed
	}
	items, err := s.repo.ListFunctionExecutionLogs(r.Context(), projectID, functionID, executionID, functionActorFrom(r), limit, after)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	next := ""
	if len(items) == limit {
		next = strconv.FormatInt(items[len(items)-1].Sequence, 10)
	}
	var nextCursor *string
	if next != "" {
		nextCursor = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": items, "pagination": pagination{Limit: limit, NextCursor: nextCursor}})
}

func (s *Server) listFunctionBuildLogs(w http.ResponseWriter, r *http.Request) {
	projectID, functionID, deploymentID, ok := functionDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	limit, _, ok := page(w, r)
	if !ok {
		return
	}
	after := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("after")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "after must be a non-negative integer")
			return
		}
		after = parsed
	}
	items, err := s.repo.ListFunctionBuildLogs(r.Context(), projectID, functionID, deploymentID, functionActorFrom(r), limit, after)
	if functionResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	next := ""
	if len(items) == limit {
		next = strconv.FormatInt(items[len(items)-1].Sequence, 10)
	}
	var nextCursor *string
	if next != "" {
		nextCursor = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": items, "pagination": pagination{Limit: limit, NextCursor: nextCursor}})
}

func functionPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	functionID, ok := pathUUID(w, r, "functionID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, functionID, true
}

func functionVariablePathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	projectID, functionID, ok := functionPathIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	variableID, ok := pathUUID(w, r, "variableID")
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return projectID, functionID, variableID, true
}

func functionDeploymentPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	projectID, functionID, ok := functionPathIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	deploymentID, ok := pathUUID(w, r, "deploymentID")
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return projectID, functionID, deploymentID, true
}

func functionExecutionPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	projectID, functionID, ok := functionPathIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	executionID, ok := pathUUID(w, r, "executionID")
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return projectID, functionID, executionID, true
}

func functionVariableKind(kind string, isSecret *bool) (string, bool, error) {
	return normalizeFunctionKind(kind, isSecret)
}

func normalizeFunctionKind(kind string, isSecret *bool) (string, bool, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		if isSecret != nil && *isSecret {
			kind = "secret"
		} else {
			kind = "variable"
		}
	}
	if kind != "variable" && kind != "secret" {
		return "", false, errors.New("kind must be variable or secret")
	}
	secret := kind == "secret"
	if isSecret != nil && *isSecret != secret {
		return "", false, errors.New("kind and secret must agree")
	}
	return kind, secret, nil
}

func functionResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, repository.ErrRowHidden):
		writeError(w, http.StatusNotFound, "not_found", "function resource was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to access this function resource")
		return true
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "function resource conflicts with an existing resource")
		return true
	case errors.Is(err, repository.ErrFunctionQuotaExceeded):
		writeError(w, http.StatusRequestEntityTooLarge, "function_quota_exceeded", "function artifact quota would be exceeded")
		return true
	case errors.Is(err, repository.ErrFunctionArtifactTooLarge), errors.Is(err, functionstore.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "function source artifact exceeds the configured maximum size")
		return true
	case errors.Is(err, repository.ErrFunctionSecretUnavailable):
		writeError(w, http.StatusServiceUnavailable, "not_ready", "function secret encryption is not ready")
		return true
	case errors.Is(err, repository.ErrDeploymentActive), errors.Is(err, repository.ErrInvalidFunctionTransition), errors.Is(err, repository.ErrExecutionNotAvailable), errors.Is(err, repository.ErrFunctionDisabled):
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return true
	case errors.Is(err, repository.ErrInvalidFunctionVariable), errors.Is(err, repository.ErrInvalidFunctionSettings), errors.Is(err, database.ErrInvalidPermissions), errors.Is(err, database.ErrDuplicatePermission), errors.Is(err, storage.ErrInvalidFilename), errors.Is(err, functionstore.ErrInvalidPath):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return true
	}
	return false
}
