package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nazxf/stealth-api/internal/observability"
)

// routes assembles the console API handler: telemetry and security
// middleware, health endpoints, the /v1 resource groups, and the
// custom-domain Site serving fallback. Each group is registered by the
// domain file that owns it so the public API can be reorganized without
// changing handlers or middleware.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	// Request telemetry is outside recovery so a recovered panic is counted as
	// the 500 response that callers actually receive.
	r.Use(s.requestID, observability.HTTPMiddlewareWithRecorder(s.recordHTTPTrace), s.requestLog, s.recoverer, s.limitRequestBody, s.cors)
	r.Get("/healthz", s.health)
	r.Get("/readyz", s.ready)
	r.Get("/version", s.version)
	r.Get("/metrics", s.metricsHandler)
	r.Route("/v1", func(r chi.Router) {
		s.registerAccountRoutes(r)
		s.registerOrganizationRoutes(r)
		s.registerProjectRoutes(r)
		s.registerAgentRoutes(r)
		s.registerMessagingRoutes(r)
		s.registerDatabaseRoutes(r)
		s.registerStorageRoutes(r)
		s.registerFunctionRoutes(r)
		s.registerSiteRoutes(r)
	})
	// A reverse proxy can forward custom-domain traffic to the same API. The
	// hostname is resolved against verified Site domains; unknown hosts return
	// 404 and never fall back to an arbitrary project artifact.
	r.Get("/", s.serveCustomDomainFile)
	r.Get("/*", s.serveCustomDomainFile)
	return r
}
