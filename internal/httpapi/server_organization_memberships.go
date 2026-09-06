package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/validate"
)

type organizationMembershipRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *Server) createMembership(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	var req organizationMembershipRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := validate.Email(req.Email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if !repository.OrganizationMembershipRole(req.Role) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "role must be one of admin, developer, viewer, or billing")
		return
	}
	item, err := s.repo.AddOrganizationMembership(r.Context(), organizationID, uuid.Must(uuid.Parse(accountFrom(r).ID)), email, req.Role)
	if organizationMembershipMutationError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "the account is already a member of this organization")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.Membership{"membership": item})
}

func (s *Server) updateMembership(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	targetID, ok := pathUUID(w, r, "accountID")
	if !ok {
		return
	}
	var req organizationMembershipRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !repository.OrganizationMembershipRole(req.Role) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "role must be one of admin, developer, viewer, or billing")
		return
	}
	item, err := s.repo.UpdateOrganizationMembershipRole(r.Context(), organizationID, targetID, uuid.Must(uuid.Parse(accountFrom(r).ID)), req.Role)
	if organizationMembershipMutationError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Membership{"membership": item})
}

func (s *Server) removeMembership(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	targetID, ok := pathUUID(w, r, "accountID")
	if !ok {
		return
	}
	err := s.repo.RemoveOrganizationMembership(r.Context(), organizationID, targetID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	if organizationMembershipMutationError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func organizationMembershipMutationError(w http.ResponseWriter, err error) bool {
	if planLimitError(w, err) {
		return true
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "organization or account membership was not found")
		return true
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage this organization membership")
		return true
	}
	return false
}
