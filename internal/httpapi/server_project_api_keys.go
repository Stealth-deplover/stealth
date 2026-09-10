package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/google/uuid"
)

type projectAPIKeyRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt *string  `json:"expires_at"`
}

func (s *Server) listProjectAPIKeys(w http.ResponseWriter, r *http.Request) {
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
	items, next, canManage, err := s.repo.ListProjectAPIKeys(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), limit, cursorID)
	if projectAPIKeyResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req projectAPIKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := validate.Name(req.Name, "name")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	scopes, err := apikey.NormalizeProjectScopes(req.Scopes)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "scopes must contain supported users.read, users.write, databases.read, databases.write, storage.read, storage.write, functions.read, functions.write, sites.read, sites.write, webhooks.read, webhooks.write, realtime.read, messaging.read, or messaging.write values")
		return
	}
	expiresAt, err := parseAPIKeyExpiry(req.ExpiresAt, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	secret, prefix, secretHash, err := apikey.NewSecret()
	if err != nil {
		internalError(s, w, err)
		return
	}
	item, err := s.repo.CreateProjectAPIKey(r.Context(), uuid.Must(uuid.NewV7()), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), name, prefix, secretHash, scopes, expiresAt)
	if projectAPIKeyResourceError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "the API key could not be created")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": item, "secret": secret})
}

func (s *Server) getProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	keyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	item, err := s.repo.ProjectAPIKeyByID(r.Context(), projectID, keyID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	if projectAPIKeyResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.ProjectAPIKey{"key": item})
}

func (s *Server) revokeProjectAPIKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	keyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	err := s.repo.RevokeProjectAPIKey(r.Context(), projectID, keyID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	if projectAPIKeyResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseAPIKeyExpiry(raw *string, now time.Time) (*time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		if raw != nil {
			return nil, errors.New("expires_at must be a future RFC3339 timestamp when provided")
		}
		return nil, nil
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*raw))
	if err != nil {
		return nil, errors.New("expires_at must be a future RFC3339 timestamp")
	}
	expiresAt = expiresAt.UTC()
	if err := apikey.ValidateExpiry(&expiresAt, now); err != nil {
		return nil, errors.New("expires_at must be within 365 days")
	}
	return &expiresAt, nil
}
