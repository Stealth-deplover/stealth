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
	"github.com/Stealth-deplover/stealth/internal/installengine"
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

type setupConfigRequest struct {
	InstanceName       string `json:"instance_name"`
	PublicURL          string `json:"public_url"`
	NetworkMode        string `json:"network_mode"`
	Hostname           string `json:"hostname"`
	DatabaseMode       string `json:"database_mode"`
	DatabaseURL        string `json:"database_url"`
	RedisMode          string `json:"redis_mode"`
	RedisURL           string `json:"redis_url"`
	StorageMode        string `json:"storage_mode"`
	StorageS3Endpoint  string `json:"storage_s3_endpoint"`
	StorageS3Region    string `json:"storage_s3_region"`
	StorageS3Bucket    string `json:"storage_s3_bucket"`
	StorageS3AccessKey string `json:"storage_s3_access_key"`
	StorageS3SecretKey string `json:"storage_s3_secret_key"`
	StorageS3UseSSL    *bool  `json:"storage_s3_use_ssl"`
	StorageS3PathStyle *bool  `json:"storage_s3_path_style"`
	StorageS3Prefix    string `json:"storage_s3_prefix"`
}

type setupGitHubManifestResponse struct {
	ManifestURL string    `json:"manifest_url"`
	ExpiresAt   time.Time `json:"expires_at"`
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

func (s *Server) registerSetupRoutes(r chi.Router) {
	r.Get("/setup/status", s.setupStatus)
	r.Get("/setup/github/manifest/callback", s.setupGitHubManifestCallback)
	r.Get("/setup/cloudflare/oauth/callback", s.setupCloudflareOAuthCallback)
	r.Post("/setup/quick-tunnel", s.registerSetupQuickTunnel)
	r.Post("/setup/recovery", s.recoverSetupSession)
	r.With(s.requireSetup).Get("/setup/preflight", s.setupPreflight)
	r.With(s.requireSetupMutation).Put("/setup/config", s.saveSetupConfig)
	r.With(s.requireSetupMutation).Post("/setup/github/manifest/start", s.startGitHubManifest)
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

func (s *Server) setupPreflight(w http.ResponseWriter, r *http.Request) {
	checks := make([]setupCheck, 0, 5)
	add := func(name, detail string, ok, required bool) {
		status := "pass"
		if !ok {
			status = "fail"
			if !required {
				status = "warn"
			}
		}
		checks = append(checks, setupCheck{Name: name, Detail: detail, Status: status, Required: required})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if s.repo == nil {
		add("Database", "database dependency is not configured", false, true)
	} else if err := s.repo.Ping(ctx); err != nil {
		add("Database", "PostgreSQL is not ready", false, true)
	} else {
		add("Database", "PostgreSQL is reachable", true, true)
	}
	if s.storage == nil || !s.storageReady {
		add("Storage", "storage is not configured", false, true)
	} else if err := s.storage.Ping(ctx); err != nil {
		add("Storage", "storage is not reachable", false, true)
	} else {
		add("Storage", "storage is reachable", true, true)
	}
	if s.limiter == nil {
		add("Redis", "rate limiter is not configured", false, true)
	} else if err := s.limiter.Ping(ctx); err != nil {
		add("Redis", "Redis is not reachable", false, true)
	} else {
		add("Redis", "Redis is reachable", true, true)
	}
	if s.setupRunner == nil {
		add("Docker", "Docker command runner is not configured", false, true)
	} else {
		if _, err := s.setupRunner.Output(ctx, "", "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
			add("Docker", "Docker is not available", false, true)
		} else {
			add("Docker", "Docker is available", true, true)
		}
	}
	writeJSON(w, http.StatusOK, setupPreflightResponse{Checks: checks})
}

func (s *Server) saveSetupConfig(w http.ResponseWriter, r *http.Request) {
	var request setupConfigRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	state, err := s.saveSetupDraft(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state.Public())
}

func (s *Server) saveSetupDraft(ctx context.Context, request setupConfigRequest) (setupstate.State, error) {
	if s.setupState == nil {
		return setupstate.State{}, setupstate.ErrUnavailable
	}
	instanceName := strings.TrimSpace(request.InstanceName)
	if instanceName == "" {
		instanceName = "Stealth"
	}
	if len(instanceName) > 120 || strings.ContainsAny(instanceName, "\x00\r\n") {
		return setupstate.State{}, errors.New("instance name is invalid")
	}
	state, err := s.setupState.Update(ctx, func(state *setupstate.State) error {
		if state.Phase == setupstate.PhaseInstalling || state.Phase == setupstate.PhaseHandoff || state.Phase == setupstate.PhaseComplete {
			return errors.New("installation is already in progress or complete")
		}
		networkMode := strings.ToLower(strings.TrimSpace(request.NetworkMode))
		if networkMode == "" {
			networkMode = state.Draft.NetworkMode
		}
		if !validNetworkMode(networkMode) {
			return errors.New("network mode is invalid")
		}
		hostname := strings.TrimSpace(request.Hostname)
		if hostname != "" {
			var validationErr error
			hostname, validationErr = setupstate.ValidateHostname(hostname)
			if validationErr != nil {
				return validationErr
			}
		}
		publicURL := strings.TrimSpace(request.PublicURL)
		if publicURL == "" && hostname != "" {
			publicURL = "https://" + hostname
		}
		if publicURL == "" {
			publicURL = "http://127.0.0.1:8081"
		}
		validatedPublicURL, validationErr := setupstate.ValidatePublicURL(publicURL)
		if validationErr != nil {
			return validationErr
		}
		publicURL = validatedPublicURL
		databaseMode := strings.ToLower(strings.TrimSpace(request.DatabaseMode))
		if databaseMode == "" {
			databaseMode = "bundled"
		}
		if databaseMode != "bundled" && databaseMode != "external" {
			return errors.New("database mode must be bundled or external")
		}
		databaseURL := strings.TrimSpace(request.DatabaseURL)
		if databaseMode == "external" {
			if databaseURL == "" {
				databaseURL = state.Draft.DatabaseURL
			}
			if !validDatabaseURL(databaseURL) {
				return errors.New("external PostgreSQL URL is invalid")
			}
		} else {
			databaseURL = ""
		}
		redisMode := strings.ToLower(strings.TrimSpace(request.RedisMode))
		if redisMode == "" {
			redisMode = "bundled"
		}
		if redisMode != "bundled" && redisMode != "external" {
			return errors.New("Redis mode must be bundled or external")
		}
		redisURL := strings.TrimSpace(request.RedisURL)
		if redisMode == "external" {
			if redisURL == "" {
				redisURL = state.Draft.RedisURL
			}
			if !validRedisURL(redisURL) {
				return errors.New("external Redis URL is invalid")
			}
		} else {
			redisURL = ""
		}
		storageMode := strings.ToLower(strings.TrimSpace(request.StorageMode))
		if storageMode == "" {
			storageMode = "local"
		}
		if storageMode != "local" && storageMode != "s3" {
			return errors.New("storage mode must be local or s3")
		}
		storageEndpoint := strings.TrimSpace(request.StorageS3Endpoint)
		if storageEndpoint == "" {
			storageEndpoint = state.Draft.StorageS3Endpoint
		}
		storageRegion := strings.TrimSpace(request.StorageS3Region)
		if storageRegion == "" {
			storageRegion = state.Draft.StorageS3Region
		}
		storageBucket := strings.TrimSpace(request.StorageS3Bucket)
		if storageBucket == "" {
			storageBucket = state.Draft.StorageS3Bucket
		}
		storageAccessKey := strings.TrimSpace(request.StorageS3AccessKey)
		if storageAccessKey == "" {
			storageAccessKey = state.Secret("storage_s3_access_key")
		}
		storageSecretKey := request.StorageS3SecretKey
		if storageSecretKey == "" {
			storageSecretKey = state.Secret("storage_s3_secret_key")
		}
		storageUseSSL := state.Draft.StorageS3UseSSL
		if request.StorageS3UseSSL != nil {
			storageUseSSL = *request.StorageS3UseSSL
		}
		storagePathStyle := state.Draft.StorageS3PathStyle
		if request.StorageS3PathStyle != nil {
			storagePathStyle = *request.StorageS3PathStyle
		}
		if storageMode == "s3" && !validS3Settings(storageEndpoint, storageRegion, storageBucket, storageAccessKey, storageSecretKey) {
			return errors.New("S3-compatible storage settings are incomplete or invalid")
		}
		databaseChanged := state.Draft.DatabaseMode != databaseMode || state.Draft.DatabaseURL != databaseURL
		redisChanged := state.Draft.RedisMode != redisMode || state.Draft.RedisURL != redisURL
		storagePrefix := strings.Trim(strings.TrimSpace(request.StorageS3Prefix), "/")
		storageChanged := state.Draft.StorageMode != storageMode ||
			state.Draft.StorageS3Endpoint != storageEndpoint ||
			state.Draft.StorageS3Region != storageRegion ||
			state.Draft.StorageS3Bucket != storageBucket ||
			state.Draft.StorageS3AccessKey != storageAccessKey ||
			state.Draft.StorageS3SecretKey != storageSecretKey ||
			state.Draft.StorageS3UseSSL != storageUseSSL ||
			state.Draft.StorageS3PathStyle != storagePathStyle ||
			state.Draft.StorageS3Prefix != storagePrefix
		state.Draft.InstanceName = instanceName
		state.Draft.PublicURL = publicURL
		state.Draft.NetworkMode = networkMode
		state.Draft.Hostname = hostname
		state.Draft.DatabaseMode = databaseMode
		state.Draft.DatabaseURL = databaseURL
		state.Draft.RedisMode = redisMode
		state.Draft.RedisURL = redisURL
		state.Draft.StorageMode = storageMode
		state.Draft.StorageS3Endpoint = storageEndpoint
		state.Draft.StorageS3Region = storageRegion
		state.Draft.StorageS3Bucket = storageBucket
		state.Draft.StorageS3AccessKey = storageAccessKey
		state.Draft.StorageS3SecretKey = storageSecretKey
		state.Draft.StorageS3UseSSL = storageUseSSL
		state.Draft.StorageS3PathStyle = storagePathStyle
		state.Draft.StorageS3Prefix = storagePrefix
		if databaseChanged {
			state.Draft.DatabaseTested = false
		}
		if redisChanged {
			state.Draft.RedisTested = false
		}
		if storageChanged {
			state.Draft.StorageTested = false
		}
		state.SetSecret("database_url", databaseURL)
		state.SetSecret("redis_url", redisURL)
		state.SetSecret("storage_s3_access_key", storageAccessKey)
		state.SetSecret("storage_s3_secret_key", storageSecretKey)
		state.ErrorCode = ""
		state.ErrorMessage = ""
		return nil
	})
	return state, err
}

func (s *Server) startGitHubManifest(w http.ResponseWriter, r *http.Request) {
	if s.githubManifest == nil {
		writeError(w, http.StatusServiceUnavailable, "github_manifest_unavailable", "GitHub App Manifest setup is unavailable")
		return
	}
	callbackURL, err := s.externalURL(r, "/v1/setup/github/manifest/callback")
	if err != nil || !strings.HasPrefix(callbackURL, "https://") {
		writeError(w, http.StatusUnprocessableEntity, "https_required", "open the temporary HTTPS setup URL before connecting GitHub")
		return
	}
	plainState, stateHash, expiresAt, err := setupstate.NewManifestState()
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
	manifest, err := githubauth.ManifestURL(githubauth.AppManifest{
		Name:          "Stealth",
		Description:   "Stealth developer control plane",
		URL:           appURL,
		RedirectURL:   callbackURL,
		Public:        false,
		DefaultEvents: []string{"installation"},
		DefaultPerms:  map[string]string{"contents": "read", "metadata": "read"},
		SetupURL:      setupURL,
	}, plainState)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "github_manifest_invalid", "the setup host cannot be used for GitHub App registration")
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if state.Phase == setupstate.PhaseInstalling || state.Phase == setupstate.PhaseComplete || state.Phase == setupstate.PhaseHandoff {
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
	writeJSON(w, http.StatusOK, setupGitHubManifestResponse{ManifestURL: manifest, ExpiresAt: expiresAt})
}

func (s *Server) setupGitHubManifestCallback(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	providedState := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\x00\r\n") || providedState == "" {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil || state.GitHub.ManifestExpiresAt.Before(time.Now().UTC()) || !compareStateHash(state.GitHub.ManifestStateHash, providedState) {
		http.Redirect(w, r, setupRedirect("/setup?github=error"), http.StatusFound)
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
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
		if state.Phase == setupstate.PhaseInstalling || state.Phase == setupstate.PhaseComplete || state.Phase == setupstate.PhaseHandoff {
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
	if s.cloudflareOAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "cloudflare_oauth_unavailable", "Cloudflare OAuth is not configured; use a scoped API token instead")
		return
	}
	callbackURL, err := s.externalURL(r, "/v1/setup/cloudflare/oauth/callback")
	if err != nil || !strings.HasPrefix(callbackURL, "https://") {
		writeError(w, http.StatusUnprocessableEntity, "https_required", "open the temporary HTTPS setup URL before connecting Cloudflare")
		return
	}
	plainState, hash, expiresAt, err := setupstate.NewManifestState()
	if err != nil {
		internalError(s, w, err)
		return
	}
	authorizationURL, err := s.cloudflareOAuth.AuthorizationURL(callbackURL, plainState, []string{"account:read", "account:write", "zone:read", "zone:dns_records:edit"})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cloudflare_oauth_invalid", "Cloudflare OAuth could not be started")
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if state.Phase == setupstate.PhaseInstalling || state.Phase == setupstate.PhaseComplete || state.Phase == setupstate.PhaseHandoff {
			return errors.New("installation is already in progress or complete")
		}
		state.Cloudflare.Mode = "oauth"
		state.Cloudflare.OAuthStateHash = hash
		state.Cloudflare.OAuthExpiresAt = expiresAt
		return nil
	}); err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", "the Cloudflare setup session could not be started")
		return
	}
	writeJSON(w, http.StatusOK, setupCloudflareOAuthResponse{AuthorizationURL: authorizationURL, ExpiresAt: expiresAt})
}

