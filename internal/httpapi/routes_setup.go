package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/setupconfig"
	"github.com/Stealth-deplover/stealth/internal/setuphandoff"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type setupStatusResponse struct {
	SetupRequired bool                   `json:"setup_required"`
	Ready         bool                   `json:"ready"`
	State         setupstate.PublicState `json:"state"`
}

type setupCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Required bool   `json:"required"`
}

type setupPreflightResponse struct {
	Checks []setupCheck `json:"checks"`
}

type setupGitHubManifestResponse struct {
	ManifestURL string    `json:"manifest_url"`
	Manifest    string    `json:"manifest"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type setupGitHubAuthorizationResponse struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type setupGitHubManualRequest struct {
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	PrivateKey    string `json:"private_key"`
	WebhookSecret string `json:"webhook_secret"`
}

type setupCloudflareOAuthResponse struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type setupCloudflareTokenRequest struct {
	APIToken string `json:"api_token"`
}

type setupCloudflareTunnelRequest struct {
	AccountID string `json:"account_id"`
	ZoneID    string `json:"zone_id"`
	Hostname  string `json:"hostname"`
	Name      string `json:"name"`
}

type setupCloudflareStatusResponse struct {
	TunnelID    string `json:"tunnel_id"`
	Status      string `json:"status"`
	Healthy     bool   `json:"healthy"`
	Connections int    `json:"connections"`
}

type setupTestRequest struct {
	URL       string `json:"url"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	UseSSL    *bool  `json:"use_ssl"`
	PathStyle *bool  `json:"path_style"`
}

type setupQuickTunnelRequest struct {
	ContainerName string `json:"container_name"`
	URL           string `json:"url"`
}

type setupInstallResponse struct {
	Status string                 `json:"status"`
	State  setupstate.PublicState `json:"state"`
}

type setupHandoffStatusResponse struct {
	Pending bool `json:"pending"`
}

type setupInstallValidationError struct {
	err error
}

func (e *setupInstallValidationError) Error() string { return e.err.Error() }

func (e *setupInstallValidationError) Unwrap() error { return e.err }

func (s *Server) registerSetupRoutes(r chi.Router) {
	r.Get("/setup/status", s.setupStatus)
	r.Get("/setup/github/manifest/callback", s.setupGitHubManifestCallback)
	r.Get("/setup/github/authorize/callback", s.setupGitHubAuthorizationCallback)
	r.Get("/setup/cloudflare/oauth/callback", s.setupCloudflareOAuthCallback)
	r.Post("/setup/quick-tunnel", s.registerSetupQuickTunnel)
	r.Post("/setup/recovery", s.recoverSetupSession)
	r.With(s.requireSetup).Get("/setup/preflight", s.setupPreflight)
	r.With(s.requireSetupMutation).Put("/setup/config", s.saveSetupConfig)
	r.With(s.requireSetupMutation).Post("/setup/github/manifest/start", s.startGitHubManifest)
	r.With(s.requireSetupMutation).Post("/setup/github/authorize/start", s.startGitHubAuthorization)
	r.With(s.requireSetupMutation).Post("/setup/github/manual", s.saveGitHubManual)
	r.With(s.requireSetupMutation).Post("/setup/cloudflare/oauth/start", s.startCloudflareOAuth)
	r.With(s.requireSetup).Get("/setup/cloudflare/accounts", s.listCloudflareAccounts)
	r.With(s.requireSetup).Get("/setup/cloudflare/zones", s.listCloudflareZones)
	r.With(s.requireSetupMutation).Post("/setup/cloudflare/token", s.saveCloudflareToken)
	r.With(s.requireSetupMutation).Post("/setup/cloudflare/tunnel", s.createCloudflareTunnel)
	r.With(s.requireSetup).Get("/setup/cloudflare/status", s.cloudflareTunnelStatus)
	r.With(s.requireSetupMutation).Post("/setup/infrastructure/database/test", s.testSetupDatabase)
	r.With(s.requireSetupMutation).Post("/setup/infrastructure/redis/test", s.testSetupRedis)
	r.With(s.requireSetupMutation).Post("/setup/infrastructure/storage/test", s.testSetupStorage)
	r.With(s.requireSetup).Get("/setup/handoff-token", s.issueSetupHandoffToken)
	r.Get("/setup/handoff/status", s.setupHandoffStatus)
	r.With(s.requireSetupMutation).Post("/setup/install", s.startSetupInstall)
	r.With(s.requireSetup).Get("/setup/install/events", s.setupInstallEvents)
}

