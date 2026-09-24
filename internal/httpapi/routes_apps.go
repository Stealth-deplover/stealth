package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerAppRoutes(r chi.Router) {
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/apps", s.listApps)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/apps", s.createApp)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/apps/{appID}", s.getApp)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/apps/{appID}", s.updateApp)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/apps/{appID}", s.deleteApp)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/apps/{appID}/deployments", s.listAppDeployments)
	r.With(s.requireProjectManagement, s.rateLimitProjectOperation("app_deployment")).Post("/projects/{projectID}/apps/{appID}/deployments", s.createAppDeployment)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/apps/{appID}/deployments/{deploymentID}", s.getAppDeployment)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/apps/{appID}/deployments/{deploymentID}/logs", s.listAppBuildLogs)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/apps/{appID}/deployments/{deploymentID}/select", s.selectAppDeployment)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/apps/{appID}/deployments/{deploymentID}", s.deleteAppDeployment)
}
