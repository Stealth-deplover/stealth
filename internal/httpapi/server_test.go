package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/ratelimit"
)

func TestDecodeJSONRejectsUnsupportedContentType(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"a@example.com"}`))
	request.Header.Set("Content-Type", "text/plain")
	var body struct {
		Email string `json:"email"`
	}
	if decodeJSON(recorder, request, &body) {
		t.Fatal("expected unsupported media type")
	}
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("got %d", recorder.Code)
	}
}

func TestMetricsEndpointUsesRouteTemplates(t *testing.T) {
	secret := bytes.Repeat([]byte("m"), 32)
	handler := NewWithDependencies(config.Config{
		SessionCookieName:          "stealth_session",
		SessionTTL:                 time.Hour,
		AppSessionTTL:              time.Hour,
		StorageRoot:                t.TempDir(),
		StorageMaxFileSize:         1 << 20,
		StorageDefaultQuotaBytes:   2 << 20,
		FunctionsMaxArtifactSize:   1 << 20,
		FunctionsDefaultQuotaBytes: 2 << 20,
		FunctionsSecretKey:         secret,
		MetricsToken:               "metrics-test-token",
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{AuthLimiter: ratelimit.NoopLimiter{}})
	projectID := "018f27e3-5d1a-7c44-ae35-1db4ea12e6d2"
	protected := httptest.NewRecorder()
	handler.ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/v1/projects/"+projectID, nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("protected request status = %d, want 401", protected.Code)
	}

	metrics := httptest.NewRecorder()
	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRequest.Header.Set("X-Metrics-Token", "metrics-test-token")
	handler.ServeHTTP(metrics, metricsRequest)
	body := metrics.Body.String()
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", metrics.Code)
	}
	if !strings.Contains(body, `stealth_api_http_requests_total{method="GET",route="/v1/projects/{projectID}",status="401"} 1`) {
		t.Fatalf("templated route metric missing:\n%s", body)
	}
	if strings.Contains(body, projectID) {
		t.Fatalf("raw project ID leaked into Prometheus output:\n%s", body)
	}
}

func TestProjectOperationRateLimitIsScopedAndReturnsRetryAfter(t *testing.T) {
	server := &Server{
		config:  config.Config{ProjectOperationRateLimit: 1, ProjectOperationRateWindow: time.Minute},
		limiter: ratelimit.NewMemoryLimiter(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	router := chi.NewRouter()
	router.With(server.rateLimitProjectOperation("test_operation")).Post("/v1/projects/{projectID}/expensive", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	projectID := "018f27e3-5d1a-7c44-ae35-1db4ea12e6d2"
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/projects/"+projectID+"/expensive", nil))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first operation status = %d, want 204", first.Code)
	}
	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/projects/"+projectID+"/expensive", nil))
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" || !strings.Contains(second.Body.String(), `"rate_limited"`) {
		t.Fatalf("second operation response = status %d headers=%v body=%q, want 429 with Retry-After and envelope", second.Code, second.Header(), second.Body.String())
	}
}

func TestProjectSessionCookieUsesProjectScopedNameAndPath(t *testing.T) {
	projectID := uuid.MustParse("018f27e3-5d1a-7c44-ae35-1db4ea12e6d2")
	if got, want := projectSessionCookieName(projectID), "stealth_app_018f27e35d1a7c44ae351db4ea12e6d2"; got != want {
		t.Fatalf("cookie name = %q, want %q", got, want)
	}
	if got, want := projectSessionCookiePath(projectID), "/v1/projects/018f27e3-5d1a-7c44-ae35-1db4ea12e6d2"; got != want {
		t.Fatalf("cookie path = %q, want %q", got, want)
	}
}

func TestProjectSessionCookieAttributes(t *testing.T) {
	projectID := uuid.MustParse("018f27e3-5d1a-7c44-ae35-1db4ea12e6d2")
	ttl := 2 * time.Hour
	server := &Server{config: config.Config{AppSessionTTL: ttl, CookieSecure: true}}
	recorder := httptest.NewRecorder()
	server.setProjectSessionCookie(recorder, projectID, "opaque-token")
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want one", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != projectSessionCookieName(projectID) || cookie.Path != projectSessionCookiePath(projectID) || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteNoneMode || cookie.MaxAge != int(ttl.Seconds()) {
		t.Fatalf("unexpected app cookie attributes: %+v", cookie)
	}
}

func TestProjectSessionCookieSameSiteRequiresSecureForCrossOrigin(t *testing.T) {
	if got := projectSessionSameSite(true); got != http.SameSiteNoneMode {
		t.Fatalf("secure cookie SameSite = %v, want None", got)
	}
	if got := projectSessionSameSite(false); got != http.SameSiteLaxMode {
		t.Fatalf("insecure cookie SameSite = %v, want Lax", got)
	}
}

func TestConsoleSessionCookieUsesCrossOriginSafeSameSiteMode(t *testing.T) {
	server := &Server{config: config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour, CookieSecure: true}}
	recorder := httptest.NewRecorder()
	server.setSessionCookie(recorder, "opaque-token")
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want one", len(cookies))
	}
	if cookie := cookies[0]; !cookie.Secure || cookie.SameSite != http.SameSiteNoneMode {
		t.Fatalf("secure console cookie attributes = %+v, want Secure and SameSite=None", cookie)
	}

	localServer := &Server{config: config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}}
	localRecorder := httptest.NewRecorder()
	localServer.setSessionCookie(localRecorder, "opaque-token")
	localCookies := localRecorder.Result().Cookies()
	if len(localCookies) != 1 {
		t.Fatalf("got %d local cookies, want one", len(localCookies))
	}
	if cookie := localCookies[0]; cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("local console cookie attributes = %+v, want insecure and SameSite=Lax", cookie)
	}
}

func TestAllowPublicAuthEnforcesProjectIPBucket(t *testing.T) {
	server := &Server{
		config:  config.Config{AuthRateLimit: 1, AuthRateWindow: time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter: ratelimit.NewMemoryLimiter(),
	}
	projectID := uuid.MustParse("018f27e3-5d1a-7c44-ae35-1db4ea12e6d2")
	first := httptest.NewRequest(http.MethodPost, "/", nil)
	first.RemoteAddr = "198.51.100.3:41000"
	if !server.allowPublicAuth(httptest.NewRecorder(), first, "login", projectID, "one@example.test") {
		t.Fatal("first request was unexpectedly limited")
	}
	secondRecorder := httptest.NewRecorder()
	second := httptest.NewRequest(http.MethodPost, "/", nil)
	second.RemoteAddr = "198.51.100.3:41001"
	if server.allowPublicAuth(secondRecorder, second, "login", projectID, "rotated@example.test") {
		t.Fatal("rotating email bypassed project IP limit")
	}
	if secondRecorder.Code != http.StatusTooManyRequests || secondRecorder.Header().Get("Retry-After") == "" {
		t.Fatalf("got status=%d retry-after=%q", secondRecorder.Code, secondRecorder.Header().Get("Retry-After"))
	}
}

func TestAllowPublicAuthFailsClosedWhenLimiterUnavailable(t *testing.T) {
	server := &Server{
		config:  config.Config{AuthRateLimit: 1, AuthRateWindow: time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter: ratelimit.UnavailableLimiter{},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "198.51.100.3:41000"
	if server.allowPublicAuth(recorder, request, "login", uuid.MustParse("018f27e3-5d1a-7c44-ae35-1db4ea12e6d2"), "person@example.test") {
		t.Fatal("unavailable limiter unexpectedly allowed request")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status=%d, want 503", recorder.Code)
	}
}

func TestAllowAccountAuthEnforcesConsoleIPBucket(t *testing.T) {
	server := &Server{
		config:  config.Config{AuthRateLimit: 1, AuthRateWindow: time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter: ratelimit.NewMemoryLimiter(),
	}
	first := httptest.NewRequest(http.MethodPost, "/", nil)
	first.RemoteAddr = "198.51.100.30:41000"
	if !server.allowAccountAuth(httptest.NewRecorder(), first, "login", "first@example.test") {
		t.Fatal("first account auth request was unexpectedly limited")
	}
	// A different address must not bypass the aggregate bucket when the
	// caller is still the same client IP. The email bucket remains a separate
	// defense, but rotating addresses cannot evade the account namespace.
	secondRecorder := httptest.NewRecorder()
	second := httptest.NewRequest(http.MethodPost, "/", nil)
	second.RemoteAddr = "198.51.100.30:41001"
	if server.allowAccountAuth(secondRecorder, second, "login", "rotated@example.test") {
		t.Fatal("rotating email bypassed Console account IP limit")
	}
	if secondRecorder.Code != http.StatusTooManyRequests || secondRecorder.Header().Get("Retry-After") == "" {
		t.Fatalf("got status=%d retry-after=%q", secondRecorder.Code, secondRecorder.Header().Get("Retry-After"))
	}
}

func TestAllowAccountAuthFailsClosedWhenLimiterUnavailable(t *testing.T) {
	server := &Server{
		config:  config.Config{AuthRateLimit: 1, AuthRateWindow: time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter: ratelimit.UnavailableLimiter{},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "198.51.100.31:41000"
	if server.allowAccountAuth(recorder, request, "registration", "person@example.test") {
		t.Fatal("unavailable limiter unexpectedly allowed Console account auth")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status=%d, want 503", recorder.Code)
	}
}

func TestFailedProjectAPIKeyAuthIsRateLimited(t *testing.T) {
	server := &Server{
		config:  config.Config{AuthRateLimit: 1, AuthRateWindow: time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter: ratelimit.NewMemoryLimiter(),
	}
	projectID := uuid.MustParse("018f27e3-5d1a-7c44-ae35-1db4ea12e6d2")
	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "198.51.100.7:41000"
	if !server.allowFailedProjectAPIKeyAuth(httptest.NewRecorder(), first, projectID) {
		t.Fatal("first failed-auth attempt was unexpectedly blocked")
	}
	secondRecorder := httptest.NewRecorder()
	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "198.51.100.7:41001"
	if server.allowFailedProjectAPIKeyAuth(secondRecorder, second, projectID) {
		t.Fatal("second failed-auth attempt was unexpectedly allowed")
	}
	if secondRecorder.Code != http.StatusTooManyRequests || secondRecorder.Header().Get("Retry-After") == "" {
		t.Fatalf("got status=%d retry-after=%q", secondRecorder.Code, secondRecorder.Header().Get("Retry-After"))
	}
}

func TestFailedProjectAPIKeyAuthFailsClosedWhenLimiterUnavailable(t *testing.T) {
	server := &Server{
		config:  config.Config{AuthRateLimit: 1, AuthRateWindow: time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		limiter: ratelimit.UnavailableLimiter{},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.8:41000"
	if server.allowFailedProjectAPIKeyAuth(recorder, request, uuid.MustParse("018f27e3-5d1a-7c44-ae35-1db4ea12e6d2")) {
		t.Fatal("unavailable limiter unexpectedly allowed failed auth")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status=%d, want 503", recorder.Code)
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"a@example.com","unexpected":true}`))
	request.Header.Set("Content-Type", "application/json")
	var body struct {
		Email string `json:"email"`
	}
	if decodeJSON(recorder, request, &body) {
		t.Fatal("expected unknown field rejection")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("got %d", recorder.Code)
	}
}

