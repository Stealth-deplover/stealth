package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/repository"
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
		PublicAppURL:          "http://127.0.0.1:8081",
		CookieSecure:          false,
	}
	handler := NewWithDependencies(configValue, nil, slog.Default(), Dependencies{
		BootstrapStore: bootstrapFake,
		SetupState:     store,
		GitHubManifest: setupManifestFake{},
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
	if len(setupCookie) != 1 || setupCookie[0].Name != setupCookieName || !setupCookie[0].HttpOnly || setupCookie[0].SameSite != http.SameSiteLaxMode || setupCookie[0].Value == setupCode || strings.Contains(verify.Body.String(), setupCode) {
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

	manifestResponse := authenticatedSetupRequest(t, handler, setupCookie[0], http.MethodPost, "https://silent-moon.trycloudflare.com/v1/setup/github/manifest/start", `{}`)
	if manifestResponse.Code != http.StatusOK {
		t.Fatalf("manifest start = %d: %s", manifestResponse.Code, manifestResponse.Body.String())
	}
	var manifestPayload struct {
		ManifestURL string `json:"manifest_url"`
	}
	if err := json.Unmarshal(manifestResponse.Body.Bytes(), &manifestPayload); err != nil {
		t.Fatal(err)
	}
	manifestURL, err := url.Parse(manifestPayload.ManifestURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := httptest.NewRecorder()
	callbackRequest := httptest.NewRequest(http.MethodGet, "https://silent-moon.trycloudflare.com/v1/setup/github/manifest/callback?code=temporary-code&state="+url.QueryEscape(manifestURL.Query().Get("state")), nil)
	callbackRequest.Host = "silent-moon.trycloudflare.com"
	handler.ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusFound || !strings.Contains(callback.Header().Get("Location"), "github=connected") {
		t.Fatalf("manifest callback = %d, headers=%#v", callback.Code, callback.Header())
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
	for _, secret := range []string{"db-password", "redis-password", "github-client-secret", "private-key", "github-webhook-secret", "bootstrap"} {
		if bytes.Contains(public, []byte(secret)) {
			t.Fatalf("public setup state contains secret %q: %s", secret, public)
		}
	}
	if state.Secret("github_client_secret") != "github-client-secret" || state.Secret("github_private_key") == "" {
		t.Fatalf("provider credentials were not persisted in encrypted state: %#v", state)
	}
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
	state.Draft.PublicURL = "http://127.0.0.1:8081"
	state.Draft.NetworkMode = "local_only"
	state.Draft.DatabaseMode = "external"
	state.Draft.DatabaseURL = "postgres://user:password@example.test/stealth"
	state.Draft.RedisMode = "external"
	state.Draft.RedisURL = "redis://:password@example.test/0"

	if err := validateInstallableSetup(state); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("database check error = %v", err)
	}
	state.Draft.DatabaseTested = true
	if err := validateInstallableSetup(state); err == nil || !strings.Contains(err.Error(), "Redis") {
		t.Fatalf("Redis check error = %v", err)
	}
	state.Draft.RedisTested = true
	if err := validateInstallableSetup(state); err != nil {
		t.Fatalf("validateInstallableSetup() after checks: %v", err)
	}
}

var _ githubauth.ManifestClient = setupManifestFake{}
