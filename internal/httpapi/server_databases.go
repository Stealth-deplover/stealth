package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	"github.com/Stealth-deplover/stealth/internal/auth"
	dbcore "github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const projectDataActorContextKey contextKey = "project-data-actor"

type projectDataActor struct {
	actor repository.DatabaseActor
}

func (s *Server) requireProjectDataActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID, err := repository.ParseUUID(chi.URLParam(r, "projectID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "projectID must be a UUID")
			return
		}
		// Explicit server credentials win over ambient browser state. This is
		// the only path that accepts X-Stealth-Key; app cookies are never used
		// for Console management authorization.
		if secret := r.Header.Get("X-Stealth-Key"); secret != "" {
			if err := apikey.ValidateSecret(secret); err != nil {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			key, err := s.repo.AuthenticateProjectAPIKey(r.Context(), projectID, apikey.HashSecret(secret))
			if errors.Is(err, repository.ErrNotFound) {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
			keyID, err := repository.ParseUUID(key.ID)
			if err != nil {
				internalError(s, w, err)
				return
			}
			if err := s.repo.TouchProjectAPIKey(r.Context(), keyID); err != nil {
				internalError(s, w, err)
				return
			}
			ctx := context.WithValue(r.Context(), projectDataActorContextKey, projectDataActor{actor: repository.DatabaseActor{Kind: repository.DatabaseAPIKeyActor, APIKeyID: keyID, APIKeyScopes: key.Scopes}})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if cookie, err := r.Cookie(projectSessionCookieName(projectID)); err == nil && cookie.Value != "" {
			user, _, err := s.repo.ApplicationUserBySession(r.Context(), projectID, authHashSessionToken(cookie.Value))
			if errors.Is(err, repository.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "application authentication is required")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
			ctx := context.WithValue(r.Context(), projectDataActorContextKey, projectDataActor{actor: repository.DatabaseActor{Kind: repository.DatabaseApplicationActor, ProjectUserID: mustUUID(user.ID)}})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if cookie, err := r.Cookie(s.config.SessionCookieName); err == nil && cookie.Value != "" {
			account, sessionID, err := s.repo.AccountBySession(r.Context(), authHashSessionToken(cookie.Value))
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			ctx := context.WithValue(r.Context(), accountContextKey, account)
			ctx = context.WithValue(ctx, sessionContextKey, sessionID)
			ctx = context.WithValue(ctx, projectDataActorContextKey, projectDataActor{actor: repository.DatabaseActor{Kind: repository.DatabaseConsoleActor, AccountID: mustUUID(account.ID)}})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		ctx := context.WithValue(r.Context(), projectDataActorContextKey, projectDataActor{actor: repository.DatabaseActor{Kind: repository.DatabaseAnonymousActor}})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// These tiny wrappers keep this file independent from the unexported helpers
// in server.go while preserving the same token hashing implementation.
func authHashSessionToken(token string) []byte { return auth.HashSessionToken(token) }
func mustUUID(value string) uuid.UUID          { parsed, _ := uuid.Parse(value); return parsed }

func databaseActorFrom(r *http.Request) repository.DatabaseActor {
	if value, ok := r.Context().Value(projectDataActorContextKey).(projectDataActor); ok {
		return value.actor
	}
	actor := projectActorFrom(r)
	if actor.kind == apiKeyProjectActor {
		return repository.DatabaseActor{Kind: repository.DatabaseAPIKeyActor, APIKeyID: actor.apiKeyID, APIKeyScopes: actor.scopes}
	}
	return repository.DatabaseActor{Kind: repository.DatabaseConsoleActor, AccountID: mustUUID(accountFrom(r).ID)}
}

type databaseCreateRequest struct {
	Name string `json:"name"`
}

type databaseTableRequest struct {
	Name              string   `json:"name"`
	RowSecurity       *bool    `json:"row_security"`
	CreatePermissions []string `json:"create_permissions"`
	ReadPermissions   []string `json:"read_permissions"`
	UpdatePermissions []string `json:"update_permissions"`
	DeletePermissions []string `json:"delete_permissions"`
}

type databaseColumnRequest struct {
	Key         string          `json:"key"`
	Type        string          `json:"type"`
	Required    bool            `json:"required"`
	VarcharSize *int            `json:"varchar_size"`
	Default     json.RawMessage `json:"default"`
}

type databaseIndexRequest struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	ColumnKeys []string `json:"column_keys"`
	Directions []string `json:"directions"`
}

type databaseRowRequest struct {
	Data              json.RawMessage `json:"data"`
	ReadPermissions   *[]string       `json:"read_permissions"`
	UpdatePermissions *[]string       `json:"update_permissions"`
	DeletePermissions *[]string       `json:"delete_permissions"`
}

func (s *Server) listProjectDatabases(w http.ResponseWriter, r *http.Request) {
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
		parsed, _ := uuid.Parse(cursor)
		cursorID = &parsed
	}
	items, next, canManage, err := s.repo.ListProjectDatabases(r.Context(), projectID, databaseActorFrom(r), limit, cursorID)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createProjectDatabase(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req databaseCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := dbcore.ValidateName(req.Name)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateProjectDatabase(r.Context(), uuid.Must(uuid.NewV7()), projectID, databaseActorFrom(r), name)
	if planLimitError(w, err) {
		return
	}
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.ProjectDatabase{"database": item})
}

func (s *Server) getProjectDatabase(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	item, err := s.repo.GetProjectDatabase(r.Context(), projectID, databaseID, databaseActorFrom(r))
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.ProjectDatabase{"database": item})
}

func (s *Server) deleteProjectDatabase(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	if err := s.repo.DeleteProjectDatabase(r.Context(), projectID, databaseID, databaseActorFrom(r)); databaseResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listDatabaseTables(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursorID *uuid.UUID
	if cursor != "" {
		parsed, _ := uuid.Parse(cursor)
		cursorID = &parsed
	}
	items, next, canManage, err := s.repo.ListDatabaseTables(r.Context(), projectID, databaseID, databaseActorFrom(r), limit, cursorID)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createDatabaseTable(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	var req databaseTableRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := dbcore.ValidateName(req.Name)
	if err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	rowSecurity := true
	if req.RowSecurity != nil {
		rowSecurity = *req.RowSecurity
	}
	item, err := s.repo.CreateDatabaseTable(r.Context(), uuid.Must(uuid.NewV7()), projectID, databaseID, databaseActorFrom(r), repository.DatabaseTableInput{Name: name, RowSecurity: rowSecurity, CreatePermissions: req.CreatePermissions, ReadPermissions: req.ReadPermissions, UpdatePermissions: req.UpdatePermissions, DeletePermissions: req.DeletePermissions})
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.DatabaseTable{"table": item})
}

func (s *Server) getDatabaseTable(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	item, err := s.repo.GetDatabaseTable(r.Context(), projectID, databaseID, tableID, databaseActorFrom(r))
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 200, map[string]domain.DatabaseTable{"table": item})
}

func (s *Server) updateDatabaseTable(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	var req databaseTableRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RowSecurity == nil {
		writeError(w, 422, "validation_error", "row_security is required")
		return
	}
	item, err := s.repo.UpdateDatabaseTable(r.Context(), projectID, databaseID, tableID, databaseActorFrom(r), repository.DatabaseTableInput{RowSecurity: *req.RowSecurity, CreatePermissions: req.CreatePermissions, ReadPermissions: req.ReadPermissions, UpdatePermissions: req.UpdatePermissions, DeletePermissions: req.DeletePermissions})
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 200, map[string]domain.DatabaseTable{"table": item})
}

func (s *Server) deleteDatabaseTable(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	if err := s.repo.DeleteDatabaseTable(r.Context(), projectID, databaseID, tableID, databaseActorFrom(r)); databaseResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listDatabaseColumns(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursorID *uuid.UUID
	if cursor != "" {
		parsed, _ := uuid.Parse(cursor)
		cursorID = &parsed
	}
	items, next, err := s.repo.ListDatabaseColumns(r.Context(), projectID, databaseID, tableID, databaseActorFrom(r), limit, cursorID)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"columns": items, "pagination": paginationOf(limit, next)})
}

func (s *Server) createDatabaseColumn(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	var req databaseColumnRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Default) == 0 {
		req.Default = nil
	}
	var defaultValue any
	hasDefault := len(req.Default) > 0
	if hasDefault {
		decoder := json.NewDecoder(bytes.NewReader(req.Default))
		decoder.UseNumber()
		if err := decoder.Decode(&defaultValue); err != nil {
			writeError(w, 422, "validation_error", "default must be valid JSON")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(w, 422, "validation_error", "default must contain one JSON value")
			return
		}
	}
	item, err := s.repo.CreateDatabaseColumn(r.Context(), uuid.Must(uuid.NewV7()), projectID, databaseID, tableID, databaseActorFrom(r), repository.DatabaseColumnInput{Key: req.Key, Type: dbcore.ColumnType(req.Type), Required: req.Required, VarcharSize: req.VarcharSize, Default: defaultValue, HasDefault: hasDefault})
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 201, map[string]domain.DatabaseColumn{"column": item})
}

