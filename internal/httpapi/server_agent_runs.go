package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
)

type agentRunRequest struct {
	Prompt string `json:"prompt"`
}

func (s *Server) listAgentRuns(w http.ResponseWriter, r *http.Request) {
	agentID, ok := pathUUID(w, r, "agentID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursorID *uuid.UUID
	if cursor != "" {
		parsed := mustUUID(cursor)
		cursorID = &parsed
	}
	items, next, err := s.repo.ListAgentRuns(r.Context(), mustUUID(accountFrom(r).ID), agentID, limit, cursorID)
	if agentRunResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": items, "pagination": paginationOf(limit, next)})
}

// createAgentRun only records an accepted queue item. It never invokes a
// provider or executes repository code inline; a trusted worker owns that
// boundary after a provider connection is configured.
func (s *Server) createAgentRun(w http.ResponseWriter, r *http.Request) {
	agentID, ok := pathUUID(w, r, "agentID")
	if !ok {
		return
	}
	var req agentRunRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.repo.CreateAgentRun(r.Context(), uuid.Must(uuid.NewV7()), mustUUID(accountFrom(r).ID), agentID, repository.AgentRunInput{Prompt: req.Prompt})
	if agentRunResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]domain.AgentRun{"run": item})
}

func (s *Server) getAgentRun(w http.ResponseWriter, r *http.Request) {
	agentID, runID, ok := agentRunPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.AgentRunByID(r.Context(), mustUUID(accountFrom(r).ID), agentID, runID)
	if agentRunResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.AgentRun{"run": item})
}

func (s *Server) cancelAgentRun(w http.ResponseWriter, r *http.Request) {
	agentID, runID, ok := agentRunPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.CancelAgentRun(r.Context(), mustUUID(accountFrom(r).ID), agentID, runID)
	if agentRunResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.AgentRun{"run": item})
}

func (s *Server) listAgentRunLogs(w http.ResponseWriter, r *http.Request) {
	agentID, runID, ok := agentRunPathIDs(w, r)
	if !ok {
		return
	}
	limit, _, ok := page(w, r)
	if !ok {
		return
	}
	after := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("after")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "after must be a non-negative integer")
			return
		}
		after = parsed
	}
	items, err := s.repo.ListAgentRunLogs(r.Context(), mustUUID(accountFrom(r).ID), agentID, runID, limit, after)
	if agentRunResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	next := ""
	if len(items) == limit {
		next = strconv.FormatInt(items[len(items)-1].Sequence, 10)
	}
	var nextCursor *string
	if next != "" {
		nextCursor = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": items, "pagination": pagination{Limit: limit, NextCursor: nextCursor}})
}

func agentRunPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	agentID, ok := pathUUID(w, r, "agentID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	runID, ok := pathUUID(w, r, "runID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return agentID, runID, true
}

func agentRunResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "agent or run was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "only project owners and admins can run agents")
		return true
	case errors.Is(err, repository.ErrInvalidAgentRun):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "agent run is invalid")
		return true
	case errors.Is(err, repository.ErrInvalidAgentRunTransition):
		writeError(w, http.StatusConflict, "conflict", "agent run cannot be changed from its current status")
		return true
	case errors.Is(err, repository.ErrAgentRunNotAvailable):
		writeError(w, http.StatusConflict, "conflict", "agent run is no longer available to the worker")
		return true
	default:
		return false
	}
}
