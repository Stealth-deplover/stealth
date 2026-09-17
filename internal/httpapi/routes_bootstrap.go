package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerBootstrapRoutes(r chi.Router) {
	r.Get("/bootstrap/status", s.bootstrapStatus)
	r.Post("/bootstrap/sessions", s.createBootstrapSession)
	r.Post("/bootstrap/verify", s.verifyBootstrapCode)
	r.Post("/bootstrap/github/device", s.startGitHubDeviceFlow)
	r.Post("/bootstrap/github/poll", s.pollGitHubDeviceFlow)
	r.Get("/bootstrap/adoption/accounts", s.listBootstrapAdoptionAccounts)
	r.Post("/bootstrap/adoption/owner", s.adoptBootstrapOwner)
}
