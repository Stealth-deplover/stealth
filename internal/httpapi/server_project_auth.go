package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/google/uuid"
)

func (s *Server) registerProjectUser(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req projectUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled, err := s.repo.ProjectRegistrationEnabled(r.Context(), projectID)
	if projectSettingsError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if !enabled {
		writeError(w, http.StatusForbidden, "registration_disabled", "public registration is disabled for this project")
		return
	}
	email, err := validate.Email(req.Email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	name, err := optionalProjectUserName(req.Name)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if !s.allowPublicAuth(w, r, "registration", projectID, email) {
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to create application session")
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to create application session")
		return
	}
	item, err := s.repo.RegisterProjectUser(r.Context(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), projectID, email, passwordHash, name, tokenHash, time.Now().UTC().Add(s.config.AppSessionTTL))
	if errors.Is(err, repository.ErrRegistrationDisabled) {
		writeError(w, http.StatusForbidden, "registration_disabled", "public registration is disabled for this project")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "an application user with this email already exists in the project")
		return
	}
	if projectSettingsError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	s.setProjectSessionCookie(w, projectID, token)
	userID, userIDErr := repository.ParseUUID(item.ID)
	if userIDErr == nil {
		s.issueProjectUserVerification(r, projectID, userID, item.Email)
	} else {
		s.logger.Error("project verification setup failed", "project_id", projectID, "user_id", item.ID, "error", userIDErr)
	}
	writeJSON(w, http.StatusCreated, map[string]domain.ApplicationUser{"account": item})
}

func (s *Server) loginProjectUser(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(req.Email))
	email, validationErr := validate.Email(req.Email)
	if validationErr == nil {
		normalizedEmail = email
	}
	if !s.allowPublicAuth(w, r, "login", projectID, normalizedEmail) {
		return
	}
	if len(req.Password) > 256 {
		// Do dummy Argon2id work without loading or verifying the user's real
		// hash. This bounds work for oversized credentials while preserving the
		// same invalid-credentials response as unknown accounts.
		auth.VerifyPasswordOrDummy("", req.Password)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if validationErr != nil {
		auth.VerifyPasswordOrDummy("", req.Password)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	userID, passwordHash, status, err := s.repo.ApplicationUserPassword(r.Context(), projectID, email)
	if errors.Is(err, repository.ErrNotFound) {
		auth.VerifyPasswordOrDummy("", req.Password)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	validPassword := auth.VerifyPasswordOrDummy(passwordHash, req.Password)
	if !validPassword || status != "active" {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to create application session")
		return
	}
	err = s.repo.CreateProjectUserSession(r.Context(), uuid.Must(uuid.NewV7()), projectID, userID, tokenHash, time.Now().UTC().Add(s.config.AppSessionTTL))
	if errors.Is(err, repository.ErrForbidden) || errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	s.setProjectSessionCookie(w, projectID, token)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) currentProjectUser(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]domain.ApplicationUser{"account": projectUserFrom(r)})
}

func (s *Server) logoutProjectUser(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	if err := s.repo.DeleteProjectUserSession(r.Context(), projectID, projectUserSessionFrom(r)); err != nil {
		internalError(s, w, err)
		return
	}
	s.clearProjectSessionCookie(w, projectID)
	w.WriteHeader(http.StatusNoContent)
}

type projectAuthSettingsRequest struct {
	RegistrationEnabled *bool     `json:"registration_enabled"`
	CORSOrigins         *[]string `json:"cors_origins"`
}

func (s *Server) getProjectAuthSettings(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	item, canManage, err := s.repo.ProjectAuthSettings(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)))
	if projectSettingsError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": item, "can_manage": canManage})
}

func (s *Server) updateProjectAuthSettings(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req projectAuthSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RegistrationEnabled == nil && req.CORSOrigins == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "registration_enabled or cors_origins is required")
		return
	}
	var origins *[]string
	if req.CORSOrigins != nil {
		normalized, normalizeErr := repository.NormalizeCORSOrigins(*req.CORSOrigins)
		if normalizeErr != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "cors_origins must contain up to 32 valid HTTP(S) origins without paths or wildcards")
			return
		}
		origins = &normalized
	}
	item, err := s.repo.UpdateProjectAuthSettings(r.Context(), projectID, uuid.Must(uuid.Parse(accountFrom(r).ID)), req.RegistrationEnabled, origins)
	if projectSettingsError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": item, "can_manage": true})
}
