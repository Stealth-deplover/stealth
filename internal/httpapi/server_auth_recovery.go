package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/google/uuid"
)

type authTokenRequest struct {
	Token    string `json:"token"`
	Secret   string `json:"secret"`  // Appwrite-compatible alias for token.
	UserID   string `json:"user_id"` // Accepted for SDK compatibility; the token remains authoritative.
	Password string `json:"password"`
}

type authVerificationRequest struct {
	URL string `json:"url"` // Optional redirect URL; constrained to trusted origins.
}

func (r authTokenRequest) tokenValue() string {
	if strings.TrimSpace(r.Token) != "" {
		return strings.TrimSpace(r.Token)
	}
	return strings.TrimSpace(r.Secret)
}

// Registration creates the identity first so email delivery cannot make the
// account transaction partially succeed. A retryable verification request is
// available if the relay is temporarily unavailable.
func (s *Server) issueAccountVerification(r *http.Request, accountID uuid.UUID, email string) {
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		s.logger.Error("account verification token generation failed", "account_id", accountID, "error", err)
		return
	}
	if _, err := s.repo.IssueAccountAuthToken(r.Context(), accountID, repository.AuthTokenEmailVerification, tokenHash, time.Now().UTC().Add(s.config.AuthVerificationTTL)); err != nil {
		s.logger.Error("account verification token persistence failed", "account_id", accountID, "error", err)
		return
	}
	if err := s.sendAuthEmail(r, email, "Verify your Stealth email", s.authLink("verify-email", nil, token), "email verification"); err != nil {
		s.logger.Warn("account verification email was not delivered", "account_id", accountID, "error", err)
	}
}

func (s *Server) issueProjectUserVerification(r *http.Request, projectID, userID uuid.UUID, email string) {
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		s.logger.Error("project verification token generation failed", "project_id", projectID, "user_id", userID, "error", err)
		return
	}
	if _, err := s.repo.IssueProjectUserAuthToken(r.Context(), projectID, userID, repository.AuthTokenEmailVerification, tokenHash, time.Now().UTC().Add(s.config.AuthVerificationTTL)); err != nil {
		s.logger.Error("project verification token persistence failed", "project_id", projectID, "user_id", userID, "error", err)
		return
	}
	if err := s.sendAuthEmail(r, email, "Verify your email", s.authLink("verify-email", &projectID, token), "project user email verification"); err != nil {
		s.logger.Warn("project verification email was not delivered", "project_id", projectID, "user_id", userID, "error", err)
	}
}

