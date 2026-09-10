package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type databaseRowsImportRequest struct {
	Rows []databaseImportRowRequest `json:"rows"`
}

type databaseImportRowRequest struct {
	ID                   *uuid.UUID      `json:"id"`
	TableID              json.RawMessage `json:"table_id"`
	ProjectID            json.RawMessage `json:"project_id"`
	Data                 json.RawMessage `json:"data"`
	ReadPermissions      *[]string       `json:"read_permissions"`
	UpdatePermissions    *[]string       `json:"update_permissions"`
	DeletePermissions    *[]string       `json:"delete_permissions"`
	CreatorProjectUserID json.RawMessage `json:"creator_project_user_id"`
	CreatedAt            json.RawMessage `json:"created_at"`
	UpdatedAt            json.RawMessage `json:"updated_at"`
}

func (s *Server) importDatabaseRows(w http.ResponseWriter, r *http.Request) {
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
	var request databaseRowsImportRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Rows) < 1 || len(request.Rows) > repository.DatabaseRowBulkImportMaxRows {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "rows must contain between 1 and 1000 items")
		return
	}
	inputs := make([]repository.DatabaseBulkRowInput, 0, len(request.Rows))
	for index, row := range request.Rows {
		data, err := decodeRowObject(row.Data)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "rows["+strconv.Itoa(index)+"].data: "+err.Error())
			return
		}
		id := uuid.Nil
		if row.ID != nil {
			id = *row.ID
			if id == uuid.Nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "rows["+strconv.Itoa(index)+"].id must be a non-zero UUID")
				return
			}
		}
		inputs = append(inputs, repository.DatabaseBulkRowInput{
			ID: id, Data: data, ReadPermissions: row.ReadPermissions,
			UpdatePermissions: row.UpdatePermissions, DeletePermissions: row.DeletePermissions,
		})
	}
	items, err := s.repo.CreateDatabaseRows(r.Context(), projectID, databaseID, tableID, databaseActorFrom(r), inputs)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"rows": items, "count": len(items)})
}