func (s *Server) issueSetupHandoffToken(w http.ResponseWriter, r *http.Request) {
	if s.setupHandoff == nil || s.bootstrap == nil {
		writeError(w, http.StatusServiceUnavailable, "handoff_unavailable", "production session handoff is unavailable")
		return
	}
	status, err := s.bootstrap.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if status.SetupRequired {
		writeError(w, http.StatusConflict, "handoff_not_ready", "finish first-owner setup before preparing the production dashboard")
		return
	}
	token, err := s.setupHandoff.Issue(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "handoff_unavailable", "the production session handoff is no longer available")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// setupHandoffStatus is a host-CLI-only observation endpoint. It is
// authenticated with the same private bootstrap proof as other CLI
// coordination calls and never exposes the handoff token or session.
func (s *Server) setupHandoffStatus(w http.ResponseWriter, r *http.Request) {
	if s.setupHandoff == nil {
		writeError(w, http.StatusServiceUnavailable, "handoff_unavailable", "production session handoff is unavailable")
		return
	}
	if !s.verifyBootstrapCLI(w, r) {
		return
	}
	pendingStore, ok := s.setupHandoff.(setuphandoff.PendingStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "handoff_unavailable", "production session handoff status is unavailable")
		return
	}
	pending, err := pendingStore.Pending(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, setupHandoffStatusResponse{Pending: pending})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	if !s.config.SetupMode || s.bootstrap == nil || s.setupState == nil {
		writeError(w, http.StatusNotFound, "not_found", "setup is not available")
		return
	}
	status, err := s.bootstrap.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, setupStatusResponse{SetupRequired: status.SetupRequired, Ready: s.setupStateReady(), State: state.Public()})
}

func (s *Server) saveSetupConfig(w http.ResponseWriter, r *http.Request) {
	var request setupconfig.Request
	if !decodeJSON(w, r, &request) {
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupState == nil {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "setup state is not available")
		return
	}
	state, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		return setupconfig.Apply(state, request)
	})
	if err != nil {
		var bindingConflict *setupstate.CloudflareBindingConflict
		if errors.As(err, &bindingConflict) {
			writeError(w, http.StatusConflict, "cloudflare_tunnel_reconfiguration_required", bindingConflict.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state.Public())
}

func (s *Server) startGitHubManifest(w http.ResponseWriter, r *http.Request) {
	if s.githubManifest == nil || s.githubOAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "github_manifest_unavailable", "GitHub App Manifest setup is unavailable")
		return
	}
	callbackURL, err := s.externalURL(r, "/v1/setup/github/manifest/callback")
	if err != nil || !strings.HasPrefix(callbackURL, "https://") {
		writeError(w, http.StatusUnprocessableEntity, "https_required", "open the temporary HTTPS setup URL before connecting GitHub")
		return
	}
	authorizationCallbackURL, err := s.externalURL(r, "/v1/setup/github/authorize/callback")
	if err != nil || !strings.HasPrefix(authorizationCallbackURL, "https://") {
		writeError(w, http.StatusUnprocessableEntity, "https_required", "open the temporary HTTPS setup URL before connecting GitHub")
		return
	}
	plainState, stateHash, expiresAt, err := setupstate.NewManifestState()
	if err != nil {
		internalError(s, w, err)
		return
	}
	appName, err := githubauth.DefaultAppName()
	if err != nil {
		internalError(s, w, err)
		return
	}
	appURL, appURLErr := s.externalURL(r, "/")
	if appURLErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "https_required", "the setup host is invalid")
		return
	}
	setupURL, setupURLErr := s.externalURL(r, "/setup")
	if setupURLErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "https_required", "the setup host is invalid")
		return
	}
	manifestURL, manifestPayload, err := githubauth.ManifestForm(githubauth.AppManifest{
		Name:         appName,
		Description:  "Stealth developer control plane",
		URL:          appURL,
		RedirectURL:  callbackURL,
		CallbackURLs: []string{authorizationCallbackURL},
		Public:       false,
		// The setup service does not consume GitHub webhooks. Requesting an
		// installation event without a hook endpoint would make the manifest
		// inconsistent with the actual installation and needlessly expand the
		// App's configuration.
		DefaultEvents: []string{},
		DefaultPerms:  map[string]string{"contents": "read", "metadata": "read"},
		SetupURL:      setupURL,
	}, plainState)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "github_manifest_invalid", "the setup host cannot be used for GitHub App registration")
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		state.GitHub.Mode = "manifest"
		state.GitHub.ManifestStateHash = stateHash
		state.GitHub.ManifestExpiresAt = expiresAt
		return nil
	}); err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", "the GitHub setup session could not be started")
		return
	}
	writeJSON(w, http.StatusOK, setupGitHubManifestResponse{ManifestURL: manifestURL, Manifest: manifestPayload, ExpiresAt: expiresAt})
}

