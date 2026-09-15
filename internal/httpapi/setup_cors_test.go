package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
	"github.com/google/uuid"
)

const (
	setupCORSTestCode       = "STEALTH-ABCD-2345-EFGH"
	setupCORSTestConfigBody = `{"instance_name":"My Stealth","network_mode":"cloudflare_tunnel","hostname":"app.example.test","database_mode":"external","database_url":"postgres://user:pass@db.example.test/stealth","redis_mode":"external","redis_url":"redis://:pass@cache.example.test/0","storage_mode":"local"}`
)

// setupNoopRunner keeps the setup preflight endpoint hermetic: Docker probes
// fail closed but the handler still responds, and no real Docker command runs.
type setupNoopRunner struct{}

func (setupNoopRunner) Run(context.Context, string, io.Writer, io.Writer, string, ...string) error {
	return nil
}

func (setupNoopRunner) Output(context.Context, string, string, ...string) ([]byte, error) {
	return nil, errors.New("probe unavailable in tests")
}

func newSetupCORSTestServer(t *testing.T, publicURL, tunnelURL string) (http.Handler, string) {
	t.Helper()
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
	bootstrapKey := bytes.Repeat([]byte{0x31}, 32)
	bootstrapFake := &setupBootstrapFake{
		bootstrapStoreFake: bootstrapStoreFake{status: repository.BootstrapStatus{SetupRequired: true}},
		verification: repository.BootstrapVerification{
			ID:        uuid.Must(uuid.NewV7()),
			ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
		},
	}
	installRoot := filepath.Join(root, "install")
	handler := NewWithDependencies(config.Config{
		SetupMode:             true,
		FunctionsSecretKey:    functionKey,
		BootstrapCLIKey:       bootstrapKey,
		SetupStateFile:        statePath,
		InstallRoot:           installRoot,
		ProductionComposeFile: filepath.Join(installRoot, "compose.production.yaml"),
		SetupComposeFile:      filepath.Join(installRoot, "compose.setup.yaml"),
		StorageRoot:           filepath.Join(root, "storage"),
		PublicAppURL:          publicURL,
		CookieSecure:          false,
	}, nil, slog.Default(), Dependencies{
		BootstrapStore: bootstrapFake,
		SetupState:     store,
		GitHubManifest: setupManifestFake{},
		GitHubOAuth:    &setupGitHubOAuthFake{},
		InstallRunner:  setupNoopRunner{},
	})
	register := httptest.NewRecorder()
	registerRequest := httptest.NewRequest(http.MethodPost, "/v1/setup/quick-tunnel", strings.NewReader(`{"container_name":"stealth-onboarding-test","url":"`+tunnelURL+`"}`))
	registerRequest.Header.Set("Content-Type", "application/json")
	registerRequest.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(bootstrapKey))
	handler.ServeHTTP(register, registerRequest)
	if register.Code != http.StatusOK {
		t.Fatalf("quick tunnel registration = %d: %s", register.Code, register.Body.String())
	}
	return handler, setupCORSTestCode
}