func (s *Server) deleteDatabaseColumn(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	columnID, ok := pathUUID(w, r, "columnID")
	if !ok {
		return
	}
	if err := s.repo.DeleteDatabaseColumn(r.Context(), projectID, databaseID, tableID, columnID, databaseActorFrom(r)); databaseResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listDatabaseIndexes(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursorID *uuid.UUID
	if cursor != "" {
		parsed, _ := uuid.Parse(cursor)
		cursorID = &parsed
	}
	items, next, err := s.repo.ListDatabaseIndexes(r.Context(), projectID, databaseID, tableID, databaseActorFrom(r), limit, cursorID)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"indexes": items, "pagination": paginationOf(limit, next)})
}

func (s *Server) createDatabaseIndex(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	var req databaseIndexRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.repo.CreateDatabaseIndex(r.Context(), uuid.Must(uuid.NewV7()), projectID, databaseID, tableID, databaseActorFrom(r), repository.DatabaseIndexInput{Name: req.Name, Type: req.Type, ColumnKeys: req.ColumnKeys, Directions: req.Directions})
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 201, map[string]domain.DatabaseIndex{"index": item})
}

func (s *Server) deleteDatabaseIndex(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	indexID, ok := pathUUID(w, r, "indexID")
	if !ok {
		return
	}
	if err := s.repo.DeleteDatabaseIndex(r.Context(), projectID, databaseID, tableID, indexID, databaseActorFrom(r)); databaseResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listDatabaseRows(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	actor := databaseActorFrom(r)
	schema, err := s.repo.DatabaseTableSchema(r.Context(), projectID, databaseID, tableID, actor)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	query, err := parseRowQuery(r, schema)
	if err != nil {
		writeDatabaseQueryError(w, err)
		return
	}
	items, next, err := s.repo.ListDatabaseRows(r.Context(), projectID, databaseID, tableID, actor, query)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"rows": items, "pagination": paginationOf(query.Limit, next)})
}