func (s *Server) setupGitHubManifestCallback(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	providedState := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\x00\r\n") || providedState == "" || s.setupState == nil || s.githubManifest == nil || s.githubOAuth == nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil || state.GitHub.ManifestExpiresAt.Before(time.Now().UTC()) || !compareStateHash(state.GitHub.ManifestStateHash, providedState) {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		if state.GitHub.ManifestExpiresAt.Before(time.Now().UTC()) || !compareStateHash(state.GitHub.ManifestStateHash, providedState) {
			return errors.New("GitHub manifest state is expired or already used")
		}
		state.GitHub.ManifestStateHash = ""
		state.GitHub.ManifestExpiresAt = time.Time{}
		return nil
	}); err != nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	credentials, err := s.githubManifest.ConvertManifest(r.Context(), code)
	if err != nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	_, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		state.GitHub.Mode = "manifest"
		state.GitHub.ClientID = credentials.ClientID
		state.GitHub.Connected = true
		state.SetSecret("github_client_secret", credentials.ClientSecret)
		state.SetSecret("github_private_key", credentials.PrivateKey)
		state.SetSecret("github_webhook_secret", credentials.WebhookSecret)
		return nil
	})
	if err != nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	authorizationURL, _, err := s.beginGitHubAuthorization(r.Context(), r, credentials.ClientID)
	if err != nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (s *Server) startGitHubAuthorization(w http.ResponseWriter, r *http.Request) {
	if s.githubOAuth == nil || s.setupState == nil {
		writeError(w, http.StatusServiceUnavailable, "github_authorization_unavailable", "GitHub browser authorization is unavailable")
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if !state.GitHub.Connected || !githubauth.ValidClientID(state.GitHub.ClientID) || strings.TrimSpace(state.Secret("github_client_secret")) == "" {
		writeError(w, http.StatusConflict, "github_not_connected", "connect a GitHub App before authorizing the first owner")
		return
	}
	authorizationURL, expiresAt, err := s.beginGitHubAuthorization(r.Context(), r, state.GitHub.ClientID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "github_authorization_invalid", "the setup host cannot be used for GitHub browser authorization")
		return
	}
	writeJSON(w, http.StatusOK, setupGitHubAuthorizationResponse{AuthorizationURL: authorizationURL, ExpiresAt: expiresAt})
}

func (s *Server) beginGitHubAuthorization(ctx context.Context, r *http.Request, clientID string) (string, time.Time, error) {
	if s.githubOAuth == nil || s.setupState == nil || !githubauth.ValidClientID(clientID) {
		return "", time.Time{}, errors.New("GitHub browser authorization is unavailable")
	}
	callbackURL, err := s.externalURL(r, "/v1/setup/github/authorize/callback")
	if err != nil || !strings.HasPrefix(callbackURL, "https://") {
		return "", time.Time{}, errors.New("GitHub browser authorization requires HTTPS")
	}
	plainState, stateHash, expiresAt, err := setupstate.NewOAuthState()
	if err != nil {
		return "", time.Time{}, err
	}
	codeVerifier, codeChallenge, err := githubauth.NewPKCE()
	if err != nil {
		return "", time.Time{}, err
	}
	authorizationURL, err := githubauth.AuthorizationURL(clientID, callbackURL, plainState, codeChallenge)
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err := s.setupState.Update(ctx, func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		if !state.GitHub.Connected || state.GitHub.ClientID != clientID || strings.TrimSpace(state.Secret("github_client_secret")) == "" {
			return errors.New("GitHub App is not connected")
		}
		state.GitHub.AuthorizationStateHash = stateHash
		state.GitHub.AuthorizationExpiresAt = expiresAt
		state.SetSecret("github_oauth_code_verifier", codeVerifier)
		return nil
	}); err != nil {
		return "", time.Time{}, err
	}
	return authorizationURL, expiresAt, nil
}

