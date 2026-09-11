package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/google/uuid"
)

type bootstrapStatusResponse struct {
	SetupRequired bool `json:"setup_required"`
}

type bootstrapSessionResponse struct {
	SetupCode string    `json:"setup_code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type createInstanceOwnerRequest struct {
	SetupCode string `json:"setup_code"`
	Email     string `json:"email"`
	Password  string `json:"password"`
}

func (s *Server) bootstrapStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.repo.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeBootstrapJSON(w, http.StatusOK, bootstrapStatusResponse{SetupRequired: status.SetupRequired})
}

func (s *Server) createBootstrapSession(w http.ResponseWriter, r *http.Request) {
	if !bootstrap.VerifyCLIProof(s.bootstrapCLIKey(), r.Header.Get(bootstrap.CLIProofHeader)) {
		writeBootstrapError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
		return
	}
	code, err := bootstrap.GenerateCode()
	if err != nil {
		internalError(s, w, err)
		return
	}
	expiresAt := time.Now().UTC().Add(bootstrap.CodeLifetime)
	if err := s.repo.CreateBootstrapSession(r.Context(), repository.BootstrapSessionInput{
		ID:        uuid.Must(uuid.NewV7()),
		CodeHash:  bootstrap.HashCode(code),
		ExpiresAt: expiresAt,
	}); err != nil {
		switch {
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
		case errors.Is(err, repository.ErrInvalidBootstrapCode):
			writeBootstrapError(w, http.StatusUnprocessableEntity, "invalid_bootstrap_session", "unable to create a setup session")
		default:
			internalError(s, w, err)
		}
		return
	}
	writeBootstrapJSON(w, http.StatusCreated, bootstrapSessionResponse{SetupCode: code, ExpiresAt: expiresAt})
}

func (s *Server) createInstanceOwner(w http.ResponseWriter, r *http.Request) {
	var req createInstanceOwnerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allowBootstrapOwner(w, r, req.Email) {
		return
	}
	if !bootstrap.ValidCode(req.SetupCode) {
		writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "invalid setup code")
		return
	}
	email, err := validate.Email(req.Email)
	if err != nil {
		writeBootstrapError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeBootstrapError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		internalError(s, w, err)
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		internalError(s, w, err)
		return
	}
	account, err := s.repo.CreateInstanceOwner(r.Context(), repository.InstanceOwnerInput{
		AccountID:         uuid.Must(uuid.NewV7()),
		SessionID:         uuid.Must(uuid.NewV7()),
		Email:             email,
		PasswordHash:      passwordHash,
		TokenHash:         tokenHash,
		SessionExpiresAt:  time.Now().UTC().Add(s.config.SessionTTL),
		BootstrapCodeHash: bootstrap.HashCode(req.SetupCode),
	})
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrInvalidBootstrapCode):
			writeBootstrapError(w, http.StatusUnauthorized, "invalid_bootstrap_code", "invalid setup code")
		case errors.Is(err, repository.ErrBootstrapSealed):
			writeBootstrapError(w, http.StatusGone, "bootstrap_complete", "instance setup has already been completed")
		case errors.Is(err, repository.ErrConflict):
			writeBootstrapError(w, http.StatusConflict, "conflict", "an account with this email already exists")
		default:
			internalError(s, w, err)
		}
		return
	}
	s.setSessionCookie(w, token)
	writeBootstrapJSON(w, http.StatusCreated, map[string]domain.Account{"account": account})
}

func (s *Server) allowBootstrapOwner(w http.ResponseWriter, r *http.Request, email string) bool {
	clientIP := s.requestClientIP(r)
	keys := []string{
		ratelimit.InstanceIPKey("bootstrap_owner", clientIP),
		ratelimit.InstanceKey("bootstrap_owner", strings.ToLower(strings.TrimSpace(email)), clientIP),
	}
	for _, key := range keys {
		decision, err := s.limiter.Allow(r.Context(), key, s.config.AuthRateLimit, s.config.AuthRateWindow)
		if err != nil {
			s.logger.Error("bootstrap rate limiter failed", "error", err)
			writeBootstrapError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication protection is temporarily unavailable")
			return false
		}
		if !decision.Allowed {
			return writeRateLimited(w, decision.RetryAfter)
		}
	}
	return true
}

func (s *Server) bootstrapCLIKey() []byte {
	if len(s.config.BootstrapCLIKey) > 0 {
		return s.config.BootstrapCLIKey
	}
	return s.config.FunctionsSecretKey
}

func writeBootstrapJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, value)
}

func writeBootstrapError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, status, code, message)
}
