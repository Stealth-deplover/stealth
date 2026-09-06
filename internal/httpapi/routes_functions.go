package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerFunctionRoutes(r chi.Router) {
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions", s.listFunctions)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/functions", s.createFunction)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}", s.getFunction)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/functions/{functionID}", s.updateFunction)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/functions/{functionID}", s.deleteFunction)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/variables", s.listFunctionVariables)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/functions/{functionID}/variables", s.createFunctionVariable)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/variables/{variableID}", s.getFunctionVariable)
	r.With(s.requireProjectManagement).Patch("/projects/{projectID}/functions/{functionID}/variables/{variableID}", s.updateFunctionVariable)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/functions/{functionID}/variables/{variableID}", s.deleteFunctionVariable)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/deployments", s.listFunctionDeployments)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/functions/{functionID}/deployments", s.uploadFunctionDeployment)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}", s.getFunctionDeployment)
	r.With(s.requireProjectManagement).Delete("/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}", s.deleteFunctionDeployment)
	r.With(s.requireProjectManagement).Post("/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/activate", s.activateFunctionDeployment)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/deployments/{deploymentID}/logs", s.listFunctionBuildLogs)
	r.With(s.requireFunctionExecutionActor).Post("/projects/{projectID}/functions/{functionID}/executions", s.createFunctionExecution)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/executions", s.listFunctionExecutions)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/executions/{executionID}", s.getFunctionExecution)
	r.With(s.requireProjectManagement).Get("/projects/{projectID}/functions/{functionID}/executions/{executionID}/logs", s.listFunctionExecutionLogs)
}
