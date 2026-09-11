package httpapi

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/google/uuid"
)

type bootstrapStatusResponse struct {
	SetupRequired bool `json:"setup_required"`
}

type bootstrapSessionResponse struct {
	SetupCode string    `json:"setup_code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type verifyBootstrapCodeRequest struct {
	SetupCode string `json:"setup_code"`
}

type verifyBootstrapCodeResponse struct {
	AuthorizationSessionID string    `json:"authorization_session_id"`
	ExpiresAt              time.Time `json:"expires_at"`
}

type startGitHubDeviceRequest struct {
	AuthorizationSessionID string `json:"authorization_session_id"`
	SetupCode              string `json:"setup_code"`
}

type githubDeviceResponse struct {
	AuthorizationSessionID string    `json:"authorization_session_id"`
	UserCode               string    `json:"user_code"`
	VerificationURI        string    `json:"verification_uri"`
	ExpiresAt              time.Time `json:"expires_at"`
	IntervalSeconds        int       `json:"interval_seconds"`
}

type pollGitHubDeviceRequest struct {
	AuthorizationSessionID string `json:"authorization_session_id"`
}

type pollGitHubDeviceResponse struct {
	Status            string          `json:"status"`
	RetryAfterSeconds int             `json:"retry_after_seconds,omitempty"`
	Account           *domain.Account `json:"account,omitempty"`
}

type adoptInstanceOwnerRequest struct {
	AccountID string `json:"account_id"`
}

const (
	// A normal browser polls at GitHub's interval (normally five seconds). The
	// generous API budget protects the database-facing endpoint from abuse
	// without making a 15-minute setup session hit the normal 10/min auth cap.
	bootstrapPollRateLimit  = 120
	bootstrapPollRateWindow = time.Minute
)

func (s *Server) bootstrapStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.repo.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeBootstrapJSON(w, http.StatusOK, bootstrapStatusResponse{SetupRequired: status.SetupRequired})
}

func (s *Server) createBootstrapSession(w http.ResponseWriter, r *http.Request) {
	if !s.bootstrapConfigured() {
		writeBootstrapError(w, http.StatusServiceUnavailable, "github_not_configured", "GitHub first-owner authentication is not configured")
		return
	}
	if !s.verifyBootstrapCLI(w, r) {
		return
	}
	code, err := bootstrap.GenerateCode()
	if err != nil {
		internalError(s, w, err)
		return
	}
	expiresAt := time.Now().UTC().Add(bootstrap.CodeLifetime)
	if err := s.repo.CreateBootstrapSession(r.Context(), repository.BootstrapSessionInput{
		ID:        uuid.Must(uuid.NewV7()),
		CodeHash:  bootstrap.HashCode(code),
		ExpiresAt: expiresAt,
	}); err != nil {
		switch {
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
		case errors.Is(err, repository.ErrInvalidBootstrapCode):
			writeBootstrapError(w, http.StatusUnprocessableEntity, "invalid_bootstrap_session", "unable to create a setup session")
		default:
			internalError(s, w, err)
		}
		return
	}
	writeBootstrapJSON(w, http.StatusCreated, bootstrapSessionResponse{SetupCode: code, ExpiresAt: expiresAt})
}

func (s *Server) verifyBootstrapCode(w http.ResponseWriter, r *http.Request) {
	var req verifyBootstrapCodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowBootstrapAttempt(w, r, "verify") {
		return
	}
	if !bootstrap.ValidCode(req.SetupCode) {
		writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "invalid, expired, or already used setup code")
		return
	}
	verification, err := s.repo.VerifyBootstrapCode(r.Context(), bootstrap.HashCode(req.SetupCode))
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
		case errors.Is(err, repository.ErrInvalidBootstrapCode):
			writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "invalid, expired, or already used setup code")
		default:
			internalError(s, w, err)
		}
		return
	}
	writeBootstrapJSON(w, http.StatusOK, verifyBootstrapCodeResponse{AuthorizationSessionID: verification.ID.String(), ExpiresAt: verification.ExpiresAt})
}

func (s *Server) startGitHubDeviceFlow(w http.ResponseWriter, r *http.Request) {
	var req startGitHubDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowBootstrapAttempt(w, r, strings.TrimSpace(req.AuthorizationSessionID)) {
		return
	}
	if !s.bootstrapConfigured() {
		writeBootstrapError(w, http.StatusServiceUnavailable, "github_not_configured", "GitHub first-owner authentication is not configured")
		return
	}
	sessionID, err := uuid.Parse(strings.TrimSpace(req.AuthorizationSessionID))
	if err != nil || !bootstrap.ValidCode(req.SetupCode) {
		writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "invalid, expired, or already used setup code")
		return
	}
	codeHash := bootstrap.HashCode(req.SetupCode)

	// Serialize starts for one API process. The database still enforces the
	// actual owner invariant, while this keeps repeated button clicks from
	// racing to replace the one persisted device flow.
	s.githubFlowMu.Lock()
	defer s.githubFlowMu.Unlock()
	verification, err := s.repo.VerifyBootstrapCode(r.Context(), codeHash)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
		case errors.Is(err, repository.ErrInvalidBootstrapCode):
			writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "invalid, expired, or already used setup code")
		default:
			internalError(s, w, err)
		}
		return
	}
	if verification.ID != sessionID {
		writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_session", "setup authorization session is invalid")
		return
	}
	device, err := s.githubClient.RequestDeviceCode(r.Context(), s.config.GitHubAppClientID)
	if err != nil {
		s.logger.Warn("GitHub device authorization request failed", "error", err)
		writeBootstrapError(w, http.StatusBadGateway, "github_unavailable", "GitHub authorization is temporarily unavailable")
		return
	}
	deviceCode := strings.TrimSpace(device.DeviceCode)
	userCode := strings.TrimSpace(device.UserCode)
	verificationURI := strings.TrimSpace(device.VerificationURI)
	if device.ExpiresIn <= 0 || device.ExpiresIn > bootstrap.CodeLifetime || device.PollingInterval < time.Second || device.PollingInterval > 5*time.Minute || deviceCode == "" || len(deviceCode) > 2048 || userCode == "" || len(userCode) > 64 || strings.ContainsAny(deviceCode+userCode, "\x00\r\n") || verificationURI != githubauth.DeviceVerificationURI {
		writeBootstrapError(w, http.StatusBadGateway, "github_invalid_response", "GitHub returned an invalid device authorization")
		return
	}
	sealedDeviceCode, err := bootstrap.SealDeviceCode(s.bootstrapCLIKey(), deviceCode)
	if err != nil {
		internalError(s, w, err)
		return
	}
	now := time.Now().UTC()
	githubExpiresAt := now.Add(device.ExpiresIn)
	if githubExpiresAt.After(verification.ExpiresAt) {
		githubExpiresAt = verification.ExpiresAt
	}
	flow, err := s.repo.StartGitHubDeviceFlow(r.Context(), repository.GitHubDeviceFlowInput{
		ID:                   sessionID,
		CodeHash:             codeHash,
		DeviceCodeCiphertext: sealedDeviceCode,
		UserCode:             userCode,
		VerificationURI:      verificationURI,
		ExpiresAt:            verification.ExpiresAt,
		GitHubExpiresAt:      githubExpiresAt,
		Interval:             device.PollingInterval,
	})
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
		case errors.Is(err, repository.ErrInvalidBootstrapCode):
			writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "invalid, expired, or already used setup code")
		default:
			internalError(s, w, err)
		}
		return
	}
	writeBootstrapJSON(w, http.StatusCreated, githubDeviceResponse{
		AuthorizationSessionID: flow.ID.String(),
		UserCode:               flow.UserCode,
		VerificationURI:        flow.VerificationURI,
		ExpiresAt:              flow.GitHubExpiresAt,
		IntervalSeconds:        maxInt(1, int(flow.Interval/time.Second)),
	})
}

func (s *Server) pollGitHubDeviceFlow(w http.ResponseWriter, r *http.Request) {
	var req pollGitHubDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowBootstrapPoll(w, r) {
		return
	}
	sessionID, err := uuid.Parse(strings.TrimSpace(req.AuthorizationSessionID))
	if err != nil {
		writeBootstrapError(w, http.StatusUnauthorized, "invalid_github_session", "GitHub authorization session is invalid")
		return
	}
	flow, allowed, err := s.repo.ClaimGitHubDevicePoll(r.Context(), sessionID)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
		case errors.Is(err, repository.ErrInvalidBootstrapCode):
			writeBootstrapError(w, http.StatusUnauthorized, "invalid_github_session", "GitHub authorization session is invalid")
		default:
			internalError(s, w, err)
		}
		return
	}
	if flow.Status == "expired" {
		writeBootstrapError(w, http.StatusGone, "github_authorization_expired", "GitHub authorization expired; start again")
		return
	}
	if flow.Status == "denied" {
		writeBootstrapError(w, http.StatusForbidden, "github_access_denied", "GitHub authorization was cancelled")
		return
	}
	if flow.Status == "failed" {
		writeBootstrapError(w, http.StatusConflict, "github_authorization_failed", "GitHub authorization could not be completed; try again")
		return
	}
	if flow.Status != "pending" || len(flow.DeviceCodeCiphertext) == 0 || flow.GitHubExpiresAt.IsZero() {
		writeBootstrapError(w, http.StatusUnauthorized, "invalid_github_session", "GitHub authorization session is invalid")
		return
	}
	if !allowed {
		writeBootstrapJSON(w, http.StatusOK, pollGitHubDeviceResponse{Status: "pending", RetryAfterSeconds: retryAfterSeconds(flow.NextPollAt)})
		return
	}
	deviceCode, err := bootstrap.OpenDeviceCode(s.bootstrapCLIKey(), flow.DeviceCodeCiphertext)
	if err != nil {
		if !s.updateGitHubDeviceFlow(w, r, sessionID, "failed", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
			return
		}
		internalError(s, w, err)
		return
	}
	result, err := s.githubClient.PollAccessToken(r.Context(), s.config.GitHubAppClientID, deviceCode)
	if err != nil {
		if !s.updateGitHubDeviceFlow(w, r, sessionID, "pending", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
			return
		}
		s.logger.Warn("GitHub device authorization poll failed", "error", err)
		writeBootstrapError(w, http.StatusBadGateway, "github_unavailable", "GitHub authorization is temporarily unavailable")
		return
	}
	switch result.Status {
	case githubauth.PollPending:
		if !s.updateGitHubDeviceFlow(w, r, sessionID, "pending", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
			return
		}
		writeBootstrapJSON(w, http.StatusOK, pollGitHubDeviceResponse{Status: "pending", RetryAfterSeconds: maxInt(1, int(flow.Interval/time.Second))})
		return
	case githubauth.PollSlowDown:
		interval := flow.Interval + 5*time.Second
		if interval > 5*time.Minute {
			interval = 5 * time.Minute
		}
		if !s.updateGitHubDeviceFlow(w, r, sessionID, "pending", interval, time.Now().UTC().Add(interval)) {
			return
		}
		writeBootstrapJSON(w, http.StatusOK, pollGitHubDeviceResponse{Status: "pending", RetryAfterSeconds: maxInt(1, int(interval/time.Second))})
		return
	case githubauth.PollExpired:
		if !s.updateGitHubDeviceFlow(w, r, sessionID, "expired", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
			return
		}
		writeBootstrapError(w, http.StatusGone, "github_authorization_expired", "GitHub authorization expired; start again")
		return
	case githubauth.PollDenied:
		if !s.updateGitHubDeviceFlow(w, r, sessionID, "denied", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
			return
		}
		writeBootstrapError(w, http.StatusForbidden, "github_access_denied", "GitHub authorization was cancelled")
		return
	case githubauth.PollAuthorized:
		// The access token is used only for this server-side identity lookup and
		// is never persisted, returned, or logged.
		user, userErr := s.githubClient.GetUser(r.Context(), result.AccessToken)
		if userErr != nil {
			if !s.updateGitHubDeviceFlow(w, r, sessionID, "failed", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
				return
			}
			s.logger.Warn("GitHub identity lookup failed", "error", userErr)
			writeBootstrapError(w, http.StatusBadGateway, "github_identity_unavailable", "GitHub identity could not be verified")
			return
		}
		input, identityErr := githubOwnerInput(user, flow)
		if identityErr != nil {
			if !s.updateGitHubDeviceFlow(w, r, sessionID, "failed", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
				return
			}
			writeBootstrapError(w, http.StatusBadGateway, "github_invalid_identity", "GitHub returned an invalid identity")
			return
		}
		token, tokenHash, tokenErr := auth.NewSessionToken()
		if tokenErr != nil {
			internalError(s, w, tokenErr)
			return
		}
		input.TokenHash = tokenHash
		input.SessionExpiresAt = time.Now().UTC().Add(s.config.SessionTTL)
		account, ownerErr := s.repo.CreateGitHubInstanceOwner(r.Context(), input)
		if ownerErr != nil {
			switch {
			case errors.Is(ownerErr, repository.ErrBootstrapSealed):
				writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
			case errors.Is(ownerErr, repository.ErrInvalidBootstrapCode):
				writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "setup authorization is invalid")
			case errors.Is(ownerErr, repository.ErrConflict):
				if !s.updateGitHubDeviceFlow(w, r, sessionID, "failed", flow.Interval, time.Now().UTC().Add(flow.Interval)) {
					return
				}
				writeBootstrapError(w, http.StatusConflict, "github_identity_conflict", "that GitHub identity cannot be used for this installation")
			default:
				internalError(s, w, ownerErr)
			}
			return
		}
		s.setSessionCookie(w, token)
		writeBootstrapJSON(w, http.StatusCreated, pollGitHubDeviceResponse{Status: "complete", Account: &account})
		return
	}
	writeBootstrapError(w, http.StatusBadGateway, "github_invalid_response", "GitHub returned an invalid authorization response")
}

func (s *Server) listBootstrapAdoptionAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.verifyBootstrapCLI(w, r) {
		return
	}
	accounts, err := s.repo.ListBootstrapAdoptionAccounts(r.Context())
	if err != nil {
		if errors.Is(err, repository.ErrBootstrapSealed) {
			writeBootstrapError(w, http.StatusConflict, "adoption_unavailable", "instance owner adoption is not available")
			return
		}
		internalError(s, w, err)
		return
	}
	writeBootstrapJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Server) adoptBootstrapOwner(w http.ResponseWriter, r *http.Request) {
	if !s.verifyBootstrapCLI(w, r) {
		return
	}
	var req adoptInstanceOwnerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	accountID, err := uuid.Parse(strings.TrimSpace(req.AccountID))
	if err != nil {
		writeBootstrapError(w, http.StatusUnprocessableEntity, "validation_error", "account_id must be a UUID")
		return
	}
	if err := s.repo.AdoptInstanceOwner(r.Context(), accountID); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			writeBootstrapError(w, http.StatusNotFound, "not_found", "account was not found")
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusConflict, "adoption_unavailable", "instance owner adoption is not available")
		default:
			internalError(s, w, err)
		}
		return
	}
	writeBootstrapJSON(w, http.StatusOK, map[string]string{"status": "adopted", "account_id": accountID.String()})
}

func githubOwnerInput(user githubauth.User, flow repository.GitHubDeviceFlow) (repository.GitHubOwnerInput, error) {
	login := strings.TrimSpace(user.Login)
	if user.ID <= 0 || login == "" || len(login) > 120 || strings.ContainsAny(login, "\x00\r\n") {
		return repository.GitHubOwnerInput{}, errors.New("invalid GitHub identity")
	}
	providerEmail := strings.TrimSpace(user.Email)
	if len(providerEmail) > 320 {
		providerEmail = ""
	}
	if providerEmail != "" {
		if normalized, err := validate.Email(providerEmail); err == nil {
			providerEmail = normalized
		} else {
			providerEmail = ""
		}
	}
	displayName := strings.TrimSpace(user.Name)
	if displayName == "" {
		displayName = login
	}
	avatarURL := strings.TrimSpace(user.AvatarURL)
	if len(displayName) > 240 || strings.ContainsAny(displayName, "\x00\r\n") || len(avatarURL) > 2048 || strings.ContainsAny(avatarURL, "\x00\r\n") {
		return repository.GitHubOwnerInput{}, errors.New("invalid GitHub identity metadata")
	}
	if avatarURL != "" {
		parsedAvatarURL, err := url.Parse(avatarURL)
		// GitHub commonly appends a cache/version query (for example, ?v=4)
		// to avatar URLs. It is display metadata, not a redirect target, so
		// keep safe HTTPS URLs while rejecting credentials and fragments.
		if err != nil || parsedAvatarURL.Scheme != "https" || parsedAvatarURL.Host == "" || parsedAvatarURL.User != nil || parsedAvatarURL.Fragment != "" {
			return repository.GitHubOwnerInput{}, errors.New("invalid GitHub avatar URL")
		}
	}
	return repository.GitHubOwnerInput{
		BootstrapSessionID: flow.ID,
		BootstrapCodeHash:  flow.CodeHash,
		AccountID:          uuid.Must(uuid.NewV7()),
		SessionID:          uuid.Must(uuid.NewV7()),
		ProviderUserID:     fmt.Sprintf("%d", user.ID),
		ProviderLogin:      login,
		ProviderEmail:      providerEmail,
		DisplayName:        displayName,
		AvatarURL:          avatarURL,
	}, nil
}

func (s *Server) verifyBootstrapCLI(w http.ResponseWriter, r *http.Request) bool {
	if !bootstrap.VerifyCLIProof(s.bootstrapCLIKey(), r.Header.Get(bootstrap.CLIProofHeader)) {
		writeBootstrapError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
		return false
	}
	return true
}

func (s *Server) updateGitHubDeviceFlow(w http.ResponseWriter, r *http.Request, sessionID uuid.UUID, status string, interval time.Duration, nextPollAt time.Time) bool {
	if err := s.repo.UpdateGitHubDeviceFlow(r.Context(), sessionID, status, interval, nextPollAt); err != nil {
		internalError(s, w, err)
		return false
	}
	return true
}

func (s *Server) bootstrapConfigured() bool {
	return len(s.bootstrapCLIKey()) == 32 && strings.TrimSpace(s.config.GitHubAppClientID) != ""
}

func (s *Server) bootstrapCLIKey() []byte {
	return s.config.BootstrapCLIKey
}

func (s *Server) allowBootstrapAttempt(w http.ResponseWriter, r *http.Request, dimension string) bool {
	return s.allowBootstrapAttemptWith(w, r, dimension, s.config.AuthRateLimit, s.config.AuthRateWindow, "bootstrap")
}

func (s *Server) allowBootstrapPoll(w http.ResponseWriter, r *http.Request) bool {
	return s.allowBootstrapAttemptWith(w, r, "", bootstrapPollRateLimit, bootstrapPollRateWindow, "bootstrap_poll")
}

func (s *Server) allowBootstrapAttemptWith(w http.ResponseWriter, r *http.Request, dimension string, limit int, window time.Duration, operation string) bool {
	keys := []string{ratelimit.InstanceIPKey(operation, s.requestClientIP(r))}
	if dimension != "" {
		keys = append(keys, ratelimit.InstanceKey(operation, strings.ToLower(strings.TrimSpace(dimension)), s.requestClientIP(r)))
	}
	for _, key := range keys {
		decision, err := s.limiter.Allow(r.Context(), key, limit, window)
		if err != nil {
			s.logger.Error("bootstrap rate limiter failed", "error", err)
			writeBootstrapError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication protection is temporarily unavailable")
			return false
		}
		if !decision.Allowed {
			return writeRateLimited(w, decision.RetryAfter)
		}
	}
	return true
}

func retryAfterSeconds(next time.Time) int {
	remaining := time.Until(next)
	if remaining <= 0 {
		return 1
	}
	return maxInt(1, int(math.Ceil(remaining.Seconds())))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func writeBootstrapJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, value)
}

func writeBootstrapError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, status, code, message)
}