func (s *Server) setupGitHubAuthorizationCallback(w http.ResponseWriter, r *http.Request) {
	providedState := strings.TrimSpace(r.URL.Query().Get("state"))
	if providedState == "" || len(providedState) > 512 || strings.ContainsAny(providedState, "\x00\r\n") || s.setupState == nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	var clientID, clientSecret, codeVerifier, setupSessionID, setupCodeHash string
	_, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		if state.GitHub.AuthorizationExpiresAt.Before(time.Now().UTC()) || !compareStateHash(state.GitHub.AuthorizationStateHash, providedState) {
			return errors.New("GitHub authorization state is expired or already used")
		}
		clientID = strings.TrimSpace(state.GitHub.ClientID)
		clientSecret = state.Secret("github_client_secret")
		codeVerifier = state.Secret("github_oauth_code_verifier")
		setupSessionID = state.SetupSessionID
		setupCodeHash = state.SetupCodeHash
		if !githubauth.ValidClientID(clientID) || clientSecret == "" || codeVerifier == "" || setupSessionID == "" || setupCodeHash == "" {
			return errors.New("GitHub authorization state is incomplete")
		}
		state.GitHub.AuthorizationStateHash = ""
		state.GitHub.AuthorizationExpiresAt = time.Time{}
		state.SetSecret("github_oauth_code_verifier", "")
		return nil
	})
	if err != nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if r.URL.Query().Get("error") != "" || code == "" || len(code) > 4096 || strings.ContainsAny(code, "\x00\r\n") || s.githubOAuth == nil || s.bootstrap == nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	callbackURL, err := s.externalURL(r, "/v1/setup/github/authorize/callback")
	if err != nil || !strings.HasPrefix(callbackURL, "https://") {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	token, err := s.githubOAuth.ExchangeAuthorizationCode(r.Context(), clientID, clientSecret, code, callbackURL, codeVerifier)
	if err != nil {
		s.logger.Warn("GitHub browser authorization exchange failed", "error", err)
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		s.logger.Warn("GitHub browser authorization returned an empty access token")
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	user, err := s.githubOAuth.GetUser(r.Context(), token.AccessToken)
	if err != nil {
		s.logger.Warn("GitHub browser identity lookup failed", "error", err)
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	sessionID, err := uuid.Parse(setupSessionID)
	if err != nil {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	codeHash, err := base64.RawURLEncoding.DecodeString(setupCodeHash)
	if err != nil || len(codeHash) != 32 {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	verification, err := s.bootstrap.VerifyBootstrapCode(r.Context(), codeHash)
	if err != nil || verification.ID != sessionID {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	owner, err := s.createGitHubInstanceOwner(r.Context(), bootstrap.GitHubAuthorization{SessionID: sessionID, CodeHash: codeHash}, user)
	if err != nil {
		s.logger.Warn("GitHub browser owner creation failed", "error", err)
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	if !s.config.SetupMode {
		s.setSessionCookie(w, owner.SessionToken)
	}
	http.Redirect(w, r, setupRedirect("/setup?github=connected"), http.StatusFound)
}

func (s *Server) saveGitHubManual(w http.ResponseWriter, r *http.Request) {
	var request setupGitHubManualRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !githubauth.ValidClientID(request.ClientID) || strings.TrimSpace(request.ClientSecret) == "" || strings.TrimSpace(request.PrivateKey) == "" || strings.ContainsAny(request.ClientSecret+request.WebhookSecret, "\x00\r\n") || strings.ContainsRune(request.PrivateKey, '\x00') || len(request.ClientSecret) > 512 || len(request.PrivateKey) > 32768 || len(request.WebhookSecret) > 512 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "GitHub App credentials are invalid")
		return
	}
	if !strings.Contains(request.PrivateKey, "BEGIN") || !strings.Contains(request.PrivateKey, "END") {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "GitHub private key is invalid")
		return
	}
	state, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		state.GitHub.Mode = "manual"
		state.GitHub.ClientID = strings.TrimSpace(request.ClientID)
		state.GitHub.Connected = true
		state.SetSecret("github_client_secret", request.ClientSecret)
		state.SetSecret("github_private_key", request.PrivateKey)
		state.SetSecret("github_webhook_secret", request.WebhookSecret)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", "the GitHub settings could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, state.Public())
}

func (s *Server) startCloudflareOAuth(w http.ResponseWriter, r *http.Request) {
	// OAuth support remains in the repository for future provider validation,
	// but this setup flow deliberately stays inactive until Cloudflare provides
	// a verified redirect and scope configuration for the deployed origin. In
	// particular, never derive an OAuth redirect from a random Quick Tunnel.
	writeError(w, http.StatusGone, "cloudflare_oauth_inactive", "Cloudflare OAuth is experimental and inactive; use a scoped Cloudflare API token")
}

func (s *Server) setupCloudflareOAuthCallback(w http.ResponseWriter, r *http.Request) {
	// A stale or externally initiated callback must not exchange a code or
	// mutate encrypted setup state while OAuth is inactive.
	http.Redirect(w, r, setupRedirect("/setup?cloudflare=oauth-inactive"), http.StatusFound)
}

func (s *Server) listCloudflareAccounts(w http.ResponseWriter, r *http.Request) {
	client, err := s.cloudflareClient(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "cloudflare_not_connected", "connect Cloudflare before choosing an account")
		return
	}
	accounts, err := client.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "cloudflare_unavailable", "Cloudflare accounts could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Server) listCloudflareZones(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "account_id is required")
		return
	}
	client, err := s.cloudflareClient(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "cloudflare_not_connected", "connect Cloudflare before choosing a zone")
		return
	}
	zones, err := client.ListZones(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cloudflare_unavailable", "Cloudflare zones could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"zones": zones})
}

