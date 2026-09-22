package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/repository"
)

type adminCloudflareConnectionRequest struct {
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id,omitempty"`
	TunnelID  string `json:"tunnel_id,omitempty"`
}

func (s *Server) getAdminCloudflare(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		internalError(s, w, errors.New("repository is unavailable"))
		return
	}
	status, err := s.repo.CloudflareRoutingStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) updateAdminCloudflare(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		internalError(s, w, errors.New("repository is unavailable"))
		return
	}
	var request adminCloudflareConnectionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	token := strings.TrimSpace(request.APIToken)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "api_token is invalid")
		return
	}
	if s.cloudflareFactory == nil {
		writeError(w, http.StatusServiceUnavailable, "cloudflare_unavailable", "Cloudflare token validation is unavailable")
		return
	}
	instanceHostname, err := s.instanceHostname()
	if err != nil {
		internalError(s, w, err)
		return
	}
	settings, err := s.repo.GetInstanceDomainSettings(r.Context(), instanceHostname)
	if err != nil {
		if instanceDomainSettingsError(s, w, err) {
			return
		}
		internalError(s, w, err)
		return
	}
	current, err := s.repo.CloudflareConnectionDetails(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if current.ConsoleHostname != "" && current.ConsoleHostname != instanceHostname {
		writeError(w, http.StatusConflict, "cloudflare_tunnel_identity_conflict", "PUBLIC_APP_URL does not match the Console hostname saved with the existing Cloudflare tunnel")
		return
	}
	accountID, tunnelID := strings.TrimSpace(current.AccountID), strings.TrimSpace(current.TunnelID)
	if accountID == "" || tunnelID == "" {
		accountID, tunnelID = strings.TrimSpace(request.AccountID), strings.TrimSpace(request.TunnelID)
		if accountID == "" || tunnelID == "" {
			writeError(w, http.StatusUnprocessableEntity, "cloudflare_tunnel_identity_required", "This instance has no recoverable Cloudflare tunnel identity. Provide the existing Cloudflare account_id and tunnel_id; Stealth will validate and reuse that tunnel without creating another.")
			return
		}
	} else if (request.AccountID != "" && strings.TrimSpace(request.AccountID) != accountID) || (request.TunnelID != "" && strings.TrimSpace(request.TunnelID) != tunnelID) {
		writeError(w, http.StatusConflict, "cloudflare_tunnel_identity_conflict", "reconnection must use the existing Cloudflare account and named tunnel")
		return
	}
	client, err := s.cloudflareFactory(token)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cloudflare_token_invalid", "Cloudflare API token is invalid")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	validated, err := cloudflare.ValidateExistingTunnel(ctx, client, accountID, current.ConsoleZoneID, instanceHostname, tunnelID, current.TunnelName, settings.WorkloadBaseDomain)
	if err != nil {
		if errors.Is(err, cloudflare.ErrUnauthorized) {
			writeError(w, http.StatusUnprocessableEntity, "cloudflare_token_rejected", "Cloudflare rejected the token or it lacks Account Tunnel Edit, Account Settings Read, Zone Read, or DNS Edit access for the required zones")
		} else {
			writeError(w, http.StatusBadGateway, "cloudflare_validation_failed", "Cloudflare could not verify the existing account, tunnel, Console DNS, and configured workload zone. Check the scoped token and keep the existing tunnel unchanged.")
		}
		return
	}
	if err := s.repo.SetCloudflareConnectionByOwner(r.Context(), mustUUID(accountFrom(r).ID), repository.CloudflareConnectionInput{
		AccountID: validated.AccountID, ConsoleZoneID: validated.ConsoleZoneID, ConsoleHostname: validated.ConsoleHostname,
		TunnelID: validated.TunnelID, TunnelName: validated.TunnelName, ConsoleRecordID: validated.ConsoleRecordID, APIToken: token,
	}); err != nil {
		switch {
		case errors.Is(err, repository.ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden", "instance owner permission is required")
		case errors.Is(err, repository.ErrCloudflareConnectionConflict):
			writeError(w, http.StatusConflict, "cloudflare_tunnel_identity_conflict", "the existing Cloudflare tunnel identity cannot be replaced")
		case errors.Is(err, repository.ErrCloudflareConnectionUnavailable):
			writeError(w, http.StatusServiceUnavailable, "cloudflare_unavailable", "Cloudflare credential encryption is unavailable")
		default:
			internalError(s, w, err)
		}
		return
	}
	status, err := s.repo.CloudflareRoutingStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
