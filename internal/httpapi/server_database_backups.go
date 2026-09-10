package httpapi

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

type databaseBackupCreateResponse struct {
	Backup domain.DatabaseBackup `json:"backup"`
}

type databaseBackupRestoreResponse struct {
	BackupID string                                 `json:"backup_id"`
	Result   repository.DatabaseBackupRestoreResult `json:"result"`
}

// listDatabaseBackups returns immutable logical snapshot metadata. Blob keys
// are intentionally never exposed; clients use the download action endpoint.
func (s *Server) listDatabaseBackups(w http.ResponseWriter, r *http.Request) {
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
		parsed, err := uuid.Parse(cursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "cursor must be a UUID")
			return
		}
		cursorID = &parsed
	}
	items, next, canManage, err := s.repo.ListDatabaseBackups(r.Context(), projectID, databaseID, databaseActorFrom(r), limit, cursorID)
	if databaseBackupResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	if !s.storageReady || s.storage == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "database backup storage is unavailable")
		return
	}
	maxRows := repository.DatabaseBackupDefaultMaxRows
	if raw := strings.TrimSpace(r.URL.Query().Get("max_rows")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > repository.DatabaseBackupMaxRows {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", fmt.Sprintf("max_rows must be between 1 and %d", repository.DatabaseBackupMaxRows))
			return
		}
		maxRows = parsed
	}
	_, payload, err := s.repo.BuildDatabaseBackup(r.Context(), projectID, databaseID, databaseActorFrom(r), maxRows)
	if databaseBackupResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	backupID := uuid.Must(uuid.NewV7())
	maxSize := s.config.StorageMaxFileSize
	if maxSize <= 0 || maxSize > repository.DatabaseBackupMaxBytes {
		maxSize = repository.DatabaseBackupMaxBytes
	}
	prepared, err := s.storage.BeginUploadWithLimit(r.Context(), projectID, databaseID, backupID, bytes.NewReader(payload), "application/json", maxSize)
	if errors.Is(err, storage.ErrTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "database backup exceeds the configured storage limit")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if err := s.storage.Commit(&prepared); err != nil {
		s.storage.Cleanup(&prepared)
		internalError(s, w, err)
		return
	}
	item, err := s.repo.CreateDatabaseBackup(r.Context(), backupID, projectID, databaseID, databaseActorFrom(r), prepared.RelativePath, prepared.Size, prepared.Checksum)
	if databaseBackupResourceError(w, err) {
		_ = s.storage.RemoveRelative(prepared.RelativePath)
		return
	}
	if err != nil {
		_ = s.storage.RemoveRelative(prepared.RelativePath)
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, databaseBackupCreateResponse{Backup: item})
}

func (s *Server) getDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	backupID, ok := pathUUID(w, r, "backupID")
	if !ok {
		return
	}
	item, _, err := s.repo.GetDatabaseBackup(r.Context(), projectID, databaseID, backupID, databaseActorFrom(r))
	if databaseBackupResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backup": item})
}

func (s *Server) downloadDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	backupID, ok := pathUUID(w, r, "backupID")
	if !ok {
		return
	}
	if !s.storageReady || s.storage == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "database backup storage is unavailable")
		return
	}
	item, path, err := s.repo.GetDatabaseBackup(r.Context(), projectID, databaseID, backupID, databaseActorFrom(r))
	if databaseBackupResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	blob, err := s.storage.OpenRelative(path)
	if errors.Is(err, storage.ErrInvalidPath) || errors.Is(err, io.ErrUnexpectedEOF) {
		internalError(s, w, errors.New("database backup blob is unavailable"))
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	defer blob.Close()
	payload, err := io.ReadAll(io.LimitReader(blob, repository.DatabaseBackupMaxBytes+1))
	if err != nil {
		internalError(s, w, err)
		return
	}
	if len(payload) < 1 || len(payload) > repository.DatabaseBackupMaxBytes || int64(len(payload)) != item.SizeBytes || subtle.ConstantTimeCompare([]byte(repository.BackupChecksum(payload)), []byte(item.ChecksumSHA256)) != 1 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", "database backup blob does not match its metadata")
		return
	}
	if _, err := blob.Seek(0, io.SeekStart); err != nil {
		internalError(s, w, err)
		return
	}
	filename := "database-backup-" + item.ID + ".json"
	contentDisposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if contentDisposition == "" {
		contentDisposition = `attachment; filename="` + filename + `"`
	}
	w.Header().Set("Content-Disposition", contentDisposition)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filename, item.CreatedAt, blob)
}

func (s *Server) restoreDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	backupID, ok := pathUUID(w, r, "backupID")
	if !ok {
		return
	}
	if !s.storageReady || s.storage == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "database backup storage is unavailable")
		return
	}
	item, path, err := s.repo.GetDatabaseBackup(r.Context(), projectID, databaseID, backupID, databaseActorFrom(r))
	if databaseBackupResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	blob, err := s.storage.OpenRelative(path)
	if err != nil {
		internalError(s, w, err)
		return
	}
	defer blob.Close()
	payload, err := io.ReadAll(io.LimitReader(blob, repository.DatabaseBackupMaxBytes+1))
	if err != nil {
		internalError(s, w, err)
		return
	}
	if len(payload) < 1 || len(payload) > repository.DatabaseBackupMaxBytes || int64(len(payload)) != item.SizeBytes {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", "database backup size does not match its metadata")
		return
	}
	checksum := repository.BackupChecksum(payload)
	if subtle.ConstantTimeCompare([]byte(checksum), []byte(item.ChecksumSHA256)) != 1 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", "database backup checksum does not match its metadata")
		return
	}
	var snapshot repository.DatabaseBackupSnapshot
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&snapshot); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", "database backup payload is invalid")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", "database backup payload contains trailing data")
		return
	}
	result, err := s.repo.RestoreDatabaseBackup(r.Context(), projectID, databaseID, databaseActorFrom(r), snapshot)
	if databaseBackupResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, databaseBackupRestoreResponse{BackupID: backupID.String(), Result: result})
}

func (s *Server) deleteDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	databaseID, ok := pathUUID(w, r, "databaseID")
	if !ok {
		return
	}
	backupID, ok := pathUUID(w, r, "backupID")
	if !ok {
		return
	}
	if !s.storageReady || s.storage == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "database backup storage is unavailable")
		return
	}
	path, err := s.repo.DeleteDatabaseBackup(r.Context(), projectID, databaseID, backupID, databaseActorFrom(r))
	if databaseBackupResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if err := s.storage.RemoveRelative(path); err != nil {
		internalError(s, w, fmt.Errorf("remove deleted database backup blob: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func databaseBackupResourceError(w http.ResponseWriter, err error) bool {
	if databaseResourceError(w, err) {
		return true
	}
	switch {
	case errors.Is(err, repository.ErrBackupTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "database backup exceeds the configured safety limit")
		return true
	case errors.Is(err, repository.ErrInvalidBackup):
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", "database backup is invalid")
		return true
	}
	return false
}