func (s *Server) saveCloudflareToken(w http.ResponseWriter, r *http.Request) {
	var request setupCloudflareTokenRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	token := strings.TrimSpace(request.APIToken)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "Cloudflare API token is invalid")
		return
	}
	if s.cloudflareFactory == nil || s.setupState == nil {
		writeError(w, http.StatusServiceUnavailable, "cloudflare_unavailable", "Cloudflare setup is not available")
		return
	}
	client, err := s.cloudflareFactory(token)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cloudflare_token_invalid", "Cloudflare API token is invalid")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := client.ListAccounts(ctx); err != nil {
		writeError(w, http.StatusBadGateway, "cloudflare_token_rejected", "Cloudflare rejected the API token")
		return
	}
	state, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		state.Cloudflare.Mode = "api_token"
		state.Cloudflare.Connected = true
		state.Cloudflare.TokenValid = true
		state.Cloudflare.ExpiresAt = time.Time{}
		state.Cloudflare.OAuthStateHash = ""
		state.Cloudflare.OAuthExpiresAt = time.Time{}
		state.SetSecret("cloudflare_access_token", token)
		state.SetSecret("cloudflare_refresh_token", "")
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", "Cloudflare settings could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, state.Public())
}

func (s *Server) createCloudflareTunnel(w http.ResponseWriter, r *http.Request) {
	var request setupCloudflareTunnelRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	client, err := s.cloudflareClient(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "cloudflare_not_connected", "connect Cloudflare before creating a tunnel")
		return
	}
	state, err := cloudflare.Provision(r.Context(), s.setupState, client, cloudflare.ProvisionRequest{
		AccountID: request.AccountID,
		ZoneID:    request.ZoneID,
		Hostname:  request.Hostname,
		Name:      request.Name,
	})
	if err != nil {
		var provisioningErr *cloudflare.ProvisionError
		_ = errors.As(err, &provisioningErr)
		switch {
		case errors.Is(err, cloudflare.ErrInvalidRequest):
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "Cloudflare account, zone, and hostname are invalid")
		case errors.Is(err, cloudflare.ErrConflict):
			var bindingConflict *setupstate.CloudflareBindingConflict
			if provisioningErr != nil && errors.As(provisioningErr.Err, &bindingConflict) {
				writeError(w, http.StatusConflict, "cloudflare_tunnel_reconfiguration_required", bindingConflict.Error())
			} else {
				writeError(w, http.StatusConflict, "cloudflare_tunnel_conflict", "the saved Cloudflare tunnel conflicts with the requested account, zone, hostname, or provider resource")
			}
		case errors.Is(err, cloudflare.ErrState):
			writeError(w, http.StatusConflict, "setup_state_conflict", "Cloudflare setup state could not be saved")
		case errors.Is(err, cloudflare.ErrProvider):
			if provisioningErr != nil && strings.HasPrefix(strings.ToLower(provisioningErr.Stage), "dns") {
				writeError(w, http.StatusBadGateway, "cloudflare_dns_failed", "Cloudflare could not configure the DNS record")
			} else if provisioningErr != nil && provisioningErr.Stage == "zones" {
				writeError(w, http.StatusBadGateway, "cloudflare_unavailable", "Cloudflare zones could not be verified")
			} else {
				writeError(w, http.StatusBadGateway, "cloudflare_tunnel_failed", "Cloudflare could not provision the named tunnel")
			}
		default:
			internalError(s, w, err)
		}
		return
	}
	if s.repo != nil {
		binding := state.EffectiveCloudflareBinding().Normalized()
		persistErr := s.repo.SaveCloudflareConnectionFromSetup(r.Context(), repository.CloudflareConnectionInput{
			AccountID: binding.AccountID, ConsoleZoneID: binding.ZoneID, ConsoleHostname: binding.Hostname,
			TunnelID: binding.TunnelID, TunnelName: binding.TunnelName, ConsoleRecordID: binding.RecordID,
			APIToken: strings.TrimSpace(state.Secret("cloudflare_access_token")),
		})
		if persistErr != nil {
			if errors.Is(persistErr, repository.ErrCloudflareConnectionConflict) {
				writeError(w, http.StatusConflict, "cloudflare_connection_conflict", "a different production Cloudflare tunnel connection is already saved")
			} else {
				writeError(w, http.StatusServiceUnavailable, "cloudflare_connection_unavailable", "Cloudflare was provisioned, but its production connection could not be persisted; retry this setup step")
			}
			return
		}
	}
	writeJSON(w, http.StatusOK, state.Public())
}

