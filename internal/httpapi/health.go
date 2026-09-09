package httpapi

import (
	"net/http"

	"github.com/nazxf/stealth-api/internal/buildinfo"
	"github.com/nazxf/stealth-api/internal/observability"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, buildinfo.Current())
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.NotFound(w, r)
		return
	}
	observability.ProtectedMetricsHandler(s.metrics.Handler(), s.config.MetricsToken).ServeHTTP(w, r)
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.repo.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "database is not ready")
		return
	}
	if !s.storageReady || s.storage == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "storage is not ready")
		return
	}
	if err := s.storage.Ping(r.Context()); err != nil {
		s.logger.Error("storage is not ready", "error", err)
		writeError(w, http.StatusServiceUnavailable, "not_ready", "storage is not ready")
		return
	}
	if !s.functionsReady || s.functions == nil || s.functionCipher == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "function services are not ready")
		return
	}
	if !s.sitesReady || s.sites == nil || s.siteArchives == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "site services are not ready")
		return
	}
	if err := s.limiter.Ping(r.Context()); err != nil {
		s.logger.Error("rate limiter is not ready", "error", err)
		writeError(w, http.StatusServiceUnavailable, "not_ready", "rate limiter is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