// sendAccountVerification creates a one-time email verification secret for a
// signed-in Console account. A second request invalidates the previous secret.
func (s *Server) sendAccountVerification(w http.ResponseWriter, r *http.Request) {
	var req authVerificationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	account := accountFrom(r)
	accountID, err := repository.ParseUUID(account.ID)
	if err != nil {
		internalError(s, w, err)
		return
	}
	if !s.allowAccountAuth(w, r, "verification_send", account.Email) {
		return
	}
	if account.EmailVerified {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		internalError(s, w, err)
		return
	}
	link, err := s.authLinkFor(r, "verify-email", nil, token, req.URL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if _, err := s.repo.IssueAccountAuthToken(r.Context(), accountID, repository.AuthTokenEmailVerification, tokenHash, time.Now().UTC().Add(s.config.AuthVerificationTTL)); err != nil {
		internalError(s, w, err)
		return
	}
	if err := s.sendAuthEmail(r, account.Email, "Verify your Stealth email", link, "email verification"); err != nil {
		s.logger.Error("account verification email delivery failed", "account_id", account.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "email_delivery_unavailable", "verification email could not be delivered")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) confirmAccountVerification(w http.ResponseWriter, r *http.Request) {
	var req authTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	token := req.tokenValue()
	if !s.allowAccountAuth(w, r, "verification_confirm", "") || !s.validAuthTokenRequest(w, r, token) {
		return
	}
	item, err := s.repo.VerifyAccountEmail(r.Context(), auth.HashSessionToken(token))
	if errors.Is(err, repository.ErrInvalidAuthToken) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_token", "verification token is invalid or expired")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Account{"account": item})
}

// createAccountRecovery always returns the same response, whether or not the
// address belongs to a Console account. This prevents account enumeration.
func (s *Server) createAccountRecovery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		URL   string `json:"url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeAuthEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if !s.allowAccountAuth(w, r, "recovery", email) {
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		internalError(s, w, err)
		return
	}
	link, linkErr := s.authLinkFor(r, "reset-password", nil, token, req.URL)
	if linkErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", linkErr.Error())
		return
	}
	account, found, err := s.repo.CreateAccountPasswordResetToken(r.Context(), email, tokenHash, time.Now().UTC().Add(s.config.AuthPasswordResetTTL))
	if err != nil {
		internalError(s, w, err)
		return
	}
	if found {
		if sendErr := s.sendAuthEmail(r, account.Email, "Reset your Stealth password", link, "password recovery"); sendErr != nil {
			s.logger.Error("account recovery email delivery failed", "account_id", account.ID, "error", sendErr)
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) confirmAccountRecovery(w http.ResponseWriter, r *http.Request) {
	var req authTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	token := req.tokenValue()
	if !s.allowAccountAuth(w, r, "recovery_confirm", "") || !s.validAuthTokenRequest(w, r, token) {
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		internalError(s, w, err)
		return
	}
	_, err = s.repo.ResetAccountPassword(r.Context(), auth.HashSessionToken(token), passwordHash)
	if errors.Is(err, repository.ErrInvalidAuthToken) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_token", "recovery token is invalid or expired")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sendProjectUserVerification(w http.ResponseWriter, r *http.Request) {
	var req authVerificationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	user := projectUserFrom(r)
	if !s.allowPublicAuth(w, r, "verification_send", projectID, user.Email) {
		return
	}
	if user.EmailVerified {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, err := repository.ParseUUID(user.ID)
	if err != nil {
		internalError(s, w, err)
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		internalError(s, w, err)
		return
	}
	link, err := s.authLinkFor(r, "verify-email", &projectID, token, req.URL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if _, err := s.repo.IssueProjectUserAuthToken(r.Context(), projectID, userID, repository.AuthTokenEmailVerification, tokenHash, time.Now().UTC().Add(s.config.AuthVerificationTTL)); err != nil {
		internalError(s, w, err)
		return
	}
	if err := s.sendAuthEmail(r, user.Email, "Verify your email", link, "project user email verification"); err != nil {
		s.logger.Error("project user verification email delivery failed", "project_id", projectID, "user_id", user.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "email_delivery_unavailable", "verification email could not be delivered")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) confirmProjectUserVerification(w http.ResponseWriter, r *http.Request) {
	var req authTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	token := req.tokenValue()
	if !s.allowPublicAuth(w, r, "verification_confirm", projectID, "") || !s.validAuthTokenRequest(w, r, token) {
		return
	}
	item, err := s.repo.VerifyProjectUserEmail(r.Context(), projectID, auth.HashSessionToken(token))
	if errors.Is(err, repository.ErrInvalidAuthToken) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_token", "verification token is invalid or expired")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.ApplicationUser{"account": item})
}

func (s *Server) createProjectUserRecovery(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req struct {
		Email string `json:"email"`
		URL   string `json:"url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeAuthEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if !s.allowPublicAuth(w, r, "recovery", projectID, email) {
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		internalError(s, w, err)
		return
	}
	link, linkErr := s.authLinkFor(r, "reset-password", &projectID, token, req.URL)
	if linkErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", linkErr.Error())
		return
	}
	user, found, err := s.repo.CreateProjectUserPasswordResetToken(r.Context(), projectID, email, tokenHash, time.Now().UTC().Add(s.config.AuthPasswordResetTTL))
	if err != nil {
		internalError(s, w, err)
		return
	}
	if found {
		if sendErr := s.sendAuthEmail(r, user.Email, "Reset your password", link, "project user password recovery"); sendErr != nil {
			s.logger.Error("project user recovery email delivery failed", "project_id", projectID, "user_id", user.ID, "error", sendErr)
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) confirmProjectUserRecovery(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req authTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	token := req.tokenValue()
	if !s.allowPublicAuth(w, r, "recovery_confirm", projectID, "") || !s.validAuthTokenRequest(w, r, token) {
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		internalError(s, w, err)
		return
	}
	_, err = s.repo.ResetProjectUserPassword(r.Context(), projectID, auth.HashSessionToken(token), passwordHash)
	if errors.Is(err, repository.ErrInvalidAuthToken) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_token", "recovery token is invalid or expired")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validAuthTokenRequest(w http.ResponseWriter, _ *http.Request, token string) bool {
	if err := auth.ValidateToken(token); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_token", "token must be a valid opaque auth token")
		return false
	}
	return true
}

