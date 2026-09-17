package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerAdminRoutes(r chi.Router) {
	r.With(s.requireInstanceAdmin).Get("/admin/overview", s.adminOverview)
	r.With(s.requireInstanceAdmin).Get("/admin/operations", s.adminOperations)
	r.With(s.requireInstanceAdmin).Get("/admin/audit-events", s.adminAuditEvents)
	r.With(s.requireInstanceAdmin).Get("/admin/telemetry/logs", s.adminTelemetryLogs)
	r.With(s.requireInstanceAdmin).Get("/admin/telemetry/traces", s.adminTelemetryTraces)
	r.With(s.requireInstanceAdmin).Get("/admin/telemetry/metrics", s.adminTelemetryMetrics)
	r.With(s.requireInstanceAdmin).Get("/admin/telemetry/sources", s.adminTelemetrySources)
}
