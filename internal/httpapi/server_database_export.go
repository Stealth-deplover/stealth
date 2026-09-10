package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

// exportDatabaseRows streams a permission-filtered table snapshot. JSON keeps
// the normal DatabaseRow shape, while CSV uses JSON literals in data cells so
// importing a number, boolean, datetime, or object does not silently turn it
// into a string.
func (s *Server) exportDatabaseRows(w http.ResponseWriter, r *http.Request) {
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
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "format must be json or csv")
		return
	}
	limit, err := parseDatabaseExportLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeDatabaseQueryError(w, err)
		return
	}
	actor := databaseActorFrom(r)
	schema, err := s.repo.DatabaseTableSchema(r.Context(), projectID, databaseID, tableID, actor)
	if databaseResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}

	filename := "rows-" + tableID.String() + "." + format
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if format == "csv" {
		s.exportDatabaseRowsCSV(w, r, projectID, databaseID, tableID, actor, schema, limit)
		return
	}
	s.exportDatabaseRowsJSON(w, r, projectID, databaseID, tableID, actor, limit)
}

func parseDatabaseExportLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return repository.DatabaseRowExportDefaultLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > repository.DatabaseRowExportMaxLimit {
		return 0, fmt.Errorf("%w: export limit must be between 1 and %d", repository.ErrInvalidQuery, repository.DatabaseRowExportMaxLimit)
	}
	return limit, nil
}

func (s *Server) exportDatabaseRowsJSON(w http.ResponseWriter, r *http.Request, projectID, databaseID, tableID uuid.UUID, actor repository.DatabaseActor, limit int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"rows":[`)); err != nil {
		s.logger.Warn("database export response write failed", "table_id", tableID, "error", err)
		return
	}
	first := true
	count, err := s.repo.StreamDatabaseRows(r.Context(), projectID, databaseID, tableID, actor, limit, func(row domain.DatabaseRow) error {
		if !first {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		_, err = w.Write(encoded)
		return err
	})
	if err != nil {
		s.logger.Warn("database JSON export failed", "table_id", tableID, "count", count, "error", err)
		return
	}
	if _, err := w.Write([]byte(`],"count":` + strconv.Itoa(count) + `}`)); err != nil {
		s.logger.Warn("database export response write failed", "table_id", tableID, "error", err)
	}
}

func (s *Server) exportDatabaseRowsCSV(w http.ResponseWriter, r *http.Request, projectID, databaseID, tableID uuid.UUID, actor repository.DatabaseActor, schema repository.DatabaseTableSchema, limit int) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	header := []string{"id", "project_id", "table_id", "created_at", "updated_at"}
	for _, column := range schema.Columns {
		header = append(header, column.Key)
	}
	if err := writer.Write(header); err != nil {
		s.logger.Warn("database CSV export failed", "table_id", tableID, "error", err)
		return
	}
	count, err := s.repo.StreamDatabaseRows(r.Context(), projectID, databaseID, tableID, actor, limit, func(row domain.DatabaseRow) error {
		record := []string{row.ID, row.ProjectID, row.TableID, row.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), row.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
		for _, column := range schema.Columns {
			value, exists := row.Data[column.Key]
			if !exists {
				record = append(record, "")
				continue
			}
			encoded, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return marshalErr
			}
			record = append(record, string(encoded))
		}
		return writer.Write(record)
	})
	writer.Flush()
	if err != nil || writer.Error() != nil {
		if err == nil {
			err = writer.Error()
		}
		s.logger.Warn("database CSV export failed", "table_id", tableID, "count", count, "error", err)
	}
}
