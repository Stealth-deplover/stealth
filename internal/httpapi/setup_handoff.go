package httpapi

import (
	"net/http"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/auth"
)

type setupHandoffRequest struct {
	Token string `json:"token"`
}

// completeSetupHandoff transfers the session created on the temporary setup
// host to the final production origin. The ticket is accepted in a POST body
// so it never becomes a URL, referrer, proxy, or browser-history credential.
func (s *Server) completeSetupHandoff(w http.ResponseWriter, r *http.Request) {
	if s.bootstrap == nil || s.setupHandoff == nil {
		writeError(w, http.StatusServiceUnavailable, "handoff_unavailable", "production session handoff is unavailable")
		return
	}
	if !s.allowBootstrapAttempt(w, r, "handoff") {
		return
	}
	token, ok := setupHandoffToken(r)
	if !ok || auth.ValidateToken(token) != nil {
		writeError(w, http.StatusUnauthorized, "handoff_invalid", "the setup handoff is invalid or expired")
		return
	}
	status, err := s.bootstrap.BootstrapStatus(r.Context())
	if err != nil {
		internalError(s, w, err)
		return
	}
	if status.SetupRequired {
		writeError(w, http.StatusConflict, "handoff_not_ready", "finish first-owner setup before opening the production dashboard")
		return
	}
	sessionToken, err := s.setupHandoff.Consume(r.Context(), token)
	if err != nil {
		s.logger.Warn("setup session handoff rejected", "error", err)
		writeError(w, http.StatusUnauthorized, "handoff_invalid", "the setup handoff is invalid or expired")
		return
	}
	s.setSessionCookie(w, sessionToken)
	target := strings.TrimRight(s.config.PublicAppURL, "/") + "/organizations"
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func setupHandoffToken(r *http.Request) (string, bool) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "application/json") {
		var request setupHandoffRequest
		if !decodeJSON(ioResponseDiscard{}, r, &request) {
			return "", false
		}
		return strings.TrimSpace(request.Token), true
	}
	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") || strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			return "", false
		}
		return strings.TrimSpace(r.FormValue("token")), true
	}
	return "", false
}

// ioResponseDiscard lets the shared strict JSON decoder validate the body
// without writing an error before the handler has decided which response to
// use. A malformed handoff is intentionally indistinguishable from an
// invalid ticket.
type ioResponseDiscard struct{}

func (ioResponseDiscard) Header() http.Header                { return make(http.Header) }
func (ioResponseDiscard) Write(contents []byte) (int, error) { return len(contents), nil }
func (ioResponseDiscard) WriteHeader(int)                    {}
