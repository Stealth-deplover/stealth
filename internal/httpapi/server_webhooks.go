package httpapi

import (
	"errors"
	"net/http"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func webhookActorFrom(r *http.Request) repository.WebhookActor {
	actor, ok := r.Context().Value(projectActorContextKey).(projectActor)
	if !ok {
		return repository.WebhookActor{}
	}
	if actor.kind == apiKeyProjectActor {
		return repository.WebhookActor{Kind: repository.WebhookAPIKeyActor, APIKeyID: actor.apiKeyID, APIKeyScopes: actor.scopes}
	}
	account, ok := r.Context().Value(accountContextKey).(domain.Account)
	if !ok {
		return repository.WebhookActor{}
	}
	return repository.WebhookActor{Kind: repository.WebhookConsoleActor, AccountID: mustUUID(account.ID)}
}

type webhookRequest struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled"`
}

type webhookPatchRequest struct {
	Name    *string   `json:"name"`
	URL     *string   `json:"url"`
	Events  *[]string `json:"events"`
	Enabled *bool     `json:"enabled"`
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
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
	items, next, canManage, err := s.repo.ListWebhooks(r.Context(), projectID, webhookActorFrom(r), limit, cursorID)
	if webhookResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req webhookRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	item, secret, err := s.repo.CreateWebhook(r.Context(), uuid.Must(uuid.NewV7()), projectID, webhookActorFrom(r), repository.WebhookInput{Name: req.Name, URL: req.URL, Events: req.Events, Enabled: enabled})
	if webhookResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"webhook": item, "secret": secret})
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request) {
	projectID, webhookID, ok := webhookPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetWebhook(r.Context(), projectID, webhookID, webhookActorFrom(r))
	if webhookResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Webhook{"webhook": item})
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	projectID, webhookID, ok := webhookPathIDs(w, r)
	if !ok {
		return
	}
	var req webhookPatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.URL == nil && req.Events == nil && req.Enabled == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "at least one webhook field must be provided")
		return
	}
	item, err := s.repo.UpdateWebhook(r.Context(), projectID, webhookID, webhookActorFrom(r), repository.WebhookPatch{Name: req.Name, URL: req.URL, Events: req.Events, Enabled: req.Enabled})
	if webhookResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Webhook{"webhook": item})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	projectID, webhookID, ok := webhookPathIDs(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeleteWebhook(r.Context(), projectID, webhookID, webhookActorFrom(r)); webhookResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	projectID, webhookID, ok := webhookPathIDs(w, r)
	if !ok {
		return
	}
	item, secret, err := s.repo.RotateWebhookSecret(r.Context(), projectID, webhookID, webhookActorFrom(r))
	if webhookResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhook": item, "secret": secret})
}

func (s *Server) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	projectID, webhookID, ok := webhookPathIDs(w, r)
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
	items, next, err := s.repo.ListWebhookDeliveries(r.Context(), projectID, webhookID, webhookActorFrom(r), limit, cursorID)
	if webhookResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": items, "pagination": paginationOf(limit, next)})
}

func webhookPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	webhookID, ok := pathUUID(w, r, "webhookID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, webhookID, true
}

func webhookResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, repository.ErrWebhookDeliveryNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project, webhook, or delivery was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage project webhooks")
		return true
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "a webhook with this name already exists")
		return true
	case errors.Is(err, repository.ErrInvalidWebhook):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "webhook settings are invalid")
		return true
	case errors.Is(err, repository.ErrWebhookPayloadTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "the webhook event payload is too large")
		return true
	case errors.Is(err, repository.ErrWebhookNotReady), errors.Is(err, repository.ErrWebhookSecretUnavailable):
		writeError(w, http.StatusServiceUnavailable, "not_ready", "webhook signing is not ready")
		return true
	default:
		return false
	}
}
