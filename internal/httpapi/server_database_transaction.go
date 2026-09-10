package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type databaseRowTransactionRequest struct {
	Operations []databaseRowTransactionOperationRequest `json:"operations"`
}

type databaseRowTransactionOperationRequest struct {
	Action            string          `json:"action"`
	ID                string          `json:"id"`
	Data              json.RawMessage `json:"data"`
	ReadPermissions   *[]string       `json:"read_permissions"`
	UpdatePermissions *[]string       `json:"update_permissions"`
	DeletePermissions *[]string       `json:"delete_permissions"`
}

func (s *Server) transactDatabaseRows(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	tableID, ok := pathUUID(w, r, "tableID")
	if !ok {
		return
	}
	var req databaseRowTransactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	operations := make([]repository.DatabaseRowTransactionOperation, 0, len(req.Operations))
	for index, operation := range req.Operations {
		var id uuid.UUID
		if operation.ID != "" {
			parsed, err := repository.ParseUUID(operation.ID)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "transaction operation id must be a UUID")
				return
			}
			id = parsed
		}
		var data map[string]any
		if len(operation.Data) > 0 && string(operation.Data) != "null" {
			decoder := json.NewDecoder(bytes.NewReader(operation.Data))
			if err := decoder.Decode(&data); err != nil || data == nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "transaction operation data must be an object")
				return
			}
			// decodeJSON already rejects trailing values for the envelope; this
			// guard keeps each raw operation equally strict.
			if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "transaction operation data must contain one JSON value")
				return
			}
		}
		if operation.Action == "update" || operation.Action == "delete" {
			if operation.ID == "" {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "update and delete operations require id")
				return
			}
		}
		if operation.Action == "delete" && len(operation.Data) > 0 {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "delete operations cannot include data")
			return
		}
		if operation.Action != "create" && operation.Action != "update" && operation.Action != "delete" {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "operation "+strconv.Itoa(index)+" action must be create, update, or delete")
			return
		}
		operations = append(operations, repository.DatabaseRowTransactionOperation{
			Action: operation.Action, ID: id, Data: data,
			ReadPermissions: operation.ReadPermissions, UpdatePermissions: operation.UpdatePermissions,
			DeletePermissions: operation.DeletePermissions,
		})
	}
	result, err := s.repo.TransactDatabaseRows(r.Context(), projectID, databaseID, tableID, databaseActorFrom(r), operations)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": result.Rows, "deleted_ids": result.DeletedIDs, "count": len(result.Rows) + len(result.DeletedIDs)})
}
