package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type adminAlertRuleRequest struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Condition  map[string]any `json:"condition"`
	Severity   string         `json:"severity"`
	ForSeconds int            `json:"for_seconds"`
	Enabled    *bool          `json:"enabled"`
}

type adminAlertRuleResponse struct {
	Rule   domain.AdminAlertRule    `json:"rule"`
	Events []domain.AdminAlertEvent `json:"events,omitempty"`
}

type adminAlertRulesResponse struct {
	Items []domain.AdminAlertRule `json:"items"`
}

type adminAlertEventsResponse struct {
	Items      []domain.AdminAlertEvent `json:"items"`
	NextCursor *string                  `json:"next_cursor,omitempty"`
}

type adminNotificationChannelRequest struct {
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`
	Enabled *bool          `json:"enabled"`
	Config  map[string]any `json:"config"`
}

type adminNotificationChannelsResponse struct {
	Items []domain.AdminNotificationChannel `json:"items"`
}

type adminNotificationTestResponse struct {
	DeliveryID string `json:"delivery_id"`
	Status     string `json:"status"`
}

type adminIncidentRequest struct {
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Status   string   `json:"status"`
	Services []string `json:"services"`
	Message  string   `json:"message"`
}

type adminIncidentPatchRequest struct {
	Title    *string   `json:"title"`
	Severity *string   `json:"severity"`
	Status   *string   `json:"status"`
	Services *[]string `json:"services"`
	Message  *string   `json:"message"`
}

type adminIncidentEventRequest struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type adminIncidentsResponse struct {
	Items []domain.AdminIncident `json:"items"`
}

type adminIncidentResponse struct {
	Incident domain.AdminIncident `json:"incident"`
}

type adminDashboardRequest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Definition  map[string]any `json:"definition"`
}

type adminDashboardsResponse struct {
	Items []domain.AdminDashboard `json:"items"`
}

type adminDashboardResponse struct {
	Dashboard domain.AdminDashboard `json:"dashboard"`
}

type adminStatusPageRequest struct {
	Name               string           `json:"name"`
	Description        string           `json:"description"`
	IsPublic           bool             `json:"is_public"`
	Components         []map[string]any `json:"components"`
	PublishedIncidents []string         `json:"published_incidents"`
}

func (s *Server) listAdminAlertRules(w http.ResponseWriter, r *http.Request) {
	limit, ok := adminConfigLimit(w, r)
	if !ok {
		return
	}
	items, err := s.repo.ListAdminAlertRules(r.Context(), limit)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminAlertRulesResponse{Items: items})
}

func (s *Server) listAdminAlertEvents(w http.ResponseWriter, r *http.Request) {
	query, ok := adminAlertEventQuery(w, r, nil)
	if !ok {
		return
	}
	page, err := s.repo.QueryAdminAlertEvents(r.Context(), query)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeAdminAlertEventPage(w, page)
}

func (s *Server) listAdminNotificationChannels(w http.ResponseWriter, r *http.Request) {
	limit, ok := adminConfigLimit(w, r)
	if !ok {
		return
	}
	items, err := s.repo.ListAdminNotificationChannels(r.Context(), limit)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminNotificationChannelsResponse{Items: items})
}

func (s *Server) createAdminNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var request adminNotificationChannelRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	config, err := json.Marshal(request.Config)
	if err != nil {
		adminControlError(s, w, repository.ErrInvalidAdminNotification)
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	id, err := uuid.NewV7()
	if err != nil {
		internalError(s, w, err)
		return
	}
	item, err := s.repo.CreateAdminNotificationChannel(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminNotificationChannelInput{Name: request.Name, Kind: request.Kind, Enabled: enabled, Config: config})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getAdminNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	item, err := s.repo.AdminNotificationChannelByID(r.Context(), id)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateAdminNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	var request adminNotificationChannelRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	config, err := json.Marshal(request.Config)
	if err != nil {
		adminControlError(s, w, repository.ErrInvalidAdminNotification)
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	item, err := s.repo.UpdateAdminNotificationChannel(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminNotificationChannelInput{Name: request.Name, Kind: request.Kind, Enabled: enabled, Config: config})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteAdminNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	if err := s.repo.DeleteAdminNotificationChannel(r.Context(), mustUUID(accountFrom(r).ID), id); err != nil {
		adminControlError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testAdminNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok || s.repo == nil {
		return
	}
	deliveryID, err := s.repo.EnqueueAdminNotificationTest(r.Context(), mustUUID(accountFrom(r).ID), id)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, adminNotificationTestResponse{DeliveryID: deliveryID.String(), Status: "pending"})
}

func (s *Server) createAdminAlertRule(w http.ResponseWriter, r *http.Request) {
	var request adminAlertRuleRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	condition, err := json.Marshal(request.Condition)
	if err != nil {
		adminControlError(s, w, repository.ErrInvalidAdminAlert)
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	id, err := uuid.NewV7()
	if err != nil {
		internalError(s, w, err)
		return
	}
	item, err := s.repo.CreateAdminAlertRule(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminAlertRuleInput{
		Name: request.Name, Kind: request.Kind, Condition: condition, Severity: request.Severity,
		ForSeconds: request.ForSeconds, Enabled: enabled,
	})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, adminAlertRuleResponse{Rule: item})
}

func (s *Server) getAdminAlertRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "alertRuleID")
	if !ok {
		return
	}
	item, err := s.repo.AdminAlertRuleByID(r.Context(), id)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	events, err := s.repo.ListAdminAlertEvents(r.Context(), id, 50)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminAlertRuleResponse{Rule: item, Events: events})
}

func (s *Server) listAdminAlertRuleEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "alertRuleID")
	if !ok {
		return
	}
	query, ok := adminAlertEventQuery(w, r, &id)
	if !ok {
		return
	}
	page, err := s.repo.QueryAdminAlertEvents(r.Context(), query)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeAdminAlertEventPage(w, page)
}

func adminAlertEventQuery(w http.ResponseWriter, r *http.Request, ruleID *uuid.UUID) (repository.AdminAlertEventQuery, bool) {
	limit, ok := adminConfigLimit(w, r)
	if !ok {
		return repository.AdminAlertEventQuery{}, false
	}
	from, ok := adminAlertEventTime(w, r, "from")
	if !ok {
		return repository.AdminAlertEventQuery{}, false
	}
	to, ok := adminAlertEventTime(w, r, "to")
	if !ok {
		return repository.AdminAlertEventQuery{}, false
	}
	if from != nil && to != nil && !to.After(*from) {
		writeError(w, http.StatusBadRequest, "validation_error", "from must be before to")
		return repository.AdminAlertEventQuery{}, false
	}
	var cursor *repository.AdminAlertEventCursor
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		parsed, err := repository.DecodeAdminAlertEventCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "cursor is invalid")
			return repository.AdminAlertEventQuery{}, false
		}
		cursor = &parsed
	}
	return repository.AdminAlertEventQuery{RuleID: ruleID, From: from, To: to, Cursor: cursor, Limit: limit}, true
}

func adminAlertEventTime(w http.ResponseWriter, r *http.Request, key string) (*time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", key+" must be an RFC3339 timestamp")
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

func writeAdminAlertEventPage(w http.ResponseWriter, page repository.AdminAlertEventPage) {
	response := adminAlertEventsResponse{Items: page.Items}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) updateAdminAlertRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "alertRuleID")
	if !ok {
		return
	}
	var request adminAlertRuleRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	condition, err := json.Marshal(request.Condition)
	if err != nil {
		adminControlError(s, w, repository.ErrInvalidAdminAlert)
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	item, err := s.repo.UpdateAdminAlertRule(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminAlertRulePatch{
		Name: &request.Name, Kind: &request.Kind, Condition: condition, Severity: &request.Severity,
		ForSeconds: &request.ForSeconds, Enabled: &enabled,
	})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminAlertRuleResponse{Rule: item})
}

func (s *Server) deleteAdminAlertRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "alertRuleID")
	if !ok {
		return
	}
	if err := s.repo.DeleteAdminAlertRule(r.Context(), mustUUID(accountFrom(r).ID), id); err != nil {
		adminControlError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAdminIncidents(w http.ResponseWriter, r *http.Request) {
	limit, ok := adminConfigLimit(w, r)
	if !ok {
		return
	}
	items, err := s.repo.ListAdminIncidents(r.Context(), limit)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminIncidentsResponse{Items: items})
}

func (s *Server) createAdminIncident(w http.ResponseWriter, r *http.Request) {
	var request adminIncidentRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		internalError(s, w, err)
		return
	}
	item, err := s.repo.CreateAdminIncident(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminIncidentInput{
		Title: request.Title, Severity: request.Severity, Status: request.Status, Services: request.Services, Message: request.Message,
	})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, adminIncidentResponse{Incident: item})
}

func (s *Server) getAdminIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "incidentID")
	if !ok {
		return
	}
	item, err := s.repo.AdminIncidentByID(r.Context(), id)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminIncidentResponse{Incident: item})
}

func (s *Server) updateAdminIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "incidentID")
	if !ok {
		return
	}
	var request adminIncidentPatchRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	item, err := s.repo.UpdateAdminIncident(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminIncidentPatch{
		Title: request.Title, Severity: request.Severity, Status: request.Status, Services: request.Services, Message: request.Message,
	})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminIncidentResponse{Incident: item})
}

func (s *Server) addAdminIncidentEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "incidentID")
	if !ok {
		return
	}
	var request adminIncidentEventRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		internalError(s, w, err)
		return
	}
	event, err := s.repo.AddAdminIncidentEvent(r.Context(), mustUUID(accountFrom(r).ID), id, eventID, repository.AdminIncidentEventInput{Kind: request.Kind, Message: request.Message})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, event)
}

func (s *Server) listAdminDashboards(w http.ResponseWriter, r *http.Request) {
	limit, ok := adminConfigLimit(w, r)
	if !ok {
		return
	}
	items, err := s.repo.ListAdminDashboards(r.Context(), limit)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminDashboardsResponse{Items: items})
}

func (s *Server) createAdminDashboard(w http.ResponseWriter, r *http.Request) {
	var request adminDashboardRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		internalError(s, w, err)
		return
	}
	definition, err := json.Marshal(request.Definition)
	if err != nil {
		adminControlError(s, w, repository.ErrInvalidAdminDashboard)
		return
	}
	item, err := s.repo.CreateAdminDashboard(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminDashboardInput{Name: request.Name, Description: request.Description, Definition: definition})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, adminDashboardResponse{Dashboard: item})
}

func (s *Server) getAdminDashboard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "dashboardID")
	if !ok {
		return
	}
	item, err := s.repo.AdminDashboardByID(r.Context(), id)
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminDashboardResponse{Dashboard: item})
}

func (s *Server) updateAdminDashboard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "dashboardID")
	if !ok {
		return
	}
	var request adminDashboardRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	definition, err := json.Marshal(request.Definition)
	if err != nil {
		adminControlError(s, w, repository.ErrInvalidAdminDashboard)
		return
	}
	item, err := s.repo.UpdateAdminDashboard(r.Context(), mustUUID(accountFrom(r).ID), id, repository.AdminDashboardInput{Name: request.Name, Description: request.Description, Definition: definition})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminDashboardResponse{Dashboard: item})
}

func (s *Server) deleteAdminDashboard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "dashboardID")
	if !ok {
		return
	}
	if err := s.repo.DeleteAdminDashboard(r.Context(), mustUUID(accountFrom(r).ID), id); err != nil {
		adminControlError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getAdminStatusPage(w http.ResponseWriter, r *http.Request) {
	item, err := s.repo.AdminStatusPage(r.Context())
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateAdminStatusPage(w http.ResponseWriter, r *http.Request) {
	var request adminStatusPageRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	components, err := json.Marshal(request.Components)
	if err != nil {
		adminControlError(s, w, repository.ErrInvalidAdminStatus)
		return
	}
	item, err := s.repo.UpdateAdminStatusPage(r.Context(), mustUUID(accountFrom(r).ID), repository.AdminStatusPageInput{
		Name: request.Name, Description: request.Description, IsPublic: request.IsPublic, Components: components, PublishedIncidents: request.PublishedIncidents,
	})
	if err != nil {
		adminControlError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) publicAdminStatusPage(w http.ResponseWriter, r *http.Request) {
	item, err := s.repo.PublicAdminStatusPage(r.Context())
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "status page is not published")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30, stale-while-revalidate=60")
	writeJSON(w, http.StatusOK, item)
}

func adminConfigLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be an integer between 1 and 100")
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func adminControlError(s *Server, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrAdminAlertRuleConflict):
		writeError(w, http.StatusConflict, "admin_alert_rule_conflict", "the alert rule changed concurrently")
	case errors.Is(err, repository.ErrInvalidAdminAlert), errors.Is(err, repository.ErrInvalidAdminAlertHistory), errors.Is(err, repository.ErrInvalidAdminNotification), errors.Is(err, repository.ErrInvalidAdminIncident), errors.Is(err, repository.ErrInvalidAdminDashboard), errors.Is(err, repository.ErrInvalidAdminStatus):
		writeError(w, http.StatusBadRequest, "validation_error", "admin configuration is invalid")
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "admin resource was not found")
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "instance owner or admin permission is required")
	default:
		internalError(s, w, err)
	}
}
