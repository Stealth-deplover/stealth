package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

const (
	setupCookieName       = "stealth_setup"
	setupCookieLifetime   = bootstrap.CodeLifetime
	setupCSRFHeader       = "X-Stealth-Setup"
	setupEventBuffer      = 32
	setupEventHeartbeat   = 20 * time.Second
	setupCallbackStateTTL = 10 * time.Minute
)

type setupCookiePayload struct {
	SessionID string    `json:"session_id"`
	CodeHash  string    `json:"code_hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

type setupSession struct {
	ID        string
	CodeHash  []byte
	ExpiresAt time.Time
}

type setupEvent struct {
	ID    uint64
	Event installengine.Event
}

type setupEventHub struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[chan setupEvent]struct{}
}

func newSetupEventHub() *setupEventHub {
	return &setupEventHub{subscribers: make(map[chan setupEvent]struct{})}
}

func (h *setupEventHub) subscribe() (<-chan setupEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers == nil {
		h.subscribers = make(map[chan setupEvent]struct{})
	}
	channel := make(chan setupEvent, setupEventBuffer)
	h.subscribers[channel] = struct{}{}
	return channel, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[channel]; ok {
			delete(h.subscribers, channel)
			close(channel)
		}
		h.mu.Unlock()
	}
}

func (h *setupEventHub) publish(event setupEvent) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if event.ID == 0 {
		h.nextID++
		event.ID = h.nextID
	} else if event.ID > h.nextID {
		h.nextID = event.ID
	}
	for channel := range h.subscribers {
		select {
		case channel <- event:
		default:
			// A reconnect receives the persisted snapshot. Dropping an event
			// here is safer than blocking the installer on a slow browser.
		}
	}
}

func (s *Server) setupStateReady() bool {
	return s != nil && s.config.SetupMode && s.setupState != nil && s.bootstrap != nil && s.functionCipher != nil
}

func (s *Server) setSetupCookie(w http.ResponseWriter, r *http.Request, sessionID string, codeHash []byte, expiresAt time.Time) error {
	if s == nil || s.functionCipher == nil || strings.TrimSpace(sessionID) == "" || len(codeHash) != 32 || expiresAt.Before(time.Now().UTC()) {
		return errors.New("setup cookie cannot be created")
	}
	payload := setupCookiePayload{SessionID: sessionID, CodeHash: base64.RawURLEncoding.EncodeToString(codeHash), ExpiresAt: expiresAt}
	contents, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode setup cookie: %w", err)
	}
	ciphertext, err := s.functionCipher.Encrypt(contents)
	if err != nil {
		return fmt.Errorf("encrypt setup cookie: %w", err)
	}
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		return errors.New("setup cookie has expired")
	}
	http.SetCookie(w, &http.Cookie{
		Name:     setupCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(ciphertext),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.requestIsHTTPS(r) || s.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Expires:  expiresAt,
	})
	return nil
}

func (s *Server) setupSessionFromRequest(r *http.Request) (setupSession, error) {
	if !s.setupStateReady() {
		return setupSession{}, errors.New("setup authentication is unavailable")
	}
	cookie, err := r.Cookie(setupCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return setupSession{}, errors.New("setup authentication is required")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(ciphertext) == 0 {
		return setupSession{}, errors.New("setup authentication is invalid")
	}
	plaintext, err := s.functionCipher.Decrypt(ciphertext)
	if err != nil {
		return setupSession{}, errors.New("setup authentication is invalid")
	}
	var payload setupCookiePayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return setupSession{}, errors.New("setup authentication is invalid")
	}
	if _, err := repository.ParseUUID(payload.SessionID); err != nil || len(payload.CodeHash) > 128 || payload.ExpiresAt.IsZero() || !payload.ExpiresAt.After(time.Now().UTC()) {
		return setupSession{}, errors.New("setup authentication is expired")
	}
	codeHash, err := base64.RawURLEncoding.DecodeString(payload.CodeHash)
	if err != nil || len(codeHash) != 32 {
		return setupSession{}, errors.New("setup authentication is invalid")
	}
	state, err := s.setupState.Load(r.Context())
	if err != nil || state.SetupSessionID != payload.SessionID || subtle.ConstantTimeCompare([]byte(state.SetupCodeHash), []byte(payload.CodeHash)) != 1 || state.Phase == setupstate.PhaseComplete {
		return setupSession{}, errors.New("setup authentication is expired or invalid")
	}
	verification, err := s.bootstrap.VerifyBootstrapCode(r.Context(), codeHash)
	if err == nil {
		if verification.ID.String() != payload.SessionID || verification.ExpiresAt.Before(payload.ExpiresAt) {
			return setupSession{}, errors.New("setup authentication is expired or invalid")
		}
		return setupSession{ID: payload.SessionID, CodeHash: codeHash, ExpiresAt: verification.ExpiresAt}, nil
	}
	// Creating the first owner seals the bootstrap row before the browser has
	// finished the remaining installation steps. The encrypted setup cookie
	// and durable state claim are then the authorization for that same setup
	// session; completion clears the claim and takes the setup routes offline.
	if errors.Is(err, repository.ErrBootstrapSealed) {
		return setupSession{ID: payload.SessionID, CodeHash: codeHash, ExpiresAt: payload.ExpiresAt}, nil
	}
	return setupSession{}, errors.New("setup authentication is expired or invalid")
}

func (s *Server) requireSetup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := s.setupSessionFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "setup_unauthorized", "setup authentication is required")
			return
		}
		ctx := context.WithValue(r.Context(), setupSessionContextKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireSetupMutation(next http.Handler) http.Handler {
	return s.requireSetup(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validSetupCSRF(r) {
			writeError(w, http.StatusForbidden, "csrf_failed", "setup request origin could not be verified")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) validSetupCSRF(r *http.Request) bool {
	if r.Header.Get(setupCSRFHeader) != "1" {
		return false
	}
	for _, header := range []string{"Origin", "Referer"} {
		raw := strings.TrimSpace(r.Header.Get(header))
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User != nil || parsed.Host == "" {
			return false
		}
		expected := s.externalOrigin(r)
		return strings.EqualFold(parsed.Scheme+"://"+parsed.Host, expected)
	}
	return true
}

func (s *Server) setupSession(r *http.Request) (setupSession, bool) {
	session, ok := r.Context().Value(setupSessionContextKey).(setupSession)
	return session, ok
}

func (s *Server) publishSetupEvent(ctx context.Context, event installengine.Event) {
	if s == nil || s.setupEvents == nil {
		return
	}
	id := uint64(0)
	if s.setupState != nil {
		state, err := s.setupState.Update(ctx, func(state *setupstate.State) error {
			state.LastEventID++
			state.Step = event.Step
			if event.Status == "failed" {
				state.Phase = setupstate.PhaseFailed
				state.ErrorCode = "install_failed"
				state.ErrorMessage = event.Error
			}
			return nil
		})
		if err != nil {
			s.logger.Error("persist setup install event failed", "error", err)
		} else {
			id = state.LastEventID
		}
	}
	s.setupEvents.publish(setupEvent{ID: id, Event: event})
}

func (s *Server) requestIsHTTPS(r *http.Request) bool {
	if r != nil && r.TLS != nil {
		return true
	}
	if r == nil {
		return false
	}
	remoteIP, _ := parseRemoteAddr(r.RemoteAddr)
	if remoteIP == nil || !trustedProxyContains(remoteIP, s.config.TrustedProxyCIDRs) {
		return false
	}
	scheme := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	return scheme == "https"
}

func (s *Server) externalOrigin(r *http.Request) string {
	scheme := "http"
	if s.requestIsHTTPS(r) {
		scheme = "https"
	}
	host := ""
	if r != nil {
		host = strings.TrimSpace(r.Host)
	}
	if !validRequestHost(host) {
		return ""
	}
	// cloudflared terminates the public HTTPS connection before forwarding to
	// the setup proxy, so the internal request commonly arrives with an HTTP
	// scheme. The CLI registers the exact Quick Tunnel URL through the local
	// bootstrap proof; use that server-owned value only when the Host matches.
	if s.config.SetupMode && s.setupState != nil {
		if state, err := s.setupState.Load(r.Context()); err == nil {
			if raw := strings.TrimSpace(state.Secret("quick_tunnel_url")); raw != "" {
				if parsed, parseErr := url.Parse(raw); parseErr == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Host, host) {
					scheme = "https"
				}
			}
		}
	}
	return scheme + "://" + host
}

func (s *Server) externalURL(r *http.Request, path string) (string, error) {
	origin := s.externalOrigin(r)
	if origin == "" {
		return "", errors.New("setup host is invalid")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return origin + path, nil
}

func validRequestHost(host string) bool {
	if host == "" || len(host) > 255 || strings.ContainsAny(host, "\x00\r\n/\\?#@ ") {
		return false
	}
	name := host
	if parsedHost, port, err := net.SplitHostPort(host); err == nil {
		name = parsedHost
		if portNumber, err := strconv.Atoi(port); err != nil || portNumber < 1 || portNumber > 65535 {
			return false
		}
	} else if strings.Contains(host, ":") {
		return false
	}
	if name == "" || net.ParseIP(name) != nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func compareStateHash(expected, supplied string) bool {
	expectedBytes, expectedErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(expected))
	suppliedBytes, suppliedErr := base64.RawURLEncoding.DecodeString(setupstate.HashManifestState(strings.TrimSpace(supplied)))
	return expectedErr == nil && suppliedErr == nil && subtle.ConstantTimeCompare(expectedBytes, suppliedBytes) == 1
}

func setupRedirect(path string) string {
	if path == "" || path[0] != '/' {
		return "/setup"
	}
	return path
}

func (s *Server) clearSetupCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: setupCookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.requestIsHTTPS(r) || s.config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}
