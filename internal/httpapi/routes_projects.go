package httpapi

import "github.com/go-chi/chi/v5"

// registerProjectRoutes owns the project control plane: project CRUD and
// auditability, project application users and Auth settings, usage, API
// keys, and the persisted service canvas layout.
func (s *Server) registerProjectRoutes(r chi.Router) {
	r.With(s.requireSession).Get("/organizations/{organizationID}/projects", s.listProjects)
	r.With(s.requireSession).Post("/organizations/{organizationID}/projects", s.createProject)
	r.With(s.requireSession).Get("/projects/{projectID}", s.getProject)
	r.With(s.requireSession).Patch("/projects/{projectID}", s.updateProject)
	r.With(s.requireSession).Delete("/projects/{projectID}", s.deleteProject)
	r.With(s.requireSession).Get("/projects/{projectID}/audit-events", s.listProjectAuditEvents)
	r.With(s.requireSession).Get("/projects/{projectID}/traces", s.listProjectTraces)
	r.With(s.requireSession).Get("/projects/{projectID}/service-layout", s.listProjectServiceLayout)
	r.With(s.requireSession).Put("/projects/{projectID}/service-layout", s.replaceProjectServiceLayout)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/users", s.listProjectUsers)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/users", s.createProjectUser)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/users/{userID}", s.getProjectUser)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/users/{userID}", s.deleteProjectUser)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/users/{userID}/status", s.updateProjectUserStatus)
	r.With(s.requireSession).Get("/projects/{projectID}/auth/settings", s.getProjectAuthSettings)
	r.With(s.requireSession).Patch("/projects/{projectID}/auth/settings", s.updateProjectAuthSettings)
	r.With(s.requireSession).Get("/projects/{projectID}/usage", s.getProjectUsage)
	r.With(s.requireSession).Get("/projects/{projectID}/usage/metering", s.getProjectUsageMetering)
	r.With(s.requireSession).Get("/projects/{projectID}/api-keys", s.listProjectAPIKeys)
	r.With(s.requireSession).Post("/projects/{projectID}/api-keys", s.createProjectAPIKey)
	r.With(s.requireSession).Get("/projects/{projectID}/api-keys/{keyID}", s.getProjectAPIKey)
	r.With(s.requireSession).Delete("/projects/{projectID}/api-keys/{keyID}", s.revokeProjectAPIKey)
}