func (s *Server) createDatabaseRow(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	var req databaseRowRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	data, err := decodeRowObject(req.Data)
	if err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateDatabaseRow(r.Context(), uuid.Must(uuid.NewV7()), projectID, databaseID, tableID, databaseActorFrom(r), repository.DatabaseRowInput{Data: data, ReadPermissions: req.ReadPermissions, UpdatePermissions: req.UpdatePermissions, DeletePermissions: req.DeletePermissions})
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 201, map[string]domain.DatabaseRow{"row": item})
}

func (s *Server) getDatabaseRow(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	rowID, ok := pathUUID(w, r, "rowID")
	if !ok {
		return
	}
	item, err := s.repo.GetDatabaseRow(r.Context(), projectID, databaseID, tableID, rowID, databaseActorFrom(r))
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 200, map[string]domain.DatabaseRow{"row": item})
}

func (s *Server) updateDatabaseRow(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	rowID, ok := pathUUID(w, r, "rowID")
	if !ok {
		return
	}
	var req databaseRowRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var data map[string]any
	if len(req.Data) > 0 {
		var err error
		data, err = decodeRowObject(req.Data)
		if err != nil {
			writeError(w, 422, "validation_error", err.Error())
			return
		}
	}
	item, err := s.repo.UpdateDatabaseRow(r.Context(), projectID, databaseID, tableID, rowID, databaseActorFrom(r), repository.DatabaseRowPatch{Data: data, ReadPermissions: req.ReadPermissions, UpdatePermissions: req.UpdatePermissions, DeletePermissions: req.DeletePermissions})
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, 200, map[string]domain.DatabaseRow{"row": item})
}

func (s *Server) deleteDatabaseRow(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	rowID, ok := pathUUID(w, r, "rowID")
	if !ok {
		return
	}
	if err := s.repo.DeleteDatabaseRow(r.Context(), projectID, databaseID, tableID, rowID, databaseActorFrom(r)); databaseResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(204)
}

func decodeRowObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("data must be a JSON object")
	}
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil || data == nil {
		return nil, errors.New("data must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("data must contain one JSON value")
	}
	return data, nil
}

func parseRowQuery(r *http.Request, schema repository.DatabaseTableSchema) (repository.RowQuery, error) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return repository.RowQuery{}, fmt.Errorf("%w: limit must be between 1 and 100", repository.ErrInvalidQuery)
		}
		limit = value
	}
	var cursor *repository.RowCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		value, err := repository.DecodeRowCursor(raw)
		if err != nil {
			return repository.RowQuery{}, err
		}
		cursor = &value
	}
	byKey := make(map[string]repository.DatabaseColumnSchema, len(schema.Columns))
	for _, column := range schema.Columns {
		byKey[column.Key] = column
	}
	filters := make([]repository.RowFilter, 0)
	query := r.URL.Query()
	for key, values := range query {
		if !strings.HasPrefix(key, "filter") || key == "filter" {
			continue
		}
		if len(values) != 1 {
			return repository.RowQuery{}, fmt.Errorf("%w: filter must occur once", repository.ErrInvalidQuery)
		}
		columnKey, ok := filterColumnKey(key)
		if !ok {
			return repository.RowQuery{}, fmt.Errorf("%w: filter name is invalid", repository.ErrInvalidQuery)
		}
		column, ok := byKey[columnKey]
		if !ok {
			return repository.RowQuery{}, fmt.Errorf("%w: filter column is not declared", repository.ErrInvalidQuery)
		}
		value, err := dbcore.ParseQueryValue(dbcore.ColumnDefinition{Key: column.Key, Type: column.Type, VarcharSize: column.VarcharSize}, values[0])
		if err != nil {
			return repository.RowQuery{}, err
		}
		filters = append(filters, repository.RowFilter{Column: column, Value: value})
	}
	if raw := query.Get("filter"); raw != "" {
		var object map[string]string
		decoder := json.NewDecoder(strings.NewReader(raw))
		if err := decoder.Decode(&object); err != nil {
			return repository.RowQuery{}, fmt.Errorf("%w: filter must be an object", repository.ErrInvalidQuery)
		}
		for key, value := range object {
			column, ok := byKey[key]
			if !ok {
				return repository.RowQuery{}, fmt.Errorf("%w: filter column is not declared", repository.ErrInvalidQuery)
			}
			parsed, err := dbcore.ParseQueryValue(dbcore.ColumnDefinition{Key: column.Key, Type: column.Type, VarcharSize: column.VarcharSize}, value)
			if err != nil {
				return repository.RowQuery{}, err
			}
			filters = append(filters, repository.RowFilter{Column: column, Value: parsed})
		}
	}
	var orderBy *repository.DatabaseColumnSchema
	if raw := query.Get("order_by"); raw != "" && raw != "id" {
		column, ok := byKey[raw]
		if !ok {
			return repository.RowQuery{}, fmt.Errorf("%w: order column is not declared", repository.ErrInvalidQuery)
		}
		if !column.Required {
			return repository.RowQuery{}, fmt.Errorf("%w: order_by must target a required column so cursor ordering is stable", repository.ErrInvalidQuery)
		}
		orderBy = &column
	}
	descending := false
	if raw := query.Get("order_direction"); raw != "" {
		switch strings.ToLower(raw) {
		case "asc":
		case "desc":
			descending = true
		default:
			return repository.RowQuery{}, fmt.Errorf("%w: order_direction must be asc or desc", repository.ErrInvalidQuery)
		}
	}
	searchRaw := query.Get("search")
	search := strings.TrimSpace(searchRaw)
	searchColumnKey := strings.TrimSpace(query.Get("search_column"))
	var searchColumn *repository.DatabaseColumnSchema
	if searchRaw != "" && search == "" {
		return repository.RowQuery{}, fmt.Errorf("%w: search must not be empty", repository.ErrInvalidQuery)
	}
	if search != "" && searchColumnKey == "" {
		return repository.RowQuery{}, fmt.Errorf("%w: search_column is required with search", repository.ErrInvalidQuery)
	}
	if searchColumnKey != "" {
		column, ok := byKey[searchColumnKey]
		if !ok {
			return repository.RowQuery{}, fmt.Errorf("%w: search column is not declared", repository.ErrInvalidQuery)
		}
		if column.Type != dbcore.TypeVarchar && column.Type != dbcore.TypeText {
			return repository.RowQuery{}, fmt.Errorf("%w: full-text search requires a varchar or text column", repository.ErrInvalidQuery)
		}
		if search == "" {
			return repository.RowQuery{}, fmt.Errorf("%w: search is required with search_column", repository.ErrInvalidQuery)
		}
		if len(search) > 256 {
			return repository.RowQuery{}, fmt.Errorf("%w: search must be at most 256 bytes", repository.ErrInvalidQuery)
		}
		searchColumn = &column
	}
	if cursor != nil && orderBy != nil {
		value, err := canonicalCursorValue(*orderBy, cursor.Value)
		if err != nil {
			return repository.RowQuery{}, err
		}
		cursor.Value = value
	}
	return repository.RowQuery{Limit: limit, Cursor: cursor, Filters: filters, OrderBy: orderBy, Descending: descending, Search: search, SearchColumn: searchColumn}, nil
}

