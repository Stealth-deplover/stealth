package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/apikey"
	"github.com/nazxf/stealth-api/internal/auth"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/ratelimit"
	"github.com/nazxf/stealth-api/internal/repository"
)

func (s *Server) requireProjectManagement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID, err := repository.ParseUUID(chi.URLParam(r, "projectID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "projectID must be a UUID")
			return
		}
		// An explicit server key takes precedence over any ambient Console
		// cookie, so its project binding and scopes can never be bypassed by a
		// browser session. The application-user cookie is intentionally ignored.
		secret := r.Header.Get("X-Stealth-Key")
		if secret != "" {
			if err := apikey.ValidateSecret(secret); err != nil {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			key, err := s.repo.AuthenticateProjectAPIKey(r.Context(), projectID, apikey.HashSecret(secret))
			if errors.Is(err, repository.ErrNotFound) {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
			keyID, err := repository.ParseUUID(key.ID)
			if err != nil {
				internalError(s, w, err)
				return
			}
			if err := s.repo.TouchProjectAPIKey(r.Context(), keyID); err != nil {
				internalError(s, w, err)
				return
			}
			ctx := context.WithValue(r.Context(), projectActorContextKey, projectActor{kind: apiKeyProjectActor, apiKeyID: keyID, scopes: key.Scopes})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if cookie, err := r.Cookie(s.config.SessionCookieName); err == nil && cookie.Value != "" {
			account, sessionID, err := s.repo.AccountBySession(r.Context(), auth.HashSessionToken(cookie.Value))
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			ctx := context.WithValue(r.Context(), accountContextKey, account)
			ctx = context.WithValue(ctx, sessionContextKey, sessionID)
			ctx = context.WithValue(ctx, projectActorContextKey, projectActor{kind: consoleProjectActor})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
	})
}

// requireFunctionExecutionActor accepts the three actors that may invoke a
// function: a Console/API-key management actor, an authenticated project
// user, or an anonymous caller when the function grants "any". Credentials
// are explicit and ordered; a malformed credential never falls through to a
// weaker actor.
func (s *Server) requireFunctionExecutionActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID, err := repository.ParseUUID(chi.URLParam(r, "projectID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "projectID must be a UUID")
			return
		}
		if secret := r.Header.Get("X-Stealth-Key"); secret != "" {
			if err := apikey.ValidateSecret(secret); err != nil {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			key, err := s.repo.AuthenticateProjectAPIKey(r.Context(), projectID, apikey.HashSecret(secret))
			if errors.Is(err, repository.ErrNotFound) {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
			keyID, err := repository.ParseUUID(key.ID)
			if err != nil {
				internalError(s, w, err)
				return
			}
			if err := s.repo.TouchProjectAPIKey(r.Context(), keyID); err != nil {
				internalError(s, w, err)
				return
			}
			ctx := context.WithValue(r.Context(), projectActorContextKey, projectActor{kind: apiKeyProjectActor, apiKeyID: keyID, scopes: key.Scopes})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if cookie, err := r.Cookie(projectSessionCookieName(projectID)); err == nil && cookie.Value != "" {
			user, _, err := s.repo.ApplicationUserBySession(r.Context(), projectID, auth.HashSessionToken(cookie.Value))
			if errors.Is(err, repository.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "application authentication is required")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
			ctx := context.WithValue(r.Context(), projectUserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if cookie, err := r.Cookie(s.config.SessionCookieName); err == nil && cookie.Value != "" {
			account, sessionID, err := s.repo.AccountBySession(r.Context(), auth.HashSessionToken(cookie.Value))
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			ctx := context.WithValue(r.Context(), accountContextKey, account)
			ctx = context.WithValue(ctx, sessionContextKey, sessionID)
			ctx = context.WithValue(ctx, projectActorContextKey, projectActor{kind: consoleProjectActor})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// No cookie is an intentional anonymous application invocation. The
		// repository checks execute_permissions before accepting it.
		next.ServeHTTP(w, r)
	})
}

func (s *Server) allowFailedProjectAPIKeyAuth(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) bool {
	decision, err := s.limiter.Allow(r.Context(), ratelimit.ProjectIPKey("api_key_auth", projectID.String(), requestClientIP(r)), s.config.AuthRateLimit, s.config.AuthRateWindow)
	if err != nil {
		s.logger.Error("API key rate limiter failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication protection is temporarily unavailable")
		return false
	}
	if !decision.Allowed {
		return writeRateLimited(w, decision.RetryAfter)
	}
	return true
}

func projectActorFrom(r *http.Request) projectActor {
	return r.Context().Value(projectActorContextKey).(projectActor)
}

func (s *Server) requireProjectAppSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID, err := repository.ParseUUID(chi.URLParam(r, "projectID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "projectID must be a UUID")
			return
		}
		cookie, err := r.Cookie(projectSessionCookieName(projectID))
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "application authentication is required")
			return
		}
		item, sessionID, err := s.repo.ApplicationUserBySession(r.Context(), projectID, auth.HashSessionToken(cookie.Value))
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "application authentication is required")
			return
		}
		if err != nil {
			internalError(s, w, err)
			return
		}
		ctx := context.WithValue(r.Context(), projectUserContextKey, item)
		ctx = context.WithValue(ctx, projectUserSessionContextKey, sessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func projectUserFrom(r *http.Request) domain.ApplicationUser {
	return r.Context().Value(projectUserContextKey).(domain.ApplicationUser)
}

func projectUserSessionFrom(r *http.Request) uuid.UUID {
	return r.Context().Value(projectUserSessionContextKey).(uuid.UUID)
}

func projectSessionCookieName(projectID uuid.UUID) string {
	return "stealth_app_" + strings.ReplaceAll(projectID.String(), "-", "")
}

func projectSessionCookiePath(projectID uuid.UUID) string {
	return "/v1/projects/" + projectID.String()
}

func (s *Server) setProjectSessionCookie(w http.ResponseWriter, projectID uuid.UUID, token string) {
	expires := time.Now().UTC().Add(s.config.AppSessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     projectSessionCookieName(projectID),
		Value:    token,
		Path:     projectSessionCookiePath(projectID),
		HttpOnly: true,
		Secure:   s.config.CookieSecure,
		SameSite: projectSessionSameSite(s.config.CookieSecure),
		MaxAge:   int(s.config.AppSessionTTL.Seconds()),
		Expires:  expires,
	})
}

func (s *Server) clearProjectSessionCookie(w http.ResponseWriter, projectID uuid.UUID) {
	http.SetCookie(w, &http.Cookie{
		Name:     projectSessionCookieName(projectID),
		Value:    "",
		Path:     projectSessionCookiePath(projectID),
		HttpOnly: true,
		Secure:   s.config.CookieSecure,
		SameSite: projectSessionSameSite(s.config.CookieSecure),
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

// Cross-origin browser clients need the project session cookie on credentialed
// requests. SameSite=None is only emitted alongside Secure; local HTTP
// development keeps Lax so browsers do not discard the cookie outright.
func projectSessionSameSite(secure bool) http.SameSite {
	if secure {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func (s *Server) allowPublicAuth(w http.ResponseWriter, r *http.Request, operation string, projectID uuid.UUID, normalizedEmail string) bool {
	clientIP := requestClientIP(r)
	keys := []string{
		ratelimit.ProjectIPKey(operation, projectID.String(), clientIP),
		ratelimit.Key(operation, projectID.String(), normalizedEmail, clientIP),
	}
	for _, key := range keys {
		decision, err := s.limiter.Allow(r.Context(), key, s.config.AuthRateLimit, s.config.AuthRateWindow)
		if err != nil {
			s.logger.Error("public auth rate limiter failed", "operation", operation, "error", err)
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication protection is temporarily unavailable")
			return false
		}
		if decision.Allowed {
			continue
		}
		return writeRateLimited(w, decision.RetryAfter)
	}
	return true
}

// allowAccountAuth applies the same two-dimensional protection used by
// project Auth to the Console's public recovery/verification endpoints. The
// literal namespace keeps account addresses and project addresses separate in
// Redis without putting raw PII in keys.
func (s *Server) allowAccountAuth(w http.ResponseWriter, r *http.Request, operation, normalizedEmail string) bool {
	clientIP := requestClientIP(r)
	keys := []string{
		ratelimit.ProjectIPKey(operation, "console", clientIP),
		ratelimit.Key(operation, "console", normalizedEmail, clientIP),
	}
	for _, key := range keys {
		decision, err := s.limiter.Allow(r.Context(), key, s.config.AuthRateLimit, s.config.AuthRateWindow)
		if err != nil {
			s.logger.Error("account auth rate limiter failed", "operation", operation, "error", err)
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication protection is temporarily unavailable")
			return false
		}
		if decision.Allowed {
			continue
		}
		return writeRateLimited(w, decision.RetryAfter)
	}
	return true
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) bool {
	retryAfterSeconds := int(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		retryAfterSeconds++
	}
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many authentication attempts; retry later")
	return false
}

func requestClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.config.SessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, 401, "unauthorized", "authentication is required")
			return
		}
		account, sessionID, err := s.repo.AccountBySession(r.Context(), auth.HashSessionToken(cookie.Value))
		if err != nil {
			writeError(w, 401, "unauthorized", "authentication is required")
			return
		}
		ctx := context.WithValue(r.Context(), accountContextKey, account)
		ctx = context.WithValue(ctx, sessionContextKey, sessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func accountFrom(r *http.Request) domain.Account {
	return r.Context().Value(accountContextKey).(domain.Account)
}
func sessionFrom(r *http.Request) uuid.UUID { return r.Context().Value(sessionContextKey).(uuid.UUID) }
func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: s.config.SessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.config.CookieSecure, SameSite: projectSessionSameSite(s.config.CookieSecure), MaxAge: int(s.config.SessionTTL.Seconds()), Expires: time.Now().UTC().Add(s.config.SessionTTL)})
}
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.config.SessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.config.CookieSecure, SameSite: projectSessionSameSite(s.config.CookieSecure), MaxAge: -1, Expires: time.Unix(1, 0)})
}

func (s *Server) limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxBodyBytes)
		contentType := strings.ToLower(r.Header.Get("Content-Type"))
		if strings.HasPrefix(contentType, "multipart/form-data") && (strings.Contains(r.URL.Path, "/storage/") || strings.Contains(r.URL.Path, "/functions/") && strings.Contains(r.URL.Path, "/deployments") || strings.Contains(r.URL.Path, "/sites/") && strings.Contains(r.URL.Path, "/deployments")) {
			configured := s.config.StorageMaxFileSize
			if strings.Contains(r.URL.Path, "/functions/") {
				configured = s.config.FunctionsMaxArtifactSize
			}
			if strings.Contains(r.URL.Path, "/sites/") {
				configured = s.config.SitesMaxArtifactSize
			}
			limit = configured + maxMultipartOverhead
			if limit < configured || limit <= 0 {
				limit = int64(maxBodyBytes)
			}
		}
		if r.ContentLength > limit {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the configured upload limit")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}
