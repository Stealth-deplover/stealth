package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
)

type databaseRelationshipRequest struct {
	SourceTableID    string `json:"source_table_id"`
	SourceColumnKey  string `json:"source_column_key"`
	TargetTableID    string `json:"target_table_id"`
	RelationshipType string `json:"type"`
	OnDelete         string `json:"on_delete"`
}

func (s *Server) listDatabaseRelationships(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	limit, cursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursorID *uuid.UUID
	if cursor != "" {
		parsed, _ := uuid.Parse(cursor)
		cursorID = &parsed
	}
	items, next, err := s.repo.ListDatabaseRelationships(r.Context(), projectID, databaseID, databaseActorFrom(r), limit, cursorID)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"relationships": items, "pagination": paginationOf(limit, next)})
}

func (s *Server) getDatabaseRelationship(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	relationshipID, ok := pathUUID(w, r, "relationshipID")
	if !ok {
		return
	}
	item, err := s.repo.GetDatabaseRelationship(r.Context(), projectID, databaseID, relationshipID, databaseActorFrom(r))
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.DatabaseRelationship{"relationship": item})
}

func (s *Server) createDatabaseRelationship(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	var req databaseRelationshipRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sourceTableID, err := repository.ParseUUID(req.SourceTableID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source_table_id must be a UUID")
		return
	}
	targetTableID, err := repository.ParseUUID(req.TargetTableID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "target_table_id must be a UUID")
		return
	}
	item, err := s.repo.CreateDatabaseRelationship(r.Context(), uuid.Must(uuid.NewV7()), projectID, databaseID, databaseActorFrom(r), repository.DatabaseRelationshipInput{
		SourceTableID: sourceTableID, SourceColumnKey: req.SourceColumnKey, TargetTableID: targetTableID,
		RelationshipType: req.RelationshipType, OnDelete: req.OnDelete,
	})
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.DatabaseRelationship{"relationship": item})
}

func (s *Server) deleteDatabaseRelationship(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	relationshipID, ok := pathUUID(w, r, "relationshipID")
	if !ok {
		return
	}
	if err := s.repo.DeleteDatabaseRelationship(r.Context(), projectID, databaseID, relationshipID, databaseActorFrom(r)); databaseResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