func (s *Server) setupCloudflareOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	providedState := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\x00\r\n") || providedState == "" || s.cloudflareOAuth == nil {
		http.Redirect(w, r, setupRedirect("/setup?cloudflare=error"), http.StatusFound)
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil || state.Cloudflare.OAuthExpiresAt.Before(time.Now().UTC()) || !compareStateHash(state.Cloudflare.OAuthStateHash, providedState) {
		http.Redirect(w, r, setupRedirect("/setup?cloudflare=error"), http.StatusFound)
		return
	}
	if _, err := s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		if state.Cloudflare.OAuthExpiresAt.Before(time.Now().UTC()) || !compareStateHash(state.Cloudflare.OAuthStateHash, providedState) {
			return errors.New("Cloudflare OAuth state is expired or already used")
		}
		state.Cloudflare.OAuthStateHash = ""
		state.Cloudflare.OAuthExpiresAt = time.Time{}
		return nil
	}); err != nil {
		http.Redirect(w, r, setupRedirect("/setup?cloudflare=error"), http.StatusFound)
		return
	}
	callbackURL, err := s.externalURL(r, "/v1/setup/cloudflare/oauth/callback")
	if err != nil {
		http.Redirect(w, r, setupRedirect("/setup?cloudflare=error"), http.StatusFound)
		return
	}
	token, err := s.cloudflareOAuth.Exchange(r.Context(), code, callbackURL)
	if err != nil || strings.TrimSpace(token.AccessToken) == "" {
		http.Redirect(w, r, setupRedirect("/setup?cloudflare=error"), http.StatusFound)
		return
	}
	_, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		state.Cloudflare.Mode = "oauth"
		state.Cloudflare.Connected = true
		state.Cloudflare.TokenValid = true
		if token.ExpiresIn > 0 {
			state.Cloudflare.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
		}
		state.SetSecret("cloudflare_access_token", token.AccessToken)
		state.SetSecret("cloudflare_refresh_token", token.RefreshToken)
		return nil
	})
	if err != nil {
		http.Redirect(w, r, setupRedirect("/setup?cloudflare=error"), http.StatusFound)
		return
	}
	http.Redirect(w, r, setupRedirect("/setup?cloudflare=connected"), http.StatusFound)
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
	token := strings.TrimSpace(request.APIToken)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "Cloudflare API token is invalid")
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
		state.Cloudflare.Mode = "api_token"
		state.Cloudflare.Connected = true
		state.Cloudflare.TokenValid = true
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
	accountID := strings.TrimSpace(request.AccountID)
	zoneID := strings.TrimSpace(request.ZoneID)
	hostname, err := setupstate.ValidateHostname(request.Hostname)
	if err != nil || accountID == "" || zoneID == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "Cloudflare account, zone, and hostname are required")
		return
	}
	client, err := s.cloudflareClient(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "cloudflare_not_connected", "connect Cloudflare before creating a tunnel")
		return
	}
	zones, err := client.ListZones(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cloudflare_unavailable", "Cloudflare zones could not be verified")
		return
	}
	zoneFound := false
	for _, zone := range zones {
		if zone.ID == zoneID && (hostname == strings.ToLower(zone.Name) || strings.HasSuffix(hostname, "."+strings.ToLower(strings.TrimSuffix(zone.Name, ".")))) {
			zoneFound = true
			break
		}
	}
	if !zoneFound {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "hostname is not inside the selected Cloudflare zone")
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if state.Draft.CloudflareTunnelID != "" && (state.Draft.CloudflareAccountID != accountID || state.Draft.CloudflareZoneID != zoneID || (state.Draft.Hostname != "" && state.Draft.Hostname != hostname)) {
		writeError(w, http.StatusConflict, "cloudflare_tunnel_conflict", "the saved Cloudflare tunnel is bound to a different account, zone, or hostname")
		return
	}
	tunnelID := state.Draft.CloudflareTunnelID
	if tunnelID == "" {
		name := strings.TrimSpace(request.Name)
		if name == "" {
			name = "stealth-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		}
		tunnel, createErr := client.CreateTunnel(r.Context(), accountID, name)
		if createErr != nil {
			writeError(w, http.StatusBadGateway, "cloudflare_tunnel_failed", "Cloudflare could not create the named tunnel")
			return
		}
		ingress := []cloudflare.IngressRule{{Hostname: hostname, Service: "http://proxy:80"}, {Service: "http_status:404"}}
		if err := client.ConfigureTunnel(r.Context(), accountID, tunnel.ID, ingress); err != nil {
			writeError(w, http.StatusBadGateway, "cloudflare_tunnel_failed", "Cloudflare could not configure the named tunnel")
			return
		}
		tunnelToken, tokenErr := client.TunnelToken(r.Context(), accountID, tunnel.ID)
		if tokenErr != nil {
			writeError(w, http.StatusBadGateway, "cloudflare_tunnel_failed", "Cloudflare could not retrieve the named tunnel token")
			return
		}
		state, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
			state.Draft.CloudflareAccountID = accountID
			state.Draft.CloudflareZoneID = zoneID
			state.Draft.CloudflareTunnelID = tunnel.ID
			state.Draft.Hostname = hostname
			state.Draft.PublicURL = "https://" + hostname
			state.SetSecret("cloudflare_tunnel_token", tunnelToken)
			return nil
		})
		if err != nil {
			writeError(w, http.StatusConflict, "setup_state_conflict", "the named tunnel could not be saved")
			return
		}
		tunnelID = tunnel.ID
	}
	if state.Secret("cloudflare_tunnel_token") == "" {
		tunnelToken, tokenErr := client.TunnelToken(r.Context(), accountID, tunnelID)
		if tokenErr != nil {
			writeError(w, http.StatusBadGateway, "cloudflare_tunnel_failed", "Cloudflare could not retrieve the named tunnel token")
			return
		}
		state, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
			state.SetSecret("cloudflare_tunnel_token", tunnelToken)
			return nil
		})
		if err != nil {
			writeError(w, http.StatusConflict, "setup_state_conflict", "the named tunnel token could not be saved")
			return
		}
	}
	if state.Draft.CloudflareRecordID == "" {
		record, recordErr := client.CreateDNSRecord(r.Context(), zoneID, cloudflare.DNSRecord{Type: "CNAME", Name: hostname, Content: tunnelID + ".cfargotunnel.com", Proxied: true, TTL: 1})
		if recordErr != nil {
			writeError(w, http.StatusBadGateway, "cloudflare_dns_failed", "Cloudflare could not create the DNS record")
			return
		}
		state, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
			state.Draft.CloudflareAccountID = accountID
			state.Draft.CloudflareZoneID = zoneID
			state.Draft.CloudflareTunnelID = tunnelID
			state.Draft.CloudflareRecordID = record.ID
			state.Draft.Hostname = hostname
			state.Draft.PublicURL = "https://" + hostname
			state.Draft.NetworkMode = "cloudflare_tunnel"
			state.Cloudflare.Connected = true
			state.Cloudflare.TokenValid = true
			return nil
		})
		if err != nil {
			writeError(w, http.StatusConflict, "setup_state_conflict", "the Cloudflare DNS record could not be saved")
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
	if state.Draft.CloudflareAccountID == "" || state.Draft.CloudflareTunnelID == "" {
		writeError(w, http.StatusConflict, "cloudflare_tunnel_missing", "create the named tunnel before checking status")
		return
	}
	client, err := s.cloudflareClient(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "cloudflare_not_connected", "connect Cloudflare before checking tunnel status")
		return
	}
	status, err := client.TunnelStatus(r.Context(), state.Draft.CloudflareAccountID, state.Draft.CloudflareTunnelID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cloudflare_unavailable", "Cloudflare tunnel status is unavailable")
		return
	}
	_, _ = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		state.Cloudflare.TokenValid = true
		return nil
	})
	writeJSON(w, http.StatusOK, setupCloudflareStatusResponse{TunnelID: state.Draft.CloudflareTunnelID, Status: status.Status, Healthy: cloudflare.StatusIsHealthy(status), Connections: status.Connections})
}