func (s *Server) cloudflareTunnelStatus(w http.ResponseWriter, r *http.Request) {
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	binding := state.EffectiveCloudflareBinding()
	if err := state.Cloudflare.Binding.ValidateDraft(state.Draft); err != nil {
		var bindingConflict *setupstate.CloudflareBindingConflict
		if errors.As(err, &bindingConflict) {
			writeError(w, http.StatusConflict, "cloudflare_tunnel_reconfiguration_required", bindingConflict.Error())
		} else {
			writeError(w, http.StatusConflict, "setup_state_conflict", "Cloudflare tunnel state is inconsistent")
		}
		return
	}
	if binding.AccountID == "" || binding.TunnelID == "" {
		writeError(w, http.StatusConflict, "cloudflare_tunnel_missing", "create the named tunnel before checking status")
		return
	}
	client, err := s.cloudflareClient(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "cloudflare_not_connected", "connect Cloudflare before checking tunnel status")
		return
	}
	status, err := client.TunnelStatus(r.Context(), binding.AccountID, binding.TunnelID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cloudflare_unavailable", "Cloudflare tunnel status is unavailable")
		return
	}
	_, _ = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return nil
		}
		state.Cloudflare.TokenValid = true
		return nil
	})
	writeJSON(w, http.StatusOK, setupCloudflareStatusResponse{TunnelID: binding.TunnelID, Status: status.Status, Healthy: cloudflare.StatusIsHealthy(status), Connections: status.Connections})
}

func (s *Server) cloudflareClient(ctx context.Context) (cloudflare.Client, error) {
	if s.cloudflareFactory == nil || s.setupState == nil {
		return nil, errors.New("Cloudflare is not configured")
	}
	state, err := s.setupState.Load(ctx)
	if err != nil {
		return nil, err
	}
	if state.Cloudflare.Mode != "api_token" || !state.Cloudflare.Connected || !state.Cloudflare.TokenValid {
		return nil, errors.New("Cloudflare is not connected with a scoped API token")
	}
	token := state.Secret("cloudflare_access_token")
	if token == "" {
		return nil, errors.New("Cloudflare is not connected")
	}
	return s.cloudflareFactory(token)
}

