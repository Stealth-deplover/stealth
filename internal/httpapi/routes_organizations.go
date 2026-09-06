package httpapi

import "github.com/go-chi/chi/v5"

func (s *Server) registerOrganizationRoutes(r chi.Router) {
	r.With(s.requireSession).Get("/organizations", s.listOrganizations)
	r.With(s.requireSession).Post("/organizations", s.createOrganization)
	r.With(s.requireSession).Patch("/organizations/{organizationID}", s.updateOrganization)
	r.With(s.requireSession).Get("/organizations/{organizationID}/plan", s.getOrganizationPlan)
	r.With(s.requireSession).Get("/organizations/{organizationID}/memberships", s.listMemberships)
	r.With(s.requireSession).Post("/organizations/{organizationID}/memberships", s.createMembership)
	r.With(s.requireSession).Patch("/organizations/{organizationID}/memberships/{accountID}", s.updateMembership)
	r.With(s.requireSession).Delete("/organizations/{organizationID}/memberships/{accountID}", s.removeMembership)
	r.With(s.requireSession).Get("/organizations/{organizationID}/invitations", s.listOrganizationInvitations)
	r.With(s.requireSession).Post("/organizations/{organizationID}/invitations", s.createOrganizationInvitation)
	r.With(s.requireSession).Delete("/organizations/{organizationID}/invitations/{invitationID}", s.revokeOrganizationInvitation)
	r.With(s.requireSession).Post("/organization-invitations/accept", s.acceptOrganizationInvitation)
	r.With(s.requireSession).Get("/organizations/{organizationID}/incidents", s.listOrganizationIncidents)
	r.With(s.requireSession).Post("/organizations/{organizationID}/incidents", s.createOrganizationIncident)
	r.With(s.requireSession).Get("/organizations/{organizationID}/incidents/{incidentID}", s.getOrganizationIncident)
	r.With(s.requireSession).Patch("/organizations/{organizationID}/incidents/{incidentID}", s.updateOrganizationIncident)
	r.With(s.requireSession).Get("/organizations/{organizationID}/traces", s.listOrganizationTraces)
	r.With(s.requireSession).Get("/organizations/{organizationID}/audit-events", s.listAuditEvents)
}
