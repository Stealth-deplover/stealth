package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
)

type organizationInvitationRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type organizationInvitationAcceptRequest struct {
	Token  string `json:"token"`
	Secret string `json:"secret"` // Appwrite-compatible alias.
}

func (req organizationInvitationAcceptRequest) tokenValue() string {
	if strings.TrimSpace(req.Token) != "" {
		return strings.TrimSpace(req.Token)
	}
	return strings.TrimSpace(req.Secret)
}

func (s *Server) listOrganizationInvitations(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	items, next, canManage, err := s.repo.ListOrganizationInvitations(r.Context(), organizationID, mustUUID(accountFrom(r).ID), limit, cursor)
	if organizationInvitationError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createOrganizationInvitation(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	var req organizationInvitationRequest
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
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		internalError(s, w, err)
		return
	}
	item, err := s.repo.CreateOrganizationInvitation(r.Context(), organizationID, mustUUID(accountFrom(r).ID), email, req.Role, tokenHash, time.Now().UTC().Add(s.config.AuthVerificationTTL))
	if organizationInvitationError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "that account is already a member of this organization or has an active invitation")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	delivery := "sent"
	if sendErr := s.sendAuthEmail(r.Context(), email, mailer.AuthEmailOrganizationInvitation, s.authLink("accept-invitation", nil, token)); sendErr != nil {
		delivery = "failed"
		s.logger.Error("organization invitation email delivery failed", "organization_id", organizationID, "invitation_id", item.ID, "error", sendErr)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invitation": item, "delivery": delivery})
}

func (s *Server) revokeOrganizationInvitation(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := pathUUID(w, r, "organizationID")
	if !ok {
		return
	}
	invitationID, ok := pathUUID(w, r, "invitationID")
	if !ok {
		return
	}
	err := s.repo.RevokeOrganizationInvitation(r.Context(), organizationID, invitationID, mustUUID(accountFrom(r).ID))
	if organizationInvitationError(w, err) {
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "an accepted invitation cannot be revoked")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) acceptOrganizationInvitation(w http.ResponseWriter, r *http.Request) {
	var req organizationInvitationAcceptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	token := req.tokenValue()
	if err := auth.ValidateToken(token); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_invitation", "invitation token is invalid or expired")
		return
	}
	membership, err := s.repo.AcceptOrganizationInvitation(r.Context(), auth.HashSessionToken(token), mustUUID(accountFrom(r).ID))
	if errors.Is(err, repository.ErrInvalidOrganizationInvitation) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_invitation", "invitation token is invalid, expired, revoked, or already used")
		return
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "invitation_email_mismatch", "sign in with the email address that received this invitation")
		return
	}
	if planLimitError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Membership{"membership": membership})
}

func organizationInvitationError(w http.ResponseWriter, err error) bool {
	if planLimitError(w, err) {
		return true
	}
	if errors.Is(err, repository.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "only organization owners and admins can manage invitations")
		return true
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "organization invitation was not found")
		return true
	}
	return false
}