func (s *Server) testSetupDatabase(w http.ResponseWriter, r *http.Request) {
	var request setupTestRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	credentials := state.SetupCredentials()
	databaseURL := strings.TrimSpace(request.URL)
	if databaseURL == "" {
		databaseURL = credentials.DatabaseURL
		if databaseURL == "" {
			databaseURL = s.config.DatabaseURL
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := pingDatabase(ctx, databaseURL); err != nil {
		writeError(w, http.StatusBadGateway, "database_connection_failed", "PostgreSQL connection test failed")
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		if state.Draft.DatabaseMode != "external" || state.SetupCredentials().DatabaseURL != databaseURL {
			return errors.New("test the saved external PostgreSQL URL before installing")
		}
		state.Draft.DatabaseTested = true
		return nil
	}); err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) testSetupRedis(w http.ResponseWriter, r *http.Request) {
	var request setupTestRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	credentials := state.SetupCredentials()
	redisURL := strings.TrimSpace(request.URL)
	if redisURL == "" {
		redisURL = credentials.RedisURL
		if redisURL == "" {
			redisURL = s.config.RedisURL
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := pingRedis(ctx, redisURL); err != nil {
		writeError(w, http.StatusBadGateway, "redis_connection_failed", "Redis connection test failed")
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		if state.Draft.RedisMode != "external" || state.SetupCredentials().RedisURL != redisURL {
			return errors.New("test the saved external Redis URL before installing")
		}
		state.Draft.RedisTested = true
		return nil
	}); err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) testSetupStorage(w http.ResponseWriter, r *http.Request) {
	var request setupTestRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.pingSetupStorage(ctx, state, request); err != nil {
		writeError(w, http.StatusBadGateway, "storage_connection_failed", "storage connection test failed")
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return errors.New("installation is already in progress or complete")
		}
		if state.Draft.StorageMode != "s3" {
			state.Draft.StorageTested = true
			return nil
		}
		credentials := state.SetupCredentials()
		endpoint := valueOr(request.Endpoint, state.Draft.StorageS3Endpoint)
		region := valueOr(request.Region, state.Draft.StorageS3Region)
		bucket := valueOr(request.Bucket, state.Draft.StorageS3Bucket)
		accessKey := valueOr(request.AccessKey, credentials.StorageS3AccessKey)
		secretKey := valueOr(request.SecretKey, credentials.StorageS3SecretKey)
		useSSL := state.Draft.StorageS3UseSSL
		if request.UseSSL != nil {
			useSSL = *request.UseSSL
		}
		pathStyle := state.Draft.StorageS3PathStyle
		if request.PathStyle != nil {
			pathStyle = *request.PathStyle
		}
		if endpoint != state.Draft.StorageS3Endpoint || region != state.Draft.StorageS3Region || bucket != state.Draft.StorageS3Bucket || accessKey != credentials.StorageS3AccessKey || secretKey != credentials.StorageS3SecretKey || useSSL != state.Draft.StorageS3UseSSL || pathStyle != state.Draft.StorageS3PathStyle {
			return errors.New("test the saved S3-compatible storage settings before installing")
		}
		state.Draft.StorageTested = true
		return nil
	}); err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// recoverSetupSession mints a fresh short-lived setup code only after the
// local CLI proves possession of the installation's bootstrap key. This is
// the trusted recovery path for a setup API restart after the first owner has
// already sealed the bootstrap row; anonymous remote callers cannot create a
// new first-run session.
func (s *Server) recoverSetupSession(w http.ResponseWriter, r *http.Request) {
	if !s.setupStateReady() || !s.verifyBootstrapCLI(w, r) {
		return
	}
	status, err := s.bootstrap.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if status.SetupRequired {
		writeError(w, http.StatusConflict, "recovery_not_needed", "first-owner setup is still waiting for its original local session")
		return
	}
	code, err := bootstrap.GenerateCode()
	if err != nil {
		internalError(s, w, err)
		return
	}
	codeHash := bootstrap.HashCode(code)
	sessionID, err := uuid.NewV7()
	if err != nil {
		internalError(s, w, err)
		return
	}
	expiresAt := time.Now().UTC().Add(bootstrap.CodeLifetime)
	_, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		// Recovery is CLI-proofed and host-owned. It must remain available for
		// an installing or handoff run so a browser can reconnect after the
		// setup API/container was restarted. A completed run is the only state
		// that must not receive a new setup claim.
		if state.Phase == setupstate.PhaseComplete {
			return errors.New("installation is already complete")
		}
		state.SetupSessionID = sessionID.String()
		state.SetupCodeHash = base64.RawURLEncoding.EncodeToString(codeHash)
		state.SetupExpiresAt = expiresAt
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", "a setup recovery session could not be saved")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeBootstrapJSON(w, http.StatusCreated, bootstrapSessionResponse{SetupCode: code, ExpiresAt: expiresAt})
}

func (s *Server) pingSetupStorage(ctx context.Context, state setupstate.State, request setupTestRequest) error {
	if state.Draft.StorageMode != "s3" {
		if err := os.MkdirAll(s.config.StorageRoot, 0o700); err != nil {
			return err
		}
		file, err := os.CreateTemp(s.config.StorageRoot, ".setup-storage-test-*")
		if err != nil {
			return err
		}
		name := file.Name()
		defer os.Remove(name)
		if _, err := file.WriteString("stealth"); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}
	credentials := state.SetupCredentials()
	endpoint := valueOr(request.Endpoint, state.Draft.StorageS3Endpoint)
	region := valueOr(request.Region, state.Draft.StorageS3Region)
	bucket := valueOr(request.Bucket, state.Draft.StorageS3Bucket)
	accessKey := valueOr(request.AccessKey, credentials.StorageS3AccessKey)
	secretKey := valueOr(request.SecretKey, credentials.StorageS3SecretKey)
	useSSL := state.Draft.StorageS3UseSSL
	if request.UseSSL != nil {
		useSSL = *request.UseSSL
	}
	pathStyle := state.Draft.StorageS3PathStyle
	if request.PathStyle != nil {
		pathStyle = *request.PathStyle
	}
	store, err := storage.NewS3(storage.S3Options{Endpoint: endpoint, Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey, UseSSL: useSSL, ForcePathStyle: pathStyle, Prefix: state.Draft.StorageS3Prefix, StagingRoot: filepath.Join(s.config.StorageRoot, "setup-staging")}, s.config.StorageMaxFileSize)
	if err != nil {
		return err
	}
	return store.Ping(ctx)
}

func (s *Server) registerSetupQuickTunnel(w http.ResponseWriter, r *http.Request) {
	if !s.setupStateReady() {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "setup state is not available")
		return
	}
	if !s.verifyBootstrapCLI(w, r) {
		return
	}
	var request setupQuickTunnelRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !validQuickTunnelName(request.ContainerName) || !validQuickTunnelURL(request.URL) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "temporary tunnel details are invalid")
		return
	}
	status, err := s.bootstrap.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	state, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		// This endpoint is CLI-proofed and is a host-owned coordination
		// projection. The host may need to replace a Quick Tunnel after a
		// restart or transient disconnect while production installation is
		// already locked. Browser-owned mutations remain sealed by the normal
		// setup middleware and setupconfig.Apply guard.
		if state.Phase == setupstate.PhaseComplete {
			return errors.New("installation is already complete")
		}
		state.QuickTunnel = request.ContainerName
		if status.SetupRequired {
			state.SetupSessionID = ""
			state.SetupCodeHash = ""
			state.SetupExpiresAt = time.Time{}
		}
		state.SetSecret("quick_tunnel_url", strings.TrimRight(request.URL, "/"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", "temporary tunnel details could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, state.Public())
}

