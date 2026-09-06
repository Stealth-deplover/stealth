package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerAccountRoutes(r chi.Router) {
	r.Post("/account/registrations", s.register)
	r.With(s.requireSession).Get("/account", s.currentAccount)
	r.With(s.requireSession).Get("/account/sessions", s.listConsoleSessions)
	r.With(s.requireSession).Delete("/account/sessions", s.revokeOtherConsoleSessions)
	r.With(s.requireSession).Delete("/account/sessions/{sessionID}", s.revokeConsoleSession)
	r.With(s.requireSession).Patch("/account/password", s.updateAccountPassword)
	r.With(s.requireSession).Post("/account/verification", s.sendAccountVerification)
	r.Put("/account/verification", s.confirmAccountVerification)
	r.Post("/account/recovery", s.createAccountRecovery)
	r.Put("/account/recovery", s.confirmAccountRecovery)
	r.Post("/sessions/email-password", s.login)
	r.With(s.requireSession).Delete("/session", s.logout)
	r.Post("/projects/{projectID}/account/registrations", s.registerProjectUser)
	r.Post("/projects/{projectID}/sessions/email-password", s.loginProjectUser)
	r.With(s.requireProjectAppSession).Get("/projects/{projectID}/account", s.currentProjectUser)
	r.With(s.requireProjectAppSession).Post("/projects/{projectID}/account/verification", s.sendProjectUserVerification)
	r.Put("/projects/{projectID}/account/verification", s.confirmProjectUserVerification)
	r.Post("/projects/{projectID}/account/recovery", s.createProjectUserRecovery)
	r.Put("/projects/{projectID}/account/recovery", s.confirmProjectUserRecovery)
	r.With(s.requireProjectAppSession).Delete("/projects/{projectID}/session", s.logoutProjectUser)
}