func setupCORSHandler(setupMode bool) http.Handler {
	server := &Server{config: config.Config{SetupMode: setupMode}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return server.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestSetupCORSAllowsSameExternalOrigin(t *testing.T) {
	handler := setupCORSHandler(true)
	request := httptest.NewRequest(http.MethodPost, "https://real-instance.trycloudflare.com/v1/bootstrap/verify", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://real-instance.trycloudflare.com")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("same-origin setup request = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://real-instance.trycloudflare.com" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
}

func TestSetupCORSRejectsForeignOrigin(t *testing.T) {
	handler := setupCORSHandler(true)
	request := httptest.NewRequest(http.MethodPost, "https://real-instance.trycloudflare.com/v1/bootstrap/verify", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "cors_forbidden") {
		t.Fatalf("foreign origin request = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("foreign origin was granted allow origin %q", got)
	}
}

// TestSetupCORSRejectsOtherQuickTunnelHost proves there is no wildcard
// TryCloudflare trust: only the host that this instance actually serves is the
// same external origin.
func TestSetupCORSRejectsOtherQuickTunnelHost(t *testing.T) {
	handler := setupCORSHandler(true)
	request := httptest.NewRequest(http.MethodPost, "https://real-instance.trycloudflare.com/v1/setup/config", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://attacker.trycloudflare.com")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "cors_forbidden") {
		t.Fatalf("other tunnel host request = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSetupCORSPreflight(t *testing.T) {
	handler := setupCORSHandler(true)
	allowed := httptest.NewRequest(http.MethodOptions, "https://real-instance.trycloudflare.com/v1/bootstrap/verify", nil)
	allowed.Header.Set("Origin", "https://real-instance.trycloudflare.com")
	allowed.Header.Set("Access-Control-Request-Method", http.MethodPost)
	allowed.Header.Set("Access-Control-Request-Headers", "content-type")
	allowedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusNoContent || allowedRecorder.Header().Get("Access-Control-Allow-Origin") != "https://real-instance.trycloudflare.com" {
		t.Fatalf("setup preflight status=%d allow-origin=%q", allowedRecorder.Code, allowedRecorder.Header().Get("Access-Control-Allow-Origin"))
	}

	denied := httptest.NewRequest(http.MethodOptions, "https://real-instance.trycloudflare.com/v1/bootstrap/verify", nil)
	denied.Header.Set("Origin", "https://evil.example")
	denied.Header.Set("Access-Control-Request-Method", http.MethodPost)
	deniedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deniedRecorder, denied)
	if got := deniedRecorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("foreign preflight was granted allow origin %q", got)
	}
}

// TestSetupCORSPreflightAllowsPUTForSetupConfig covers the browser preflight for
// the setup configuration mutation, which uses PUT. Both the advertised allow
// methods and the method gate must include PUT, and a foreign origin must still
// be denied.
func TestSetupCORSPreflightAllowsPUTForSetupConfig(t *testing.T) {
	handler := setupCORSHandler(true)
	allowed := httptest.NewRequest(http.MethodOptions, "https://real-instance.trycloudflare.com/v1/setup/config", nil)
	allowed.Header.Set("Origin", "https://real-instance.trycloudflare.com")
	allowed.Header.Set("Access-Control-Request-Method", http.MethodPut)
	allowed.Header.Set("Access-Control-Request-Headers", "content-type,x-stealth-setup")
	allowedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusNoContent {
		t.Fatalf("setup config PUT preflight = %d, want %d", allowedRecorder.Code, http.StatusNoContent)
	}
	if got := allowedRecorder.Header().Get("Access-Control-Allow-Origin"); got != "https://real-instance.trycloudflare.com" {
		t.Fatalf("setup config PUT allow origin = %q", got)
	}
	if methods := allowedRecorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, http.MethodPut) {
		t.Fatalf("setup config PUT allow methods = %q, want PUT", methods)
	}

	denied := httptest.NewRequest(http.MethodOptions, "https://real-instance.trycloudflare.com/v1/setup/config", nil)
	denied.Header.Set("Origin", "https://evil.example")
	denied.Header.Set("Access-Control-Request-Method", http.MethodPut)
	deniedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deniedRecorder, denied)
	if got := deniedRecorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("foreign setup config PUT preflight was granted allow origin %q", got)
	}
}

// TestSetupAuthorizationAllowsQuickTunnelSameOrigin reproduces the real VPS
// failure end to end: the browser Origin reaches the setup API through the
// tunnel, verification succeeds, the cookie is emitted, a protected GET
// succeeds, and a same-origin mutation passes CSRF.
func TestSetupAuthorizationAllowsQuickTunnelSameOrigin(t *testing.T) {
	handler, code := newSetupCORSTestServer(t, "http://localhost:8081", "https://real-instance.trycloudflare.com")

	before := httptest.NewRecorder()
	beforeRequest := httptest.NewRequest(http.MethodGet, "https://real-instance.trycloudflare.com/v1/setup/cloudflare/accounts", nil)
	beforeRequest.Host = "real-instance.trycloudflare.com"
	handler.ServeHTTP(before, beforeRequest)
	if before.Code != http.StatusUnauthorized {
		t.Fatalf("protected GET before verify = %d, want 401", before.Code)
	}

	verify := httptest.NewRecorder()
	verifyRequest := httptest.NewRequest(http.MethodPost, "https://real-instance.trycloudflare.com/v1/bootstrap/verify", strings.NewReader(`{"setup_code":"`+code+`"}`))
	verifyRequest.Host = "real-instance.trycloudflare.com"
	verifyRequest.Header.Set("Content-Type", "application/json")
	verifyRequest.Header.Set("Origin", "https://real-instance.trycloudflare.com")
	handler.ServeHTTP(verify, verifyRequest)
	if verify.Code != http.StatusOK {
		t.Fatalf("setup verify = %d: %s", verify.Code, verify.Body.String())
	}
	cookies := verify.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != setupCookieName {
		t.Fatalf("setup verify cookies = %#v", cookies)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("tunnel setup cookie attributes = %+v", cookies[0])
	}

	// The protected GET now reaches the handler (authorization succeeded) and
	// returns its own domain error instead of 401 setup_unauthorized.
	after := httptest.NewRecorder()
	afterRequest := httptest.NewRequest(http.MethodGet, "https://real-instance.trycloudflare.com/v1/setup/cloudflare/accounts", nil)
	afterRequest.Host = "real-instance.trycloudflare.com"
	afterRequest.AddCookie(cookies[0])
	handler.ServeHTTP(after, afterRequest)
	if after.Code == http.StatusUnauthorized || !strings.Contains(after.Body.String(), "cloudflare_not_connected") {
		t.Fatalf("protected GET after verify = %d: %s", after.Code, after.Body.String())
	}

	mutation := httptest.NewRecorder()
	mutationRequest := httptest.NewRequest(http.MethodPut, "https://real-instance.trycloudflare.com/v1/setup/config", strings.NewReader(setupCORSTestConfigBody))
	mutationRequest.Host = "real-instance.trycloudflare.com"
	mutationRequest.Header.Set("Content-Type", "application/json")
	mutationRequest.Header.Set("Origin", "https://real-instance.trycloudflare.com")
	mutationRequest.Header.Set(setupCSRFHeader, "1")
	mutationRequest.AddCookie(cookies[0])
	handler.ServeHTTP(mutation, mutationRequest)
	if mutation.Code != http.StatusOK {
		t.Fatalf("setup config mutation = %d: %s", mutation.Code, mutation.Body.String())
	}
}

// TestSetupAuthorizationAllowsLocalhostFallback covers the documented SSH
// forwarding fallback at http://localhost:8081: verification, a protected GET,
// and a same-origin mutation must all work, and the emitted cookie must not be
// Secure so a plain HTTP browser stores it.
func TestSetupAuthorizationAllowsLocalhostFallback(t *testing.T) {
	handler, code := newSetupCORSTestServer(t, "http://localhost:8081", "https://real-instance.trycloudflare.com")

	verify := httptest.NewRecorder()
	verifyRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8081/v1/bootstrap/verify", strings.NewReader(`{"setup_code":"`+code+`"}`))
	verifyRequest.Host = "localhost:8081"
	verifyRequest.Header.Set("Content-Type", "application/json")
	verifyRequest.Header.Set("Origin", "http://localhost:8081")
	handler.ServeHTTP(verify, verifyRequest)
	if verify.Code != http.StatusOK {
		t.Fatalf("localhost verify = %d: %s", verify.Code, verify.Body.String())
	}
	cookies := verify.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != setupCookieName {
		t.Fatalf("localhost setup cookies = %#v", cookies)
	}
	if cookies[0].Secure {
		t.Fatalf("localhost setup cookie must not be Secure over plain HTTP: %+v", cookies[0])
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("localhost setup cookie attributes = %+v", cookies[0])
	}

	protected := httptest.NewRecorder()
	protectedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8081/v1/setup/cloudflare/accounts", nil)
	protectedRequest.Host = "localhost:8081"
	protectedRequest.AddCookie(cookies[0])
	handler.ServeHTTP(protected, protectedRequest)
	if protected.Code == http.StatusUnauthorized || !strings.Contains(protected.Body.String(), "cloudflare_not_connected") {
		t.Fatalf("localhost protected GET after verify = %d: %s", protected.Code, protected.Body.String())
	}

	mutation := httptest.NewRecorder()
	mutationRequest := httptest.NewRequest(http.MethodPut, "http://localhost:8081/v1/setup/config", strings.NewReader(setupCORSTestConfigBody))
	mutationRequest.Host = "localhost:8081"
	mutationRequest.Header.Set("Content-Type", "application/json")
	mutationRequest.Header.Set("Origin", "http://localhost:8081")
	mutationRequest.Header.Set(setupCSRFHeader, "1")
	mutationRequest.AddCookie(cookies[0])
	handler.ServeHTTP(mutation, mutationRequest)
	if mutation.Code != http.StatusOK {
		t.Fatalf("localhost setup mutation = %d: %s", mutation.Code, mutation.Body.String())
	}
}

// TestSetupCORSProductionUnchanged confirms that outside setup mode a
// same-host origin is still rejected unless it is explicitly allowlisted.
func TestSetupCORSProductionUnchanged(t *testing.T) {
	handler := setupCORSHandler(false)
	request := httptest.NewRequest(http.MethodPost, "https://console.example.com/v1/account", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://console.example.com")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "cors_forbidden") {
		t.Fatalf("production unconfigured origin = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSetupCookieSecureFollowsExternalScheme(t *testing.T) {
	functionKey := bytes.Repeat([]byte{0x53}, functionsecret.KeySize)
	cipher, err := functionsecret.New(functionKey)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.Must(uuid.NewV7()).String()
	codeHash := bytes.Repeat([]byte{0x44}, 32)
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	server := &Server{config: config.Config{SetupMode: true}, functionCipher: cipher, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	tunnelRecorder := httptest.NewRecorder()
	tunnelRequest := httptest.NewRequest(http.MethodPost, "https://abc.trycloudflare.com/v1/bootstrap/verify", nil)
	if err := server.setSetupCookie(tunnelRecorder, tunnelRequest, sessionID, codeHash, expiresAt); err != nil {
		t.Fatal(err)
	}
	tunnelCookies := tunnelRecorder.Result().Cookies()
	if len(tunnelCookies) != 1 || !tunnelCookies[0].Secure {
		t.Fatalf("tunnel setup cookie = %#v, want Secure", tunnelCookies)
	}

	localhostRecorder := httptest.NewRecorder()
	localhostRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8081/v1/bootstrap/verify", nil)
	localhostRequest.Host = "localhost:8081"
	if err := server.setSetupCookie(localhostRecorder, localhostRequest, sessionID, codeHash, expiresAt); err != nil {
		t.Fatal(err)
	}
	localhostCookies := localhostRecorder.Result().Cookies()
	if len(localhostCookies) != 1 || localhostCookies[0].Secure {
		t.Fatalf("localhost setup cookie = %#v, want non-Secure", localhostCookies)
	}

	forced := &Server{config: config.Config{SetupMode: true, CookieSecure: true}, functionCipher: cipher, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	forcedRecorder := httptest.NewRecorder()
	forcedRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8081/v1/bootstrap/verify", nil)
	forcedRequest.Host = "localhost:8081"
	if err := forced.setSetupCookie(forcedRecorder, forcedRequest, sessionID, codeHash, expiresAt); err != nil {
		t.Fatal(err)
	}
	forcedCookies := forcedRecorder.Result().Cookies()
	if len(forcedCookies) != 1 || !forcedCookies[0].Secure {
		t.Fatalf("COOKIE_SECURE=true setup cookie = %#v, want Secure", forcedCookies)
	}
}
