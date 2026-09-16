package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/setupconfig"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
	"github.com/google/uuid"
)

type setupBootstrapFake struct {
	bootstrapStoreFake
	verification repository.BootstrapVerification
}

func (f *setupBootstrapFake) VerifyBootstrapCode(context.Context, []byte) (repository.BootstrapVerification, error) {
	return f.verification, nil
}

type setupManifestFake struct{}

func (setupManifestFake) ConvertManifest(context.Context, string) (githubauth.AppCredentials, error) {
	return githubauth.AppCredentials{
		ID:            42,
		Name:          "stealth",
		ClientID:      "Iv1.setup-client",
		ClientSecret:  "github-client-secret",
		PrivateKey:    "-----BEGIN PRIVATE KEY-----\nprivate-key\n-----END PRIVATE KEY-----",
		WebhookSecret: "github-webhook-secret",
	}, nil
}

type setupGitHubOAuthFake struct {
	user        githubauth.User
	exchangeErr error
	userErr     error
	exchanges   int
}

type setupUpdateProbeStore struct {
	setupstate.Store
	updates chan struct{}
}

func (s *setupUpdateProbeStore) Update(ctx context.Context, mutate func(*setupstate.State) error) (setupstate.State, error) {
	select {
	case s.updates <- struct{}{}:
	default:
	}
	return s.Store.Update(ctx, mutate)
}

type gatedSetupCloudflareClient struct {
	zonesStarted chan struct{}
	releaseZones chan struct{}
	once         sync.Once
}

func (c *gatedSetupCloudflareClient) ListAccounts(context.Context) ([]cloudflare.Account, error) {
	return nil, nil
}

