package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

const corsAllowedMethods = "GET, POST, PATCH, DELETE, OPTIONS"
const corsAllowedHeaders = "Accept, Authorization, Content-Type, Idempotency-Key, Last-Event-ID, X-Requested-With"

// cors applies a per-project, credentialed origin allowlist. The Console
// bridge intentionally strips Origin, so Console requests remain same-origin
// and do not need to be added to every project. For browser applications, a
// configured origin is required before cookies or API keys can be used.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawOrigin := strings.TrimSpace(r.Header.Get("Origin"))
		if rawOrigin == "" {
			next.ServeHTTP(w, r)
			return
		}
		origin, err := repository.NormalizeCORSOrigin(rawOrigin)
		if err != nil {
			corsDenied(w, r)
			return
		}
		// The browser-hosted management Console is a trusted application
		// surface, not a tenant's public application. Its origins are explicit
		// deployment configuration so credentials are never enabled for an
		// arbitrary origin. This check also covers project routes, allowing the
		// Console to use its management session without copying its origin into
		// every project's public Auth CORS setting.
		if containsCORSOrigin(s.config.ConsoleCORSOrigins, origin) {
			setCORSHeaders(w, origin, r)
			if r.Method == http.MethodOptions {
				if !corsMethodAllowed(r.Header.Get("Access-Control-Request-Method")) {
					corsDenied(w, r)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		projectID, ok := projectIDFromCORSPath(r.URL.Path)
		if !ok {
			// Management endpoints have no tenant policy to consult. Reject an
			// unconfigured browser origin before it can perform a credentialed
			// request; same-origin and non-browser requests have no Origin header
			// and took the early return above.
			corsDenied(w, r)
			return
		}
		origins, err := s.repo.ProjectCORSOrigins(r.Context(), projectID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				next.ServeHTTP(w, r)
				return
			}
			internalError(s, w, err)
			return
		}
		if !containsCORSOrigin(origins, origin) {
			corsDenied(w, r)
			return
		}

		setCORSHeaders(w, origin, r)
		if r.Method == http.MethodOptions {
			if !corsMethodAllowed(r.Header.Get("Access-Control-Request-Method")) {
				corsDenied(w, r)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func projectIDFromCORSPath(path string) (uuid.UUID, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "projects" {
		return uuid.Nil, false
	}
	projectID, err := repository.ParseUUID(parts[2])
	if err != nil {
		return uuid.Nil, false
	}
	return projectID, true
}

func containsCORSOrigin(origins []string, target string) bool {
	for _, origin := range origins {
		if origin == target {
			return true
		}
	}
	return false
}

func setCORSHeaders(w http.ResponseWriter, origin string, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
	w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
	w.Header().Set("Access-Control-Expose-Headers", "Content-Type, X-Trace-ID")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Add("Vary", "Origin")
	if r.Method == http.MethodOptions {
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
	}
}

func corsMethodAllowed(raw string) bool {
	method := strings.ToUpper(strings.TrimSpace(raw))
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func corsDenied(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusForbidden, "cors_forbidden", "origin is not allowed for this project")
}
