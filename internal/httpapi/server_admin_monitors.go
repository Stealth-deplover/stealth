package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/monitoring"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type adminMonitorRequest struct {
	Name                  string            `json:"name"`
	Kind                  string            `json:"kind"`
	Target                string            `json:"target"`
	IntervalSeconds       int               `json:"interval_seconds"`
	TimeoutMS             int               `json:"timeout_ms"`
	Enabled               *bool             `json:"enabled"`
	Method                string            `json:"method,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	Body                  string            `json:"body,omitempty"`
	ExpectedStatus        int               `json:"expected_status,omitempty"`
	BodyContains          string            `json:"body_contains,omitempty"`
	LatencyThresholdMS    int               `json:"latency_threshold_ms,omitempty"`
	Host                  string            `json:"host,omitempty"`
	Port                  int               `json:"port,omitempty"`
	RecordType            string            `json:"record_type,omitempty"`
	ExpectedValues        []string          `json:"expected_values,omitempty"`
	GraceSeconds          int               `json:"grace_seconds,omitempty"`
	CertificateExpiryDays int               `json:"certificate_expiry_days,omitempty"`
}

type adminMonitorResponse struct {
	Monitor           domain.AdminMonitor        `json:"monitor"`
	Checks            []domain.AdminMonitorCheck `json:"checks,omitempty"`
	HeartbeatToken    string                     `json:"heartbeat_token,omitempty"`
	HeartbeatEndpoint string                     `json:"heartbeat_endpoint,omitempty"`
}

type adminMonitorsResponse struct {
	Items []domain.AdminMonitor `json:"items"`
}

func (s *Server) listAdminMonitors(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		internalError(s, w, errors.New("repository is unavailable"))
		return
	}
	limit, ok := s.adminLimit(w, r)
	if !ok {
		return
	}
	if limit > 100 {
		limit = 100
	}
	items, err := s.repo.ListAdminMonitors(r.Context(), limit)
	if err != nil {
		adminMonitorError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminMonitorsResponse{Items: items})
}

func (s *Server) createAdminMonitor(w http.ResponseWriter, r *http.Request) {
	var request adminMonitorRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		internalError(s, w, err)
		return
	}
	input, heartbeatToken, ok := s.adminMonitorInput(w, request)
	if !ok {
		return
	}
	item, err := s.repo.CreateAdminMonitor(r.Context(), mustUUID(accountFrom(r).ID), id, input)
	if err != nil {
		adminMonitorError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, adminMonitorResponse{
		Monitor: item, HeartbeatToken: heartbeatToken,
		HeartbeatEndpoint: heartbeatEndpoint(item.ID, heartbeatToken),
	})
}

func (s *Server) getAdminMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "monitorID")
	if !ok || s.repo == nil {
		return
	}
	item, err := s.repo.AdminMonitorByID(r.Context(), id)
	if err != nil {
		adminMonitorError(s, w, err)
		return
	}
	checks, err := s.repo.ListAdminMonitorChecks(r.Context(), id, 20)
	if err != nil {
		adminMonitorError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminMonitorResponse{Monitor: item, Checks: checks})
}

func (s *Server) updateAdminMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "monitorID")
	if !ok || s.repo == nil {
		return
	}
	var request adminMonitorRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	input, heartbeatToken, ok := s.adminMonitorInput(w, request)
	if !ok {
		return
	}
	item, err := s.repo.UpdateAdminMonitor(r.Context(), mustUUID(accountFrom(r).ID), id, input)
	if err != nil {
		adminMonitorError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminMonitorResponse{
		Monitor: item, HeartbeatToken: heartbeatToken,
		HeartbeatEndpoint: heartbeatEndpoint(item.ID, heartbeatToken),
	})
}

func (s *Server) deleteAdminMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "monitorID")
	if !ok || s.repo == nil {
		return
	}
	if err := s.repo.DeleteAdminMonitor(r.Context(), mustUUID(accountFrom(r).ID), id); err != nil {
		adminMonitorError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) recordAdminMonitorHeartbeat(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "monitoring is unavailable")
		return
	}
	id, ok := pathUUID(w, r, "monitorID")
	if !ok {
		return
	}
	decision, err := s.limiter.Allow(r.Context(), ratelimit.ProjectIPKey("monitor_heartbeat", id.String(), s.requestClientIP(r)), 60, time.Minute)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "heartbeat protection is temporarily unavailable")
		return
	}
	if !decision.Allowed {
		writeRateLimited(w, decision.RetryAfter)
		return
	}
	token := strings.TrimSpace(r.Header.Get("X-Stealth-Heartbeat"))
	if len(token) < 32 || len(token) > 256 || strings.ContainsAny(token, "\x00\r\n") {
		writeError(w, http.StatusUnauthorized, "unauthorized", "heartbeat token is invalid")
		return
	}
	hash := sha256.Sum256([]byte(token))
	if err := s.repo.RecordAdminHeartbeat(r.Context(), id, hash[:]); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "heartbeat token is invalid")
			return
		}
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminMonitorInput(w http.ResponseWriter, request adminMonitorRequest) (repository.AdminMonitorInput, string, bool) {
	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	interval := request.IntervalSeconds
	if interval == 0 {
		interval = 60
	}
	timeout := request.TimeoutMS
	if timeout == 0 {
		timeout = 5000
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" && kind == "http" {
		method = http.MethodGet
	}
	expectedStatus := request.ExpectedStatus
	if expectedStatus == 0 && kind == "http" {
		expectedStatus = http.StatusOK
	}
	config := map[string]any{
		"method": method, "headers": request.Headers, "body": request.Body,
		"expected_status": expectedStatus, "body_contains": request.BodyContains,
		"latency_threshold_ms": request.LatencyThresholdMS, "host": strings.TrimSpace(request.Host),
		"port": request.Port, "record_type": strings.ToUpper(strings.TrimSpace(request.RecordType)),
		"expected_values": request.ExpectedValues, "grace_seconds": request.GraceSeconds,
		"certificate_expiry_days": request.CertificateExpiryDays,
	}
	heartbeatToken := ""
	var heartbeatHash []byte
	if kind == "heartbeat" {
		var err error
		heartbeatToken, err = newHeartbeatToken()
		if err != nil {
			internalError(s, w, err)
			return repository.AdminMonitorInput{}, "", false
		}
		hash := sha256.Sum256([]byte(heartbeatToken))
		heartbeatHash = hash[:]
		config["token_hash"] = hex.EncodeToString(hash[:])
	}
	secretConfig, err := json.Marshal(config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "monitor configuration is invalid")
		return repository.AdminMonitorInput{}, "", false
	}
	if err := monitoring.ValidateConfig(kind, request.Target, secretConfig); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "monitor configuration is invalid")
		return repository.AdminMonitorInput{}, "", false
	}
	publicConfig := map[string]any{
		"method": method, "has_headers": len(request.Headers) > 0, "has_body": request.Body != "",
		"expected_status": expectedStatus, "body_assertion_enabled": request.BodyContains != "",
		"latency_threshold_ms": request.LatencyThresholdMS, "host": strings.TrimSpace(request.Host),
		"port": request.Port, "record_type": strings.ToUpper(strings.TrimSpace(request.RecordType)),
		"expected_values": request.ExpectedValues, "grace_seconds": request.GraceSeconds,
		"certificate_expiry_days": request.CertificateExpiryDays,
	}
	public, err := json.Marshal(publicConfig)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "monitor configuration is invalid")
		return repository.AdminMonitorInput{}, "", false
	}
	return repository.AdminMonitorInput{
		Name: request.Name, Kind: kind, Target: strings.TrimSpace(request.Target),
		IntervalSeconds: interval, TimeoutMS: timeout, Enabled: enabled, PublicConfig: public,
		SecretConfig: secretConfig, HeartbeatTokenHash: heartbeatHash,
	}, heartbeatToken, true
}

func newHeartbeatToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func heartbeatEndpoint(id, token string) string {
	if token == "" {
		return ""
	}
	return "/v1/monitor-heartbeats/" + id
}

func adminMonitorError(s *Server, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrInvalidAdminMonitor):
		writeError(w, http.StatusBadRequest, "validation_error", "monitor configuration is invalid")
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, repository.ErrNoAdminMonitor):
		writeError(w, http.StatusNotFound, "not_found", "monitor was not found")
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "instance owner or admin permission is required")
	default:
		internalError(s, w, err)
	}
}