func (s *Server) startSetupInstall(w http.ResponseWriter, r *http.Request) {
	var ignored map[string]any
	if !decodeJSON(w, r, &ignored) {
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if !s.setupStateReady() {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "the setup service is not ready")
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if state.Phase == setupstate.PhaseInstallRequested || state.Phase == setupstate.PhaseInstalling {
		status := "accepted"
		if state.Phase == setupstate.PhaseInstalling {
			status = "installing"
		}
		writeJSON(w, http.StatusAccepted, setupInstallResponse{Status: status, State: state.Public()})
		return
	}
	if state.Phase == setupstate.PhaseComplete || state.Phase == setupstate.PhaseHandoff {
		writeError(w, http.StatusConflict, "setup_complete", "installation has already been handed off")
		return
	}
	bootstrapStatus, err := s.bootstrap.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if bootstrapStatus.SetupRequired {
		writeError(w, http.StatusConflict, "setup_owner_required", "finish GitHub first-owner verification before installing")
		return
	}
	if err := setupconfig.ValidateInstallableSetup(state); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	runID := uuid.NewString()
	state, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if err := setupconfig.ValidateInstallableSetup(*state); err != nil {
			return &setupInstallValidationError{err: err}
		}
		if err := setupstate.RequestInstallation(state, runID); err != nil {
			return err
		}
		state.Step = "Configuration and secrets"
		return nil
	})
	if err != nil {
		var validationErr *setupInstallValidationError
		if errors.As(err, &validationErr) {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", validationErr.Error())
			return
		}
		writeError(w, http.StatusConflict, "setup_state_conflict", "the installation could not be started")
		return
	}
	writeJSON(w, http.StatusAccepted, setupInstallResponse{Status: "accepted", State: state.Public()})
}

func (s *Server) setupInstallEvents(w http.ResponseWriter, r *http.Request) {
	if s.setupState == nil {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "setup events are not available")
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "sse_unavailable", "install event streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, "snapshot", state.LastEventID, state.Public())
	flusher.Flush()
	poll := time.NewTicker(setupEventPoll)
	defer poll.Stop()
	heartbeat := time.NewTicker(setupEventHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			latest, loadErr := s.setupState.Load(r.Context())
			if loadErr != nil || latest.LastEventID <= state.LastEventID {
				continue
			}
			state = latest
			writeSSE(w, "progress", state.LastEventID, map[string]string{
				"step":  state.Step,
				"error": state.ErrorMessage,
			})
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, eventName string, id uint64, payload any) {
	contents, err := jsonMarshal(payload)
	if err != nil {
		return
	}
	if id > 0 {
		_, _ = io.WriteString(w, "id: "+strconv.FormatUint(id, 10)+"\n")
	}
	_, _ = io.WriteString(w, "event: "+eventName+"\n")
	_, _ = io.WriteString(w, "data: "+contents+"\n\n")
}

func pingDatabase(ctx context.Context, raw string) error {
	if !setupconfig.ValidDatabaseURL(raw) {
		return errors.New("invalid PostgreSQL URL")
	}
	poolConfig, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return err
	}
	poolConfig.MinConns = 0
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	return pool.Ping(ctx)
}

func pingRedis(ctx context.Context, raw string) error {
	if !setupconfig.ValidRedisURL(raw) {
		return errors.New("invalid Redis URL")
	}
	options, err := redis.ParseURL(raw)
	if err != nil {
		return err
	}
	client := redis.NewClient(options)
	defer client.Close()
	return client.Ping(ctx).Err()
}

func validQuickTunnelName(value string) bool {
	return strings.HasPrefix(value, "stealth-onboarding-") && len(value) <= 128 && safeDockerName(value)
}

func validQuickTunnelURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && strings.HasSuffix(host, ".trycloudflare.com") && strings.TrimSuffix(host, ".trycloudflare.com") != ""
}

func safeDockerName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			if index == 0 && character != 's' {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

// jsonMarshal is kept local to the SSE writer so the event path has no
// dependency on the HTTP JSON response headers or on request-scoped state.
func jsonMarshal(value any) (string, error) {
	contents, err := json.Marshal(value)
	return string(contents), err
}