func (s *Server) cloudflareClient(ctx context.Context) (cloudflare.Client, error) {
	if s.cloudflareFactory == nil || s.setupState == nil {
		return nil, errors.New("Cloudflare is not configured")
	}
	state, err := s.setupState.Load(ctx)
	if err != nil {
		return nil, err
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
	databaseURL := strings.TrimSpace(request.URL)
	if databaseURL == "" {
		databaseURL = state.Draft.DatabaseURL
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
		if state.Draft.DatabaseMode != "external" || state.Draft.DatabaseURL != databaseURL {
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
	redisURL := strings.TrimSpace(request.URL)
	if redisURL == "" {
		redisURL = state.Draft.RedisURL
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
		if state.Draft.RedisMode != "external" || state.Draft.RedisURL != redisURL {
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
		if state.Draft.StorageMode != "s3" {
			state.Draft.StorageTested = true
			return nil
		}
		endpoint := valueOr(request.Endpoint, state.Draft.StorageS3Endpoint)
		region := valueOr(request.Region, state.Draft.StorageS3Region)
		bucket := valueOr(request.Bucket, state.Draft.StorageS3Bucket)
		accessKey := valueOr(request.AccessKey, state.Secret("storage_s3_access_key"))
		secretKey := valueOr(request.SecretKey, state.Secret("storage_s3_secret_key"))
		useSSL := state.Draft.StorageS3UseSSL
		if request.UseSSL != nil {
			useSSL = *request.UseSSL
		}
		pathStyle := state.Draft.StorageS3PathStyle
		if request.PathStyle != nil {
			pathStyle = *request.PathStyle
		}
		if endpoint != state.Draft.StorageS3Endpoint || region != state.Draft.StorageS3Region || bucket != state.Draft.StorageS3Bucket || accessKey != state.Secret("storage_s3_access_key") || secretKey != state.Secret("storage_s3_secret_key") || useSSL != state.Draft.StorageS3UseSSL || pathStyle != state.Draft.StorageS3PathStyle {
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
		if state.Phase == setupstate.PhaseComplete {
			return errors.New("setup is already complete")
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
	endpoint := valueOr(request.Endpoint, state.Draft.StorageS3Endpoint)
	region := valueOr(request.Region, state.Draft.StorageS3Region)
	bucket := valueOr(request.Bucket, state.Draft.StorageS3Bucket)
	accessKey := valueOr(request.AccessKey, state.Secret("storage_s3_access_key"))
	secretKey := valueOr(request.SecretKey, state.Secret("storage_s3_secret_key"))
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
		if state.Phase == setupstate.PhaseComplete {
			return errors.New("setup is already complete")
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
	if !s.setupStateReady() || s.setupEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "the setup service is not ready")
		return
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if state.Phase == setupstate.PhaseInstalling {
		writeJSON(w, http.StatusAccepted, setupInstallResponse{Status: "installing", State: state.Public()})
		return
	}
	if state.Phase == setupstate.PhaseComplete || state.Phase == setupstate.PhaseHandoff {
		writeError(w, http.StatusConflict, "setup_complete", "installation has already been handed off")
		return
	}
	if err := validateInstallableSetup(state); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	plan, err := s.buildFinalInstallPlan(state)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "configuration_error", "the reviewed configuration could not be prepared")
		return
	}
	runID := uuid.NewString()
	state, err = s.setupState.Update(r.Context(), func(state *setupstate.State) error {
		state.Phase = setupstate.PhaseInstalling
		state.InstallRunID = runID
		state.Step = installengine.StepNames[installengine.StepConfiguration]
		state.ErrorCode = ""
		state.ErrorMessage = ""
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, "setup_state_conflict", "the installation could not be started")
		return
	}
	go s.runSetupInstall(runID, plan)
	writeJSON(w, http.StatusAccepted, setupInstallResponse{Status: "installing", State: state.Public()})
}

func (s *Server) buildFinalInstallPlan(state setupstate.State) (installengine.Plan, error) {
	layout, err := installengine.NewLayout(s.config.InstallRoot)
	if err != nil {
		return installengine.Plan{}, err
	}
	base, err := installengine.ReadEnvFile(layout.EnvFile)
	if err != nil {
		return installengine.Plan{}, err
	}
	databaseURL := state.Draft.DatabaseURL
	if databaseURL == "" {
		databaseURL = base["DATABASE_URL"]
	}
	redisURL := state.Draft.RedisURL
	if redisURL == "" {
		redisURL = base["REDIS_URL"]
	}
	updates := map[string]string{
		"SETUP_MODE":           "false",
		"GITHUB_APP_CLIENT_ID": state.GitHub.ClientID,
		"PUBLIC_APP_URL":       state.Draft.PublicURL,
		"DATABASE_URL":         databaseURL,
		"REDIS_URL":            redisURL,
		"COOKIE_SECURE":        strconv.FormatBool(strings.HasPrefix(state.Draft.PublicURL, "https://")),
	}
	if state.Draft.NetworkMode == "public_ip" {
		updates["PROXY_HTTP_BIND"] = "0.0.0.0"
	} else {
		updates["PROXY_HTTP_BIND"] = "127.0.0.1"
	}
	if state.Draft.StorageMode != "s3" {
		updates["STORAGE_DRIVER"] = "local"
	} else {
		updates["STORAGE_DRIVER"] = "s3"
		updates["STORAGE_S3_ENDPOINT"] = state.Draft.StorageS3Endpoint
		updates["STORAGE_S3_REGION"] = state.Draft.StorageS3Region
		updates["STORAGE_S3_BUCKET"] = state.Draft.StorageS3Bucket
		updates["STORAGE_S3_ACCESS_KEY"] = state.Secret("storage_s3_access_key")
		updates["STORAGE_S3_SECRET_KEY"] = state.Secret("storage_s3_secret_key")
		updates["STORAGE_S3_USE_SSL"] = strconv.FormatBool(state.Draft.StorageS3UseSSL)
		updates["STORAGE_S3_PATH_STYLE"] = strconv.FormatBool(state.Draft.StorageS3PathStyle)
		updates["STORAGE_S3_PREFIX"] = state.Draft.StorageS3Prefix
	}
	cloudflareEnabled := state.Draft.NetworkMode == "cloudflare_tunnel"
	if cloudflareEnabled {
		if err := installengine.WritePrivateFile(filepath.Join(layout.Root, "state", "cloudflare-tunnel-token"), state.Secret("cloudflare_tunnel_token")+"\n"); err != nil {
			return installengine.Plan{}, err
		}
		updates["CLOUDFLARE_TUNNEL_TOKEN_FILE"] = "./state/cloudflare-tunnel-token"
	}
	contents, err := installengine.MergeEnv(base, updates)
	if err != nil {
		return installengine.Plan{}, err
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, contents); err != nil {
		return installengine.Plan{}, err
	}
	return installengine.Plan{
		Layout:             layout,
		Version:            baseVersion(base),
		PublicURL:          state.Draft.PublicURL,
		GitHubAppClientID:  state.GitHub.ClientID,
		Existing:           true,
		ExternalDatabase:   state.Draft.DatabaseMode == "external",
		ExternalRedis:      state.Draft.RedisMode == "external",
		Cloudflare:         cloudflareEnabled,
		VerifyPublicURL:    cloudflareEnabled || state.Draft.NetworkMode == "public_ip",
		InternalAPIURL:     "http://api:8080",
		InternalConsoleURL: "http://console:3000",
		InternalProxyURL:   "http://proxy:80",
	}, nil
}

func (s *Server) runSetupInstall(runID string, plan installengine.Plan) {
	ctx := context.Background()
	err := s.setupEngine.Install(ctx, plan, func(event installengine.Event) {
		s.publishSetupEvent(ctx, event)
	})
	if err != nil {
		s.setupState.Update(ctx, func(state *setupstate.State) error {
			if state.InstallRunID == runID {
				state.Phase = setupstate.PhaseFailed
				state.ErrorCode = "install_failed"
				state.ErrorMessage = safeSetupError(err)
			}
			return nil
		})
		return
	}
	_, _ = s.setupState.Update(ctx, func(state *setupstate.State) error {
		if state.InstallRunID == runID {
			state.Step = "Cleanup"
		}
		return nil
	})
	cleanupFailed := false
	if state, stateErr := s.setupState.Load(ctx); stateErr == nil {
		if state.QuickTunnel != "" {
			if err := s.removeQuickTunnel(ctx, plan.Layout, state.QuickTunnel); err != nil {
				cleanupFailed = true
				s.logger.Error("temporary setup tunnel cleanup failed", "error", err)
			}
		}
	} else {
		cleanupFailed = true
		s.logger.Error("load setup state before cleanup failed", "error", stateErr)
	}
	// The setup Compose project shares the production data volumes, so only
	// the setup API, Console, and proxy are removed. A cleanup failure leaves
	// the state resumable and keeps the browser informed that production is
	// ready but the temporary control plane still needs attention.
	if s.setupRunner != nil && plan.Layout.SetupComposeFile != "" {
		if err := s.setupRunner.Run(ctx, plan.Layout.Root, io.Discard, io.Discard, "docker", "compose", "--env-file", plan.Layout.EnvFile, "-f", plan.Layout.SetupComposeFile, "rm", "-sf", "setup-console", "setup-proxy"); err != nil {
			cleanupFailed = true
			s.logger.Error("setup Compose cleanup failed", "error", err)
		}
	}
	if cleanupFailed {
		s.publishSetupEvent(ctx, installengine.Event{Step: "Cleanup", Status: "failed", Error: "production is ready, but temporary setup cleanup needs to be retried"})
		return
	}
	// Keep the setup API alive until the production origin consumes the
	// one-time session handoff. This closes the race where a browser loses the
	// final redirect while the setup container is being removed, and gives a
	// refreshed setup browser a short window to rotate its ticket.
	_, _ = s.setupState.Update(ctx, func(state *setupstate.State) error {
		if state.InstallRunID == runID {
			state.Phase = setupstate.PhaseHandoff
			state.Step = "Handoff"
			state.ErrorCode = ""
			state.ErrorMessage = ""
		}
		return nil
	})
	s.publishSetupEvent(ctx, installengine.Event{Step: "Handoff", Status: "succeeded", Message: "production is ready; transferring the Console session"})
	go s.waitForSetupHandoff(runID, plan)
}

type pendingSetupHandoff interface {
	Pending(context.Context) (bool, error)
}

func (s *Server) resumeSetupLifecycle() {
	state, err := s.setupState.Load(context.Background())
	if err != nil {
		s.logger.Warn("setup lifecycle resume could not load state", "error", err)
		return
	}
	switch state.Phase {
	case setupstate.PhaseInstalling:
		if state.InstallRunID == "" {
			s.markSetupResumeFailed("setup installation has no durable run identifier")
			return
		}
		plan, planErr := s.buildFinalInstallPlan(state)
		if planErr != nil {
			s.markSetupResumeFailed(planErr.Error())
			return
		}
		go s.runSetupInstall(state.InstallRunID, plan)
	case setupstate.PhaseHandoff:
		if s.setupHandoff != nil && state.InstallRunID != "" {
			layout, layoutErr := installengine.NewLayout(s.config.InstallRoot)
			if layoutErr != nil {
				s.logger.Warn("setup handoff cleanup cannot resolve installation layout", "error", layoutErr)
				return
			}
			go s.waitForSetupHandoff(state.InstallRunID, installengine.Plan{Layout: layout})
		}
	}
}

func (s *Server) markSetupResumeFailed(message string) {
	message = safeSetupError(errors.New(message))
	_, _ = s.setupState.Update(context.Background(), func(state *setupstate.State) error {
		state.Phase = setupstate.PhaseFailed
		state.ErrorCode = "install_resume_failed"
		state.ErrorMessage = message
		return nil
	})
}

func (s *Server) waitForSetupHandoff(runID string, plan installengine.Plan) {
	ctx := context.Background()
	deadline := time.Now().UTC().Add(bootstrap.CodeLifetime)
	pendingStore, canObserve := s.setupHandoff.(pendingSetupHandoff)
	if !canObserve {
		// The production implementation is a FileStore. Keep injected stores
		// useful in tests and fail closed if a different implementation cannot
		// report consumption: the setup routes remain sealed by the database
		// owner invariant, while the operator can remove the setup project.
		s.logger.Warn("setup handoff store cannot observe consumption")
		s.finalizeSetupHandoff(ctx, runID, plan)
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		pending, err := pendingStore.Pending(ctx)
		if err != nil {
			s.logger.Warn("setup handoff observation failed", "error", err)
		}
		expired := time.Now().UTC().After(deadline)
		if (err == nil && !pending) || expired {
			if expired {
				_ = s.setupHandoff.Discard(ctx)
			}
			s.finalizeSetupHandoff(ctx, runID, plan)
			return
		}
		<-ticker.C
	}
}

func (s *Server) finalizeSetupHandoff(ctx context.Context, runID string, plan installengine.Plan) {
	_, err := s.setupState.Update(ctx, func(state *setupstate.State) error {
		if state.InstallRunID == runID {
			state.Phase = setupstate.PhaseComplete
			state.Step = "Complete"
			state.ErrorCode = ""
			state.ErrorMessage = ""
			state.SetupSessionID = ""
			state.SetupCodeHash = ""
			state.SetupExpiresAt = time.Time{}
		}
		return nil
	})
	if err != nil {
		s.logger.Error("persist completed setup handoff failed", "error", err)
		return
	}
	s.publishSetupEvent(ctx, installengine.Event{Step: "Complete", Status: "succeeded", Message: "production setup is complete"})
	// Persist and publish the terminal state before asking Docker to remove the
	// setup API container that is executing this goroutine. The cleanup command
	// is detached from the state transition so stopping this service cannot
	// leave the browser in an apparently-running state.
	if s.setupRunner != nil && plan.Layout.SetupComposeFile != "" {
		go func() {
			time.Sleep(2 * time.Second)
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := s.setupRunner.Run(cleanupCtx, plan.Layout.Root, io.Discard, io.Discard, "docker", "compose", "--env-file", plan.Layout.EnvFile, "-f", plan.Layout.SetupComposeFile, "rm", "-sf", "setup"); err != nil {
				s.logger.Warn("setup API cleanup after completion failed", "error", err)
			}
		}()
	}
}

func (s *Server) removeQuickTunnel(ctx context.Context, layout installengine.Layout, name string) error {
	if !validQuickTunnelName(name) || s.setupRunner == nil {
		return nil
	}
	containers, err := s.setupRunner.Output(ctx, layout.Root, "docker", "ps", "--all", "--filter", "name=^"+name+"$", "--format", "{{.Names}}")
	if err != nil {
		return err
	}
	for _, candidate := range strings.Split(string(containers), "\n") {
		if strings.TrimSpace(candidate) == name {
			return s.setupRunner.Run(ctx, layout.Root, io.Discard, io.Discard, "docker", "rm", "--force", name)
		}
	}
	return nil
}

func (s *Server) setupInstallEvents(w http.ResponseWriter, r *http.Request) {
	if s.setupState == nil || s.setupEvents == nil {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "setup events are not available")
		return
	}
	channel, unsubscribe := s.setupEvents.subscribe()
	defer unsubscribe()
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
	heartbeat := time.NewTicker(setupEventHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-channel:
			if !ok {
				return
			}
			writeSSE(w, "progress", event.ID, event.Event)
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

func validNetworkMode(value string) bool {
	switch value {
	case "cloudflare_tunnel", "public_ip", "reverse_proxy", "local_only":
		return true
	default:
		return false
	}
}

func validateInstallableSetup(state setupstate.State) error {
	if !state.GitHub.Connected || !githubauth.ValidClientID(state.GitHub.ClientID) {
		return errors.New("connect GitHub before installing Stealth")
	}
	if _, err := setupstate.ValidatePublicURL(state.Draft.PublicURL); err != nil {
		return errors.New("choose a valid public Console URL")
	}
	if !validNetworkMode(state.Draft.NetworkMode) {
		return errors.New("choose a valid networking mode")
	}
	if state.Draft.NetworkMode == "cloudflare_tunnel" {
		if state.Draft.Hostname == "" || state.Draft.CloudflareAccountID == "" || state.Draft.CloudflareZoneID == "" || state.Draft.CloudflareTunnelID == "" || state.Draft.CloudflareRecordID == "" || state.Secret("cloudflare_tunnel_token") == "" {
			return errors.New("finish Cloudflare tunnel and DNS setup before installing")
		}
	}
	if state.Draft.DatabaseMode == "external" {
		if !validDatabaseURL(state.Draft.DatabaseURL) || !state.Draft.DatabaseTested {
			return errors.New("test and save an external PostgreSQL connection before installing")
		}
	}
	if state.Draft.RedisMode == "external" {
		if !validRedisURL(state.Draft.RedisURL) || !state.Draft.RedisTested {
			return errors.New("test and save an external Redis connection before installing")
		}
	}
	if state.Draft.StorageMode == "s3" {
		if !validS3Settings(state.Draft.StorageS3Endpoint, state.Draft.StorageS3Region, state.Draft.StorageS3Bucket, state.Secret("storage_s3_access_key"), state.Secret("storage_s3_secret_key")) || !state.Draft.StorageTested {
			return errors.New("test and save S3-compatible storage before installing")
		}
	}
	return nil
}

func validDatabaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") && parsed.Host != "" && parsed.User != nil && !strings.ContainsAny(raw, "\x00\r\n")
}

func validRedisURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "redis" || parsed.Scheme == "rediss") && parsed.Host != "" && !strings.ContainsAny(raw, "\x00\r\n")
}

func validS3Settings(endpoint, region, bucket, accessKey, secretKey string) bool {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && strings.TrimSpace(region) != "" && len(bucket) >= 3 && len(bucket) <= 63 && strings.TrimSpace(accessKey) != "" && strings.TrimSpace(secretKey) != "" && !strings.ContainsAny(endpoint+region+bucket+accessKey+secretKey, "\x00\r\n")
}

func pingDatabase(ctx context.Context, raw string) error {
	if !validDatabaseURL(raw) {
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
	if !validRedisURL(raw) {
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

func baseVersion(values map[string]string) string {
	if value := strings.TrimSpace(values["STEALTH_API_IMAGE"]); value != "" {
		if index := strings.LastIndexByte(value, ':'); index >= 0 && index+1 < len(value) {
			return value[index+1:]
		}
	}
	return "v0.0.0"
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func safeSetupError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}

// jsonMarshal is kept local to the SSE writer so the event path has no
// dependency on the HTTP JSON response headers or on request-scoped state.
func jsonMarshal(value any) (string, error) {
	contents, err := json.Marshal(value)
	return string(contents), err
}
