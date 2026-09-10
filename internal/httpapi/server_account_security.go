package httpapi

import (
	"errors"
	"net/http"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type updateAccountPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
}

func (s *Server) updateAccountPassword(w http.ResponseWriter, r *http.Request) {
	var req updateAccountPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	accountID := uuid.MustParse(accountFrom(r).ID)
	_, currentHash, err := s.repo.AccountPassword(r.Context(), accountFrom(r).Email)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is invalid")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if !auth.VerifyPasswordOrDummy(currentHash, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is invalid")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if auth.VerifyPasswordOrDummy(currentHash, req.Password) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "new password must differ from the current password")
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		internalError(s, w, err)
		return
	}
	revoked, err := s.repo.UpdateAccountPassword(r.Context(), accountID, sessionFrom(r), passwordHash)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "account is no longer available")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"sessions_revoked": revoked})
}
