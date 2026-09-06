package httpapi

import "github.com/go-chi/chi/v5"

// registerAgentRoutes keeps the agent control-plane surface together. Route
// groups are extracted from the server constructor incrementally so the
// public API can be reorganized without changing handlers or middleware.
func (s *Server) registerAgentRoutes(r chi.Router) {
	r.With(s.requireSession).Get("/agent-catalog", s.agentCatalog)
	r.With(s.requireSession).Get("/agents", s.listAgents)
	r.With(s.requireSession).Post("/agents", s.createAgent)
	r.With(s.requireSession).Get("/agents/{agentID}", s.getAgent)
	r.With(s.requireSession).Patch("/agents/{agentID}", s.updateAgent)
	r.With(s.requireSession).Delete("/agents/{agentID}", s.deleteAgent)
	r.With(s.requireSession).Get("/agents/{agentID}/runs", s.listAgentRuns)
	r.With(s.requireSession).Post("/agents/{agentID}/runs", s.createAgentRun)
	r.With(s.requireSession).Get("/agents/{agentID}/runs/{runID}", s.getAgentRun)
	r.With(s.requireSession).Post("/agents/{agentID}/runs/{runID}/cancel", s.cancelAgentRun)
	r.With(s.requireSession).Get("/agents/{agentID}/runs/{runID}/logs", s.listAgentRunLogs)
}