func TestDecodeJSONMapsBodyLimitTo413(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"payload":"`+strings.Repeat("a", maxBodyBytes)+`"}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Body = http.MaxBytesReader(recorder, request.Body, maxBodyBytes)
	var body struct {
		Payload string `json:"payload"`
	}
	if decodeJSON(recorder, request, &body) {
		t.Fatal("expected payload too large rejection")
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d", recorder.Code)
	}
}

func TestPageRejectsInvalidCursor(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?cursor=not-a-uuid", nil)
	_, _, ok := page(recorder, request)
	if ok {
		t.Fatal("expected invalid cursor rejection")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("got %d", recorder.Code)
	}
}

func TestPageAcceptsUUIDCursor(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?limit=5&cursor=018f27e3-5d1a-7c44-ae35-1db4ea12e6d2", nil)
	limit, cursor, ok := page(recorder, request)
	if !ok || limit != 5 || cursor == "" {
		t.Fatalf("got limit=%d cursor=%q ok=%t", limit, cursor, ok)
	}
}

func TestPageRejectsInvalidLimit(t *testing.T) {
	for _, value := range []string{"0", "101", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/?limit="+value, nil)
			_, _, ok := page(recorder, request)
			if ok || recorder.Code != http.StatusBadRequest {
				t.Fatalf("got ok=%t status=%d", ok, recorder.Code)
			}
		})
	}
}
