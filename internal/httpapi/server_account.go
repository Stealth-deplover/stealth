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

type registerRequest struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	OrganizationName string `json:"organization_name"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := validate.Email(req.Email)
	if err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	// Console registration is public just like project registration. Apply
	// both the aggregate IP bucket and the email/IP bucket before doing the
	// expensive password hash or creating tenant rows.
	if !s.allowAccountAuth(w, r, "registration", email) {
		return
	}
	bootstrapStatus, err := s.bootstrap.BootstrapStatus(r.Context())
	if err != nil {
		s.logger.Error("bootstrap status lookup failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "instance setup state is temporarily unavailable")
		return
	}
	if bootstrapStatus.SetupRequired {
		writeError(w, http.StatusConflict, "bootstrap_required", "complete first-run instance setup before creating regular accounts")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	name := req.OrganizationName
	if strings.TrimSpace(name) == "" {
		name = strings.Split(email, "@")[0] + "'s organization"
	}
	name, err = validate.Name(name, "organization_name")
	if err != nil {
		writeError(w, 422, "validation_error", err.Error())
		return
	}
	accountID := uuid.Must(uuid.NewV7())
	orgID := uuid.Must(uuid.NewV7())
	sessionID := uuid.Must(uuid.NewV7())
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to create account")
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, 500, "internal_error", "unable to create session")
		return
	}
	orgSlug := "personal-" + strings.ReplaceAll(orgID.String(), "-", "")[:16]
	account, org, err := s.repo.Signup(r.Context(), repository.SignupInput{AccountID: accountID, OrganizationID: orgID, SessionID: sessionID, Email: email, PasswordHash: passwordHash, OrganizationName: name, OrganizationSlug: orgSlug, TokenHash: tokenHash, SessionExpiresAt: time.Now().UTC().Add(s.config.SessionTTL)})
	if err != nil {
		if errors.Is(err, repository.ErrBootstrapRequired) {
			writeError(w, http.StatusConflict, "bootstrap_required", "complete first-run instance setup before creating regular accounts")
			return
		}
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, 409, "conflict", "an account with this email already exists")
			return
		}
		s.logger.Error("signup failed", "error", err)
		writeError(w, 500, "internal_error", "unable to create account")
		return
	}
	s.setSessionCookie(w, token)
	s.issueAccountVerification(r, accountID, account.Email)
	writeJSON(w, http.StatusCreated, map[string]any{"account": account, "organization": org})
}
func (s *Server) currentAccount(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]domain.Account{"account": accountFrom(r)})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Rate-limit before credential lookup (and before Argon2id work) so
	// unknown addresses and malformed passwords cannot turn this endpoint into
	// an unbounded CPU or account-enumeration oracle.
	normalizedEmail := strings.ToLower(strings.TrimSpace(req.Email))
	if !s.allowAccountAuth(w, r, "login", normalizedEmail) {
		return
	}
	email, err := validate.Email(req.Email)
	if err != nil {
		auth.VerifyPasswordOrDummy("", req.Password)
		writeError(w, 401, "invalid_credentials", "invalid email or password")
		return
	}
	accountID, hash, err := s.repo.AccountPassword(r.Context(), email)
	if errors.Is(err, repository.ErrNotFound) {
		auth.VerifyPasswordOrDummy("", req.Password)
		writeError(w, 401, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		s.logger.Error("account lookup failed", "error", err)
		writeError(w, 500, "internal_error", "unable to create session")
		return
	}
	if !auth.VerifyPasswordOrDummy(hash, req.Password) {
		writeError(w, 401, "invalid_credentials", "invalid email or password")
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, 500, "internal_error", "unable to create session")
		return
	}
	if err = s.repo.CreateSession(r.Context(), uuid.Must(uuid.NewV7()), accountID, tokenHash, time.Now().UTC().Add(s.config.SessionTTL)); err != nil {
		s.logger.Error("session creation failed", "error", err)
		writeError(w, 500, "internal_error", "unable to create session")
		return
	}
	s.setSessionCookie(w, token)
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.repo.DeleteSession(r.Context(), sessionFrom(r), uuid.Must(uuid.Parse(accountFrom(r).ID))); err != nil {
		s.logger.Error("logout failed", "error", err)
		writeError(w, 500, "internal_error", "unable to delete session")
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}