func canonicalCursorValue(column repository.DatabaseColumnSchema, value any) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("%w: ordered cursor cannot contain a null value", repository.ErrInvalidQuery)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: cursor value is invalid", repository.ErrInvalidQuery)
	}
	var text string
	switch column.Type {
	case dbcore.TypeJSON:
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("%w: cursor value is invalid", repository.ErrInvalidQuery)
		}
		return decoded, nil
	case dbcore.TypeBoolean:
		text = string(raw)
	case dbcore.TypeInteger, dbcore.TypeDouble:
		text = string(raw)
	default:
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("%w: cursor value is invalid", repository.ErrInvalidQuery)
		}
	}
	value, err = dbcore.ParseQueryValue(dbcore.ColumnDefinition{Key: column.Key, Type: column.Type, VarcharSize: column.VarcharSize}, strings.Trim(text, `"`))
	if err != nil {
		return nil, fmt.Errorf("%w: cursor value is invalid", repository.ErrInvalidQuery)
	}
	return value, nil
}

func filterColumnKey(value string) (string, bool) {
	if strings.HasPrefix(value, "filter.") {
		key := strings.TrimPrefix(value, "filter.")
		return key, key != ""
	}
	if strings.HasPrefix(value, "filter_") {
		key := strings.TrimPrefix(value, "filter_")
		return key, key != ""
	}
	if strings.HasPrefix(value, "filter[") && strings.HasSuffix(value, "]") {
		key := strings.TrimSuffix(strings.TrimPrefix(value, "filter["), "]")
		return key, key != ""
	}
	return "", false
}

func writeDatabaseQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, repository.ErrUnindexedQuery) {
		writeError(w, 422, "unindexed_query", "filters and ordering require a real key index on the declared column")
		return
	}
	if errors.Is(err, repository.ErrInvalidQuery) || errors.Is(err, dbcore.ErrInvalidValue) {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	writeError(w, 422, "validation_error", "invalid database query")
}

func databaseResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, repository.ErrRowHidden):
		writeError(w, http.StatusNotFound, "not_found", "database resource was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to access this database resource")
		return true
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "database resource conflicts with an existing resource")
		return true
	case errors.Is(err, repository.ErrReferenceViolation):
		writeError(w, http.StatusConflict, "reference_violation", "database row is still referenced by another row")
		return true
	case errors.Is(err, repository.ErrSchemaConflict):
		writeError(w, http.StatusConflict, "schema_conflict", "schema change conflicts with existing rows or indexes")
		return true
	case errors.Is(err, repository.ErrUnindexedQuery):
		writeError(w, http.StatusUnprocessableEntity, "unindexed_query", "filters and ordering require a real key index on the declared column")
		return true
	case errors.Is(err, repository.ErrInvalidQuery):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return true
	case errors.Is(err, dbcore.ErrInvalidIdentifier), errors.Is(err, dbcore.ErrInvalidColumn), errors.Is(err, dbcore.ErrInvalidPermissions), errors.Is(err, dbcore.ErrDuplicatePermission), errors.Is(err, dbcore.ErrInvalidName), errors.Is(err, dbcore.ErrInvalidRow), errors.Is(err, dbcore.ErrMissingRequired), errors.Is(err, dbcore.ErrUnknownField), errors.Is(err, dbcore.ErrInvalidValue):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return true
	}
	return false
}
