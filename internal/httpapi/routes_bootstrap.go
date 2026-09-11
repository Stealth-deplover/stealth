package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerBootstrapRoutes(r chi.Router) {
	r.Get("/bootstrap/status", s.bootstrapStatus)
	r.Post("/bootstrap/sessions", s.createBootstrapSession)
	r.Post("/bootstrap/owner", s.createInstanceOwner)
}