func (c *gatedSetupCloudflareClient) ListZones(ctx context.Context, _ string) ([]cloudflare.Zone, error) {
	c.once.Do(func() { close(c.zonesStarted) })
	select {
	case <-c.releaseZones:
		return []cloudflare.Zone{{ID: "zone-a", Name: "example.test"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*gatedSetupCloudflareClient) ListTunnels(context.Context, string, string) ([]cloudflare.Tunnel, error) {
	return nil, nil
}

func (*gatedSetupCloudflareClient) CreateTunnel(_ context.Context, _, name string) (cloudflare.Tunnel, error) {
	return cloudflare.Tunnel{ID: "tunnel-a", Name: name}, nil
}

func (*gatedSetupCloudflareClient) ConfigureTunnel(context.Context, string, string, []cloudflare.IngressRule) error {
	return nil
}

func (*gatedSetupCloudflareClient) ListDNSRecords(context.Context, string, string) ([]cloudflare.DNSRecord, error) {
	return nil, nil
}

func (*gatedSetupCloudflareClient) CreateDNSRecord(_ context.Context, _ string, record cloudflare.DNSRecord) (cloudflare.DNSRecord, error) {
	record.ID = "record-a"
	return record, nil
}

func (*gatedSetupCloudflareClient) UpdateDNSRecord(_ context.Context, _, recordID string, record cloudflare.DNSRecord) (cloudflare.DNSRecord, error) {
	record.ID = recordID
	return record, nil
}

func (*gatedSetupCloudflareClient) TunnelStatus(context.Context, string, string) (cloudflare.TunnelStatus, error) {
	return cloudflare.TunnelStatus{Status: "healthy"}, nil
}

func (*gatedSetupCloudflareClient) TunnelToken(context.Context, string, string) (string, error) {
	return "tunnel-token", nil
}

func (f *setupGitHubOAuthFake) ExchangeAuthorizationCode(context.Context, string, string, string, string, string) (githubauth.OAuthToken, error) {
	f.exchanges++
	if f.exchangeErr != nil {
		return githubauth.OAuthToken{}, f.exchangeErr
	}
	return githubauth.OAuthToken{AccessToken: "server-only-github-token"}, nil
}

func (f *setupGitHubOAuthFake) GetUser(context.Context, string) (githubauth.User, error) {
	if f.userErr != nil {
		return githubauth.User{}, f.userErr
	}
	return f.user, nil
}

func TestSetupHTTPFlowClaimsCodeAndKeepsProviderSecretsServerSide(t *testing.T) {
	root := t.TempDir()
	functionKey := bytes.Repeat([]byte{0x53}, functionsecret.KeySize)
	cipher, err := functionsecret.New(functionKey)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state", "setup-state.enc")
	store, err := setupstate.NewFileStore(statePath, cipher)
	if err != nil {
		t.Fatal(err)
	}
	verificationID := uuid.Must(uuid.NewV7())
	setupCode := "STEALTH-ABCD-2345-EFGH"
	bootstrapKey := bytes.Repeat([]byte{0x31}, 32)
	bootstrapFake := &setupBootstrapFake{
		bootstrapStoreFake: bootstrapStoreFake{status: repository.BootstrapStatus{SetupRequired: true}},
		verification:       repository.BootstrapVerification{ID: verificationID, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)},
	}
	githubOAuth := &setupGitHubOAuthFake{user: githubauth.User{ID: 424242, Login: "stealth-owner", Email: "owner@example.test", Name: "Stealth Owner", AvatarURL: "https://avatars.githubusercontent.com/u/424242"}}
	installRoot := filepath.Join(root, "install")
	configValue := config.Config{
		SetupMode:             true,
		FunctionsSecretKey:    functionKey,
		BootstrapCLIKey:       bootstrapKey,
		SetupStateFile:        statePath,
		InstallRoot:           installRoot,
		ProductionComposeFile: filepath.Join(installRoot, "compose.production.yaml"),
		SetupComposeFile:      filepath.Join(installRoot, "compose.setup.yaml"),
		StorageRoot:           filepath.Join(root, "storage"),
		PublicAppURL:          "http://localhost:8081",
		CookieSecure:          false,
	}
	handler := NewWithDependencies(configValue, nil, slog.Default(), Dependencies{
		BootstrapStore: bootstrapFake,
		SetupState:     store,
		GitHubManifest: setupManifestFake{},
		GitHubOAuth:    githubOAuth,
	})

	register := httptest.NewRecorder()
	registerRequest := httptest.NewRequest(http.MethodPost, "/v1/setup/quick-tunnel", strings.NewReader(`{"container_name":"stealth-onboarding-test","url":"https://silent-moon.trycloudflare.com"}`))
	registerRequest.Header.Set("Content-Type", "application/json")
	registerRequest.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(bootstrapKey))
	handler.ServeHTTP(register, registerRequest)
	if register.Code != http.StatusOK {
		t.Fatalf("quick tunnel registration = %d: %s", register.Code, register.Body.String())
	}

	verify := httptest.NewRecorder()
	verifyRequest := httptest.NewRequest(http.MethodPost, "https://silent-moon.trycloudflare.com/v1/bootstrap/verify", strings.NewReader(`{"setup_code":"STEALTH-ABCD-2345-EFGH"}`))
	verifyRequest.Host = "silent-moon.trycloudflare.com"
	verifyRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(verify, verifyRequest)
	if verify.Code != http.StatusOK {
		t.Fatalf("bootstrap verification = %d: %s", verify.Code, verify.Body.String())
	}
	setupCookie := verify.Result().Cookies()
	if len(setupCookie) != 1 || setupCookie[0].Name != setupCookieName || !setupCookie[0].HttpOnly || !setupCookie[0].Secure || setupCookie[0].SameSite != http.SameSiteLaxMode || setupCookie[0].Value == setupCode || strings.Contains(verify.Body.String(), setupCode) {
		t.Fatalf("setup verification exposed code or issued an unsafe cookie: cookies=%#v body=%s", setupCookie, verify.Body.String())
	}

	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/verify", strings.NewReader(`{"setup_code":"STEALTH-ABCD-2345-EFGH"}`))
	replayRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusConflict || !strings.Contains(replay.Body.String(), "setup_code_used") {
		t.Fatalf("setup code replay = %d: %s", replay.Code, replay.Body.String())
	}

	configBody := `{"instance_name":"My Stealth","network_mode":"cloudflare_tunnel","hostname":"app.example.test","database_mode":"external","database_url":"postgres://db-user:db-password@example.test/stealth","redis_mode":"external","redis_url":"redis://:redis-password@example.test/0","storage_mode":"local"}`
	configResponse := authenticatedSetupRequest(t, handler, setupCookie[0], http.MethodPut, "https://silent-moon.trycloudflare.com/v1/setup/config", configBody)
	if configResponse.Code != http.StatusOK || strings.Contains(configResponse.Body.String(), "db-password") || strings.Contains(configResponse.Body.String(), "redis-password") {
		t.Fatalf("setup config response = %d: %s", configResponse.Code, configResponse.Body.String())
	}
	s3ConfigResponse := authenticatedSetupRequest(t, handler, setupCookie[0], http.MethodPut, "https://silent-moon.trycloudflare.com/v1/setup/config", `{"instance_name":"My Stealth","network_mode":"cloudflare_tunnel","hostname":"app.example.test","database_mode":"external","redis_mode":"external","storage_mode":"s3","storage_s3_endpoint":"https://s3.example.test","storage_s3_region":"us-east-1","storage_s3_bucket":"stealth","storage_s3_access_key":"s3-access-key","storage_s3_secret_key":"s3-secret-key","storage_s3_use_ssl":true,"storage_s3_path_style":true}`)
	if s3ConfigResponse.Code != http.StatusOK || strings.Contains(s3ConfigResponse.Body.String(), "s3-secret-key") {
		t.Fatalf("S3 setup config response = %d: %s", s3ConfigResponse.Code, s3ConfigResponse.Body.String())
	}

	manifestResponse := authenticatedSetupRequest(t, handler, setupCookie[0], http.MethodPost, "https://silent-moon.trycloudflare.com/v1/setup/github/manifest/start", `{}`)
	if manifestResponse.Code != http.StatusOK {
		t.Fatalf("manifest start = %d: %s", manifestResponse.Code, manifestResponse.Body.String())
	}
	var manifestPayload struct {
		ManifestURL string `json:"manifest_url"`
		Manifest    string `json:"manifest"`
	}
	if err := json.Unmarshal(manifestResponse.Body.Bytes(), &manifestPayload); err != nil {
		t.Fatal(err)
	}
	manifestURL, err := url.Parse(manifestPayload.ManifestURL)
	if err != nil {
		t.Fatal(err)
	}
	if manifestURL.Query().Get("manifest") != "" || manifestURL.Query().Get("state") == "" || manifestPayload.Manifest == "" {
		t.Fatalf("manifest response is not a POST form payload: %#v", manifestPayload)
	}
	var registeredManifest githubauth.AppManifest
	if err := json.Unmarshal([]byte(manifestPayload.Manifest), &registeredManifest); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(registeredManifest.Name, "Stealth Setup ") || len(registeredManifest.DefaultEvents) != 0 || registeredManifest.RedirectURL != "https://silent-moon.trycloudflare.com/v1/setup/github/manifest/callback" {
		t.Fatalf("unexpected GitHub App manifest: %#v", registeredManifest)
	}
	callback := httptest.NewRecorder()
	callbackRequest := httptest.NewRequest(http.MethodGet, "https://silent-moon.trycloudflare.com/v1/setup/github/manifest/callback?code=temporary-code&state="+url.QueryEscape(manifestURL.Query().Get("state")), nil)
	callbackRequest.Host = "silent-moon.trycloudflare.com"
	handler.ServeHTTP(callback, callbackRequest)
	authorizationLocation := callback.Header().Get("Location")
	authorizationURL, err := url.Parse(authorizationLocation)
	if callback.Code != http.StatusFound || authorizationURL.Host != "github.com" || authorizationURL.Path != "/login/oauth/authorize" {
		t.Fatalf("manifest callback = %d, headers=%#v", callback.Code, callback.Header())
	}
	if authorizationURL.Query().Get("client_id") != "Iv1.setup-client" || authorizationURL.Query().Get("redirect_uri") != "https://silent-moon.trycloudflare.com/v1/setup/github/authorize/callback" || authorizationURL.Query().Get("code_challenge_method") != "S256" || authorizationURL.Query().Get("code_challenge") == "" {
		t.Fatalf("manifest callback did not start browser authorization: %s", authorizationLocation)
	}

	authorizeCallback := httptest.NewRecorder()
	authorizeRequest := httptest.NewRequest(http.MethodGet, "https://silent-moon.trycloudflare.com/v1/setup/github/authorize/callback?code=oauth-code&state="+url.QueryEscape(authorizationURL.Query().Get("state")), nil)
	authorizeRequest.Host = "silent-moon.trycloudflare.com"
	handler.ServeHTTP(authorizeCallback, authorizeRequest)
	if authorizeCallback.Code != http.StatusFound || !strings.Contains(authorizeCallback.Header().Get("Location"), "github=connected") {
		t.Fatalf("GitHub authorization callback = %d, headers=%#v", authorizeCallback.Code, authorizeCallback.Header())
	}

	repeatedAuthorizationCallback := httptest.NewRecorder()
	handler.ServeHTTP(repeatedAuthorizationCallback, authorizeRequest)
	if repeatedAuthorizationCallback.Code != http.StatusFound || !strings.Contains(repeatedAuthorizationCallback.Header().Get("Location"), "github=error") || githubOAuth.exchanges != 1 {
		t.Fatalf("GitHub authorization callback replay = %d, headers=%#v exchanges=%d", repeatedAuthorizationCallback.Code, repeatedAuthorizationCallback.Header(), githubOAuth.exchanges)
	}

	repeatedCallback := httptest.NewRecorder()
	handler.ServeHTTP(repeatedCallback, callbackRequest)
	if repeatedCallback.Code != http.StatusFound || !strings.Contains(repeatedCallback.Header().Get("Location"), "github=error") {
		t.Fatalf("manifest callback replay = %d, headers=%#v", repeatedCallback.Code, repeatedCallback.Header())
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(state.Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"db-password", "redis-password", "s3-access-key", "s3-secret-key", "github-client-secret", "private-key", "github-webhook-secret", "bootstrap"} {
		if bytes.Contains(public, []byte(secret)) {
			t.Fatalf("public setup state contains secret %q: %s", secret, public)
		}
	}
	credentials := state.SetupCredentials()
	if credentials.DatabaseURL != "postgres://db-user:db-password@example.test/stealth" || credentials.RedisURL != "redis://:redis-password@example.test/0" || credentials.StorageS3AccessKey != "s3-access-key" || credentials.StorageS3SecretKey != "s3-secret-key" {
		t.Fatalf("setup credentials were not persisted in the encrypted state: %#v", credentials)
	}
	if state.Secret("github_client_secret") != "github-client-secret" || state.Secret("github_private_key") == "" {
		t.Fatalf("provider credentials were not persisted in encrypted state: %#v", state)
	}
	if state.Secret("github_oauth_code_verifier") != "" || state.GitHub.AuthorizationStateHash != "" {
		t.Fatalf("GitHub OAuth verifier/state was not consumed: %#v", state.GitHub)
	}
}

type gatedSetupInstallRunner struct {
	mu        sync.Mutex
	started   chan struct{}
	release   chan struct{}
	done      chan struct{}
	startOnce sync.Once
	doneOnce  sync.Once
	runCalls  int
}

func (r *gatedSetupInstallRunner) Run(ctx context.Context, _ string, _, _ io.Writer, _ string, _ ...string) error {
	r.mu.Lock()
	r.runCalls++
	r.mu.Unlock()
	r.startOnce.Do(func() { close(r.started) })
	select {
	case <-r.release:
		r.doneOnce.Do(func() { close(r.done) })
		return errors.New("test installation stopped")
	case <-ctx.Done():
		r.doneOnce.Do(func() { close(r.done) })
		return ctx.Err()
	}
}

func (*gatedSetupInstallRunner) Output(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

func (r *gatedSetupInstallRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runCalls
}

func TestSetupInstallPersistsRequestAndSuppressesDuplicateWorkers(t *testing.T) {
	root := t.TempDir()
	functionKey := bytes.Repeat([]byte{0x57}, functionsecret.KeySize)
	cipher, err := functionsecret.New(functionKey)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := installengine.NewLayout(filepath.Join(root, "install"))
	if err != nil {
		t.Fatal(err)
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, "STEALTH_API_IMAGE=ghcr.io/stealth-deplover/stealth-api:v1.2.3\n"); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.ComposeFile, []byte("services:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.ProxyFile, []byte("server {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state", "setup-state.enc")
	store, err := setupstate.NewFileStore(statePath, cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Draft.PublicURL = "http://localhost:8081"
	state.Draft.NetworkMode = "local_only"
	state.GitHub.Connected = true
	state.GitHub.ClientID = "Iv1.setup-client"
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	runner := &gatedSetupInstallRunner{started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	server := &Server{
		config:         config.Config{SetupMode: true, InstallRoot: layout.Root},
		bootstrap:      &bootstrapStoreFake{status: repository.BootstrapStatus{SetupRequired: false}},
		functionCipher: cipher,
		setupState:     store,
		setupEngine:    installengine.New(installengine.Options{Runner: runner, PollAttempts: 1}),
		setupRunner:    runner,
		setupEvents:    newSetupEventHub(),
		logger:         slog.Default(),
	}

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPost, "/v1/setup/install", strings.NewReader(`{}`))
	firstRequest.Header.Set("Content-Type", "application/json")
	server.startSetupInstall(first, firstRequest)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first install request = %d: %s", first.Code, first.Body.String())
	}
	var firstResponse setupInstallResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse.Status != "accepted" || firstResponse.State.Phase != setupstate.PhaseInstallRequested {
		t.Fatalf("first install response = %#v", firstResponse)
	}
	if firstResponse.State.Version == 0 {
		t.Fatal("accepted response did not include durable setup state")
	}

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("setup worker did not start")
	}
	state, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseInstalling || state.InstallRunID == "" {
		t.Fatalf("claimed setup state = %#v", state)
	}
	runID := state.InstallRunID

	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodPost, "/v1/setup/install", strings.NewReader(`{}`))
	secondRequest.Header.Set("Content-Type", "application/json")
	server.startSetupInstall(second, secondRequest)
	if second.Code != http.StatusAccepted {
		t.Fatalf("duplicate install request = %d: %s", second.Code, second.Body.String())
	}
	var secondResponse setupInstallResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondResponse); err != nil {
		t.Fatal(err)
	}
	if secondResponse.Status != "installing" || secondResponse.State.Phase != setupstate.PhaseInstalling || runner.calls() != 1 {
		t.Fatalf("duplicate install response = %#v, runner calls = %d", secondResponse, runner.calls())
	}

	close(runner.release)
	select {
	case <-runner.done:
	case <-time.After(time.Second):
		t.Fatal("setup worker did not finish")
	}
	deadline := time.Now().Add(time.Second)
	for {
		state, err = store.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase == setupstate.PhaseFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("setup worker did not publish failure: %#v", state)
		}
		time.Sleep(time.Millisecond)
	}
	if state.InstallRunID != runID || state.ErrorCode != "install_failed" {
		t.Fatalf("failed setup state = %#v", state)
	}
}

func TestSetupInstallWorkerLeavesRunOwnedByLockHolder(t *testing.T) {
	root := t.TempDir()
	functionKey := bytes.Repeat([]byte{0x58}, functionsecret.KeySize)
	cipher, err := functionsecret.New(functionKey)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := installengine.NewLayout(filepath.Join(root, "install"))
	if err != nil {
		t.Fatal(err)
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, "PUBLIC_APP_URL=http://localhost:8080\n"); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.ComposeFile, []byte("services:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.ProxyFile, []byte("server {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(filepath.Join(root, "state", "setup-state.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Phase = setupstate.PhaseInstalling
	state.InstallRunID = "run-1"
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	runner := &setupInstallProbeRunner{}
	server := &Server{
		setupState:  store,
		setupEngine: installengine.New(installengine.Options{Runner: runner}),
		logger:      slog.Default(),
	}
	lock, err := installengine.AcquireProcessLock(layout.StateDir, "install.lock", "installation")
	if err != nil {
		t.Fatal(err)
	}
	server.runSetupInstall("run-1", installengine.Plan{Layout: layout, Existing: true, Version: "v1.2.3"})
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}

	state, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseInstalling || state.InstallRunID != "run-1" {
		t.Fatalf("lock-holder run was overwritten: %#v", state)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("losing worker invoked Docker: %#v", runner.calls)
	}
}

type setupInstallProbeRunner struct {
	calls []string
}

func (r *setupInstallProbeRunner) Run(_ context.Context, _ string, _, _ io.Writer, name string, _ ...string) error {
	r.calls = append(r.calls, name)
	return nil
}

func (r *setupInstallProbeRunner) Output(_ context.Context, _ string, name string, _ ...string) ([]byte, error) {
	r.calls = append(r.calls, name)
	return nil, nil
}

func TestGitHubAuthorizationRejectsInvalidCallbackState(t *testing.T) {
	store, server, request := newGitHubAuthorizationTestServer(t, nil)
	authorizationURL, _, err := server.beginGitHubAuthorization(context.Background(), request, "Iv1.setup-client")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := httptest.NewRecorder()
	callbackRequest := httptest.NewRequest(http.MethodGet, "https://silent-moon.trycloudflare.com/v1/setup/github/authorize/callback?code=oauth-code&state=tampered", nil)
	callbackRequest.Host = "silent-moon.trycloudflare.com"
	server.setupGitHubAuthorizationCallback(callback, callbackRequest)
	if callback.Code != http.StatusFound || !strings.Contains(callback.Header().Get("Location"), "github=error") {
		t.Fatalf("invalid GitHub callback state = %d, headers=%#v", callback.Code, callback.Header())
	}
	if server.githubOAuth.(*setupGitHubOAuthFake).exchanges != 0 {
		t.Fatal("invalid GitHub callback state reached token exchange")
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.GitHub.AuthorizationStateHash == "" || state.Secret("github_oauth_code_verifier") == "" || parsed.Query().Get("state") == "" {
		t.Fatal("invalid callback consumed the valid authorization state")
	}
}

func TestGitHubAuthorizationFailureConsumesStateWithoutLeakingProviderData(t *testing.T) {
	store, server, request := newGitHubAuthorizationTestServer(t, errors.New("provider rejected oauth-code"))
	authorizationURL, _, err := server.beginGitHubAuthorization(context.Background(), request, "Iv1.setup-client")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := httptest.NewRecorder()
	callbackRequest := httptest.NewRequest(http.MethodGet, "https://silent-moon.trycloudflare.com/v1/setup/github/authorize/callback?code=oauth-code&state="+url.QueryEscape(parsed.Query().Get("state")), nil)
	callbackRequest.Host = "silent-moon.trycloudflare.com"
	server.setupGitHubAuthorizationCallback(callback, callbackRequest)
	if callback.Code != http.StatusFound || !strings.Contains(callback.Header().Get("Location"), "github=error") || strings.Contains(callback.Header().Get("Location"), "oauth-code") {
		t.Fatalf("GitHub authorization failure = %d, headers=%#v", callback.Code, callback.Header())
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.GitHub.AuthorizationStateHash != "" || state.Secret("github_oauth_code_verifier") != "" {
		t.Fatalf("failed authorization did not consume one-time state: %#v", state.GitHub)
	}
}

func TestGitHubAuthorizationIdentityFailureConsumesState(t *testing.T) {
	store, server, request := newGitHubAuthorizationTestServer(t, nil)
	server.githubOAuth.(*setupGitHubOAuthFake).userErr = errors.New("GitHub user lookup failed")
	authorizationURL, _, err := server.beginGitHubAuthorization(context.Background(), request, "Iv1.setup-client")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := httptest.NewRecorder()
	callbackRequest := httptest.NewRequest(http.MethodGet, "https://silent-moon.trycloudflare.com/v1/setup/github/authorize/callback?code=oauth-code&state="+url.QueryEscape(parsed.Query().Get("state")), nil)
	callbackRequest.Host = "silent-moon.trycloudflare.com"
	server.setupGitHubAuthorizationCallback(callback, callbackRequest)
	if callback.Code != http.StatusFound || !strings.Contains(callback.Header().Get("Location"), "github=error") {
		t.Fatalf("GitHub identity failure = %d, headers=%#v", callback.Code, callback.Header())
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.GitHub.AuthorizationStateHash != "" || state.Secret("github_oauth_code_verifier") != "" {
		t.Fatalf("identity failure did not consume one-time state: %#v", state.GitHub)
	}
}

func TestSetupModeDoesNotStartGitHubDeviceFlow(t *testing.T) {
	server := &Server{config: config.Config{SetupMode: true}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/github/device", strings.NewReader(`{"authorization_session_id":"00000000-0000-0000-0000-000000000000","setup_code":"STEALTH-ABCD-2345-EFGH"}`))
	request.Header.Set("Content-Type", "application/json")
	server.startGitHubDeviceFlow(recorder, request)
	if recorder.Code != http.StatusGone || !strings.Contains(recorder.Body.String(), "github_device_flow_inactive") {
		t.Fatalf("setup-mode Device Flow response = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func newGitHubAuthorizationTestServer(t *testing.T, exchangeErr error) (setupstate.Store, *Server, *http.Request) {
	t.Helper()
	root := t.TempDir()
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x68}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(filepath.Join(root, "state", "setup-state.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.SetupSessionID = uuid.Must(uuid.NewV7()).String()
	state.SetupCodeHash = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32))
	state.SetupExpiresAt = time.Now().UTC().Add(15 * time.Minute)
	state.GitHub.Connected = true
	state.GitHub.ClientID = "Iv1.setup-client"
	state.SetSecret("github_client_secret", "github-client-secret")
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	server := &Server{config: config.Config{SetupMode: true}, setupState: store, githubOAuth: &setupGitHubOAuthFake{exchangeErr: exchangeErr}, logger: slog.Default()}
	request := httptest.NewRequest(http.MethodPost, "https://silent-moon.trycloudflare.com/v1/setup/github/authorize/start", nil)
	request.Host = "silent-moon.trycloudflare.com"
	return store, server, request
}

func authenticatedSetupRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Host = "silent-moon.trycloudflare.com"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(setupCSRFHeader, "1")
	request.AddCookie(cookie)
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestCloudflareOAuthIsExplicitlyInactive(t *testing.T) {
	server := &Server{}
	start := httptest.NewRecorder()
	server.startCloudflareOAuth(start, httptest.NewRequest(http.MethodPost, "/v1/setup/cloudflare/oauth/start", nil))
	if start.Code != http.StatusGone || !strings.Contains(start.Body.String(), "cloudflare_oauth_inactive") || strings.Contains(start.Body.String(), "authorization_url") {
		t.Fatalf("inactive OAuth start = %d: %s", start.Code, start.Body.String())
	}

	callback := httptest.NewRecorder()
	server.setupCloudflareOAuthCallback(callback, httptest.NewRequest(http.MethodGet, "/v1/setup/cloudflare/oauth/callback?code=secret-code&state=secret-state", nil))
	if callback.Code != http.StatusFound || !strings.Contains(callback.Header().Get("Location"), "cloudflare=oauth-inactive") {
		t.Fatalf("inactive OAuth callback = %d, headers=%#v", callback.Code, callback.Header())
	}
}

func TestSetupConfigRejectsCloudflareBindingChange(t *testing.T) {
	root := t.TempDir()
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x64}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(filepath.Join(root, "state", "setup-state.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Draft.Hostname = "stealth.old.example.test"
	state.Draft.PublicURL = "https://stealth.old.example.test"
	state.Draft.NetworkMode = "cloudflare_tunnel"
	state.Cloudflare.Binding = setupstate.CloudflareBinding{
		AccountID: "account-a", ZoneID: "zone-a", Hostname: "stealth.old.example.test",
		TunnelName: "stealth-prod", TunnelID: "tunnel-a", RecordID: "record-a",
	}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	server := &Server{setupState: store}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/v1/setup/config", strings.NewReader(`{"instance_name":"Stealth","public_url":"https://stealth.new.example.test","network_mode":"cloudflare_tunnel","hostname":"stealth.new.example.test","database_mode":"bundled","redis_mode":"bundled","storage_mode":"local"}`))
	request.Header.Set("Content-Type", "application/json")
	server.saveSetupConfig(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "cloudflare_tunnel_reconfiguration_required") || !strings.Contains(recorder.Body.String(), "stealth.old.example.test") {
		t.Fatalf("stale binding config response = %d: %s", recorder.Code, recorder.Body.String())
	}
	state, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Draft.Hostname != "stealth.old.example.test" || state.Cloudflare.Binding.TunnelID != "tunnel-a" {
		t.Fatalf("rejected config changed state: draft=%#v binding=%#v", state.Draft, state.Cloudflare.Binding)
	}
}

func TestCloudflareProvisioningSerializesSetupConfigMutation(t *testing.T) {
	root := t.TempDir()
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x66}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	baseStore, err := setupstate.NewFileStore(filepath.Join(root, "state", "setup-state.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Cloudflare.Mode = "api_token"
	state.Cloudflare.Connected = true
	state.Cloudflare.TokenValid = true
	state.SetSecret("cloudflare_access_token", "scoped-token")
	if err := baseStore.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	store := &setupUpdateProbeStore{Store: baseStore, updates: make(chan struct{}, 16)}
	client := &gatedSetupCloudflareClient{zonesStarted: make(chan struct{}), releaseZones: make(chan struct{})}
	server := &Server{
		setupState: store,
		logger:     slog.Default(),
		cloudflareFactory: func(string) (cloudflare.Client, error) {
			return client, nil
		},
	}

	provisionDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/setup/cloudflare/tunnel", strings.NewReader(`{"account_id":"account-a","zone_id":"zone-a","hostname":"stealth.old.example.test","name":"stealth-prod"}`))
		request.Header.Set("Content-Type", "application/json")
		server.createCloudflareTunnel(recorder, request)
		provisionDone <- recorder
	}()
	select {
	case <-client.zonesStarted:
	case <-time.After(time.Second):
		t.Fatal("Cloudflare provisioning did not reach the gated zone lookup")
	}

	configDone := make(chan *httptest.ResponseRecorder, 1)
	configStarted := make(chan struct{})
	go func() {
		close(configStarted)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/v1/setup/config", strings.NewReader(`{"instance_name":"Stealth","public_url":"https://stealth.new.example.test","network_mode":"cloudflare_tunnel","hostname":"stealth.new.example.test","database_mode":"bundled","redis_mode":"bundled","storage_mode":"local"}`))
		request.Header.Set("Content-Type", "application/json")
		server.saveSetupConfig(recorder, request)
		configDone <- recorder
	}()
	<-configStarted
	select {
	case <-store.updates:
		t.Fatal("setup configuration reached the store while Cloudflare provisioning was in progress")
	case <-time.After(100 * time.Millisecond):
	}
	close(client.releaseZones)

	provisionResponse := <-provisionDone
	configResponse := <-configDone
	if provisionResponse.Code != http.StatusOK {
		t.Fatalf("Cloudflare provisioning = %d: %s", provisionResponse.Code, provisionResponse.Body.String())
	}
	if configResponse.Code != http.StatusConflict || !strings.Contains(configResponse.Body.String(), "cloudflare_tunnel_reconfiguration_required") {
		t.Fatalf("concurrent setup configuration = %d: %s", configResponse.Code, configResponse.Body.String())
	}
	state, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Draft.Hostname != "stealth.old.example.test" || state.Cloudflare.Binding.Hostname != "stealth.old.example.test" || state.Cloudflare.Binding.TunnelID != "tunnel-a" {
		t.Fatalf("concurrent mutation left inconsistent state: draft=%#v binding=%#v", state.Draft, state.Cloudflare.Binding)
	}
}

func TestSaveCloudflareTokenKeepsTokenOutOfPublicState(t *testing.T) {
	root := t.TempDir()
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x63}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(filepath.Join(root, "state", "setup-state.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts" || r.Header.Get("Authorization") != "Bearer scoped-token" {
			t.Fatalf("unexpected Cloudflare verification request: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"result":[{"id":"account-1","name":"Acme"}]}`)
	}))
	defer provider.Close()
	server := &Server{
		setupState: store,
		cloudflareFactory: func(token string) (cloudflare.Client, error) {
			return cloudflare.NewClient(token, provider.URL, provider.Client())
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/setup/cloudflare/token", strings.NewReader(`{"api_token":"scoped-token"}`))
	request.Header.Set("Content-Type", "application/json")
	server.saveCloudflareToken(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "scoped-token") {
		t.Fatalf("Cloudflare token response = %d: %s", recorder.Code, recorder.Body.String())
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Cloudflare.Mode != "api_token" || state.Secret("cloudflare_access_token") != "scoped-token" {
		t.Fatalf("Cloudflare token was not stored in encrypted state: %#v", state.Cloudflare)
	}
}

func TestSetupCookiePayloadIsOpaqueAndBoundToClaim(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x61}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{config: config.Config{SetupMode: true}, functionCipher: cipher}
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "https://setup.example.test/setup", nil)
	request.Host = "setup.example.test"
	id := uuid.Must(uuid.NewV7()).String()
	hash := bytes.Repeat([]byte{0x22}, 32)
	if err := server.setSetupCookie(writer, request, id, hash, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	cookie := writer.Result().Cookies()[0]
	if strings.Contains(cookie.Value, id) || strings.Contains(cookie.Value, base64.RawURLEncoding.EncodeToString(hash)) {
		t.Fatalf("cookie contains plaintext setup binding: %q", cookie.Value)
	}
}

func TestSetupRecoveryRequiresCLIProofAndPersistsFreshClaim(t *testing.T) {
	root := t.TempDir()
	functionKey := bytes.Repeat([]byte{0x72}, functionsecret.KeySize)
	cipher, err := functionsecret.New(functionKey)
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(filepath.Join(root, "state", "setup-state.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	cliKey := bytes.Repeat([]byte{0x41}, 32)
	server := &Server{
		config:         config.Config{SetupMode: true, BootstrapCLIKey: cliKey},
		bootstrap:      &setupBootstrapFake{bootstrapStoreFake: bootstrapStoreFake{status: repository.BootstrapStatus{SetupRequired: false}}},
		functionCipher: cipher,
		setupState:     store,
		logger:         slog.Default(),
	}

	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/v1/setup/recovery", nil)
	server.recoverSetupSession(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("recovery without CLI proof = %d: %s", unauthorized.Code, unauthorized.Body.String())
	}

	recovered := httptest.NewRecorder()
	recoveryRequest := httptest.NewRequest(http.MethodPost, "/v1/setup/recovery", nil)
	recoveryRequest.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(cliKey))
	server.recoverSetupSession(recovered, recoveryRequest)
	if recovered.Code != http.StatusCreated {
		t.Fatalf("recovery with CLI proof = %d: %s", recovered.Code, recovered.Body.String())
	}
	var payload bootstrapSessionResponse
	if err := json.Unmarshal(recovered.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !bootstrap.ValidCode(payload.SetupCode) || !payload.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("recovery returned invalid session: %#v", payload)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(state.SetupSessionID); err != nil || state.SetupCodeHash == "" || !state.SetupExpiresAt.Equal(payload.ExpiresAt) {
		t.Fatalf("recovery claim was not persisted: %#v", state)
	}
	expectedHash := base64.RawURLEncoding.EncodeToString(bootstrap.HashCode(payload.SetupCode))
	if state.SetupCodeHash != expectedHash {
		t.Fatalf("recovery claim hash = %q, want %q", state.SetupCodeHash, expectedHash)
	}
}

func TestValidateInstallableSetupRequiresSavedExternalChecks(t *testing.T) {
	state := setupstate.NewState()
	state.GitHub.Connected = true
	state.GitHub.ClientID = "Iv1.setup-client"
	state.Draft.PublicURL = "http://localhost:8081"
	state.Draft.NetworkMode = "local_only"
	state.Draft.DatabaseMode = "external"
	state.Draft.RedisMode = "external"
	state.SetSetupCredentials(setupstate.SetupCredentials{
		DatabaseURL: "postgres://user:password@example.test/stealth",
		RedisURL:    "redis://:password@example.test/0",
	})

	if err := setupconfig.ValidateInstallableSetup(state); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("database check error = %v", err)
	}
	state.Draft.DatabaseTested = true
	if err := setupconfig.ValidateInstallableSetup(state); err == nil || !strings.Contains(err.Error(), "Redis") {
		t.Fatalf("Redis check error = %v", err)
	}
	state.Draft.RedisTested = true
	if err := setupconfig.ValidateInstallableSetup(state); err != nil {
		t.Fatalf("ValidateInstallableSetup() after checks: %v", err)
	}
}

func TestSetupRedirectRejectsUnsafeTargets(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: "/setup"},
		{name: "relative without slash", raw: "setup", want: "/setup"},
		{name: "absolute URL", raw: "https://attacker.example", want: "/setup"},
		{name: "protocol relative URL", raw: "//attacker.example", want: "/setup"},
		{name: "backslash URL", raw: "/\\attacker.example", want: "/setup"},
		{name: "CRLF injection", raw: "/setup\r\nLocation: https://attacker.example", want: "/setup"},
		{name: "safe setup status", raw: "/setup?github=connected", want: "/setup?github=connected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := setupRedirect(test.raw); got != test.want {
				t.Fatalf("setupRedirect(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

var _ githubauth.ManifestClient = setupManifestFake{}
