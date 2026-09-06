package httpapi

import "github.com/go-chi/chi/v5"

// registerSiteRoutes owns the Site control plane plus the public deployment
// serving endpoints. Public routes deliberately stay unauthenticated; the
// handlers resolve immutable deployment artifacts themselves.
func (s *Server) registerSiteRoutes(r chi.Router) {
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/sites", s.listSites)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/sites", s.createSite)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/sites/{siteID}", s.getSite)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/sites/{siteID}", s.updateSite)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/sites/{siteID}", s.deleteSite)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/sites/{siteID}/domains", s.listSiteDomains)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/sites/{siteID}/domains", s.createSiteDomain)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/sites/{siteID}/domains/{domainID}", s.getSiteDomain)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/sites/{siteID}/domains/{domainID}", s.deleteSiteDomain)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/sites/{siteID}/domains/{domainID}/verify", s.verifySiteDomain)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/sites/{siteID}/deployments", s.listSiteDeployments)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/sites/{siteID}/deployments", s.uploadSiteDeployment)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/sites/{siteID}/deployments/git", s.createGitSiteDeployment)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}", s.getSiteDeployment)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}", s.deleteSiteDeployment)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}/activate", s.activateSiteDeployment)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/sites/{siteID}/deployments/{deploymentID}/logs", s.listSiteBuildLogs)
	r.Get("/sites/{siteID}/deployments/{deploymentID}", s.serveSiteDeploymentFile)
	r.Get("/sites/{siteID}/deployments/{deploymentID}/*", s.serveSiteDeploymentFile)
	r.Get("/sites/{siteID}", s.serveSiteFile)
	r.Get("/sites/{siteID}/*", s.serveSiteFile)
}
