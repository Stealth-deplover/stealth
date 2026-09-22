package httpapi

import (
	"errors"
	"net/http"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/go-chi/chi/v5"
)

// platformSiteRoutes is deliberately a separate handler graph from routes.
// A request reaching this listener can only resolve a current PostgreSQL
// platform hostname and read the corresponding static artifact; it cannot
// fall through to the Console API or an internal health endpoint.
func (s *Server) platformSiteRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(s.requestID, s.recoverer)
	r.Get("/", s.servePlatformSiteFile)
	r.Get("/*", s.servePlatformSiteFile)
	return r
}

func (s *Server) servePlatformSiteFile(w http.ResponseWriter, r *http.Request) {
	hostname := requestHostname(r)
	if s.config.SetupMode || hostname == "" || s.repo == nil {
		writeError(w, http.StatusNotFound, "not_found", "site was not found")
		return
	}
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "site artifact storage is not ready")
		return
	}
	artifact, err := s.repo.GetActiveSiteArtifactByPlatformHostname(r.Context(), hostname)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "site was not found")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	s.servePublishedSiteFile(w, r, artifact)
}
