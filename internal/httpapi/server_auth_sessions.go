package httpapi

import (
	"errors"
	"net/http"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func (s *Server) listConsoleSessions(w http.ResponseWriter, r *http.Request) {
	items, err := s.repo.ListConsoleSessions(r.Context(), uuid.MustParse(accountFrom(r).ID), sessionFrom(r))
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (s *Server) revokeConsoleSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := pathUUID(w, r, "sessionID")
	if !ok {
		return
	}
	if err := s.repo.RevokeConsoleSession(r.Context(), uuid.MustParse(accountFrom(r).ID), sessionID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "session was not found")
			return
		}
		internalError(s, w, err)
		return
	}
	if sessionID == sessionFrom(r) {
		s.clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeOtherConsoleSessions(w http.ResponseWriter, r *http.Request) {
	count, err := s.repo.RevokeOtherConsoleSessions(r.Context(), uuid.MustParse(accountFrom(r).ID), sessionFrom(r))
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": count})
}