func normalizeAuthEmail(raw string) (string, error) {
	return validateEmail(raw)
}

func validateEmail(raw string) (string, error) {
	// Keep all email validation in the existing validator so Console and
	// project Auth enforce the same normalization and length rules.
	return validate.Email(raw)
}

func (s *Server) authLink(path string, projectID *uuid.UUID, token string) string {
	base := strings.TrimRight(strings.TrimSpace(s.config.PublicAppURL), "/")
	if base == "" {
		base = "http://localhost:4173"
	}
	parsed, err := url.Parse(base + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return base + "/" + strings.TrimLeft(path, "/") + "?token=" + url.QueryEscape(token)
	}
	query := parsed.Query()
	query.Set("token", token)
	if projectID != nil {
		query.Set("project_id", projectID.String())
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// authLinkFor supports Appwrite-style caller-provided redirect URLs while
// keeping links on an origin the deployment has explicitly trusted. Console
// links may use PUBLIC_APP_URL; project links may additionally use one of the
// project's exact CORS origins. Paths and existing harmless query parameters
// are preserved, while fragments and credentials are rejected.
func (s *Server) authLinkFor(r *http.Request, path string, projectID *uuid.UUID, token, redirect string) (string, error) {
	redirect = strings.TrimSpace(redirect)
	if redirect == "" {
		return s.authLink(path, projectID, token), nil
	}
	if len(redirect) > 2048 || strings.ContainsAny(redirect, "\x00\r\n \t") {
		return "", errors.New("url must be an absolute HTTP(S) URL without credentials, whitespace, or a fragment")
	}
	parsed, err := url.Parse(redirect)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("url must be an absolute HTTP(S) URL without credentials, whitespace, or a fragment")
	}
	origin, err := repository.NormalizeCORSOrigin(parsed.Scheme + "://" + parsed.Host)
	if err != nil {
		return "", errors.New("url must use a valid HTTP(S) origin")
	}
	trusted := false
	base, baseErr := url.Parse(strings.TrimRight(strings.TrimSpace(s.config.PublicAppURL), "/"))
	if baseErr == nil && base.Host != "" {
		baseOrigin, normalizeErr := repository.NormalizeCORSOrigin(base.Scheme + "://" + base.Host)
		trusted = normalizeErr == nil && baseOrigin == origin
	}
	if !trusted && projectID != nil {
		origins, lookupErr := s.repo.ProjectCORSOrigins(r.Context(), *projectID)
		if lookupErr != nil && !errors.Is(lookupErr, repository.ErrNotFound) {
			return "", errors.New("unable to validate redirect origin")
		}
		trusted = containsCORSOrigin(origins, origin)
	}
	if !trusted {
		return "", errors.New("url origin is not trusted for this project")
	}
	query := parsed.Query()
	query.Set("token", token)
	if projectID != nil {
		query.Set("project_id", projectID.String())
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *Server) sendAuthEmail(r *http.Request, recipient, subject, link, purpose string) error {
	if s.emailSender == nil {
		return mailer.ErrDisabled
	}
	message := mailer.Message{
		To:       recipient,
		Subject:  subject,
		TextBody: fmt.Sprintf("Use the following one-time link to complete %s:\n\n%s\n\nThis link expires in %s and can only be used once. If you did not request this, you can ignore this email.", purpose, link, authLinkTTL(s, purpose)),
	}
	return s.emailSender.Send(r.Context(), message)
}

func authLinkTTL(s *Server, purpose string) string {
	if strings.Contains(purpose, "verification") || strings.Contains(purpose, "invitation") {
		return s.config.AuthVerificationTTL.String()
	}
	return s.config.AuthPasswordResetTTL.String()
}
