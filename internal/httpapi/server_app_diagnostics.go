package httpapi

import (
	"net/http"
)

func (s *Server) getAppDiagnostics(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	diagnostics, err := s.repo.GetAppDiagnostics(r.Context(), projectID, appID, appActorFrom(r))
	if appResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, diagnostics)
}

func (s *Server) rollbackAppDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, appID, deploymentID, ok := appDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	result, err := s.repo.RollbackAppDeployment(r.Context(), projectID, appID, deploymentID, appActorFrom(r))
	if appResourceError(w, err) || appDeploymentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": result.App, "deployment": result.Deployment})
}
