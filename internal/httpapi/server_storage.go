package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/apikey"
	"github.com/nazxf/stealth-api/internal/auth"
	"github.com/nazxf/stealth-api/internal/database"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/storage"
	"github.com/nazxf/stealth-api/internal/validate"
)

const projectStorageActorContextKey contextKey = "project-storage-actor"

// requireProjectStorageActor applies the same explicit actor precedence as
// Database data routes: X-Stealth-Key, project app cookie, Console cookie,
// then anonymous. Invalid credentials stop the request; they never fall
// through to a weaker actor.
func (s *Server) requireProjectStorageActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID, err := repository.ParseUUID(chi.URLParam(r, "projectID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "projectID must be a UUID")
			return
		}
		if secret := r.Header.Get("X-Stealth-Key"); secret != "" {
			if err := apikey.ValidateSecret(secret); err != nil {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			key, err := s.repo.AuthenticateProjectAPIKey(r.Context(), projectID, apikey.HashSecret(secret))
			if errors.Is(err, repository.ErrNotFound) {
				if !s.allowFailedProjectAPIKeyAuth(w, r, projectID) {
					return
				}
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
			keyID, err := repository.ParseUUID(key.ID)
			if err != nil {
				internalError(s, w, err)
				return
			}
			if err := s.repo.TouchProjectAPIKey(r.Context(), keyID); err != nil {
				internalError(s, w, err)
				return
			}
			ctx := context.WithValue(r.Context(), projectStorageActorContextKey, repository.StorageActor{Kind: repository.StorageAPIKeyActor, APIKeyID: keyID, APIKeyScopes: key.Scopes})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if cookie, err := r.Cookie(projectSessionCookieName(projectID)); err == nil && cookie.Value != "" {
			user, _, err := s.repo.ApplicationUserBySession(r.Context(), projectID, auth.HashSessionToken(cookie.Value))
			if errors.Is(err, repository.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "application authentication is required")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
			ctx := context.WithValue(r.Context(), projectStorageActorContextKey, repository.StorageActor{Kind: repository.StorageApplicationActor, ProjectUserID: mustUUID(user.ID)})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if cookie, err := r.Cookie(s.config.SessionCookieName); err == nil && cookie.Value != "" {
			account, sessionID, err := s.repo.AccountBySession(r.Context(), auth.HashSessionToken(cookie.Value))
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
				return
			}
			ctx := context.WithValue(r.Context(), accountContextKey, account)
			ctx = context.WithValue(ctx, sessionContextKey, sessionID)
			ctx = context.WithValue(ctx, projectStorageActorContextKey, repository.StorageActor{Kind: repository.StorageConsoleActor, AccountID: mustUUID(account.ID)})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		ctx := context.WithValue(r.Context(), projectStorageActorContextKey, repository.StorageActor{Kind: repository.StorageAnonymousActor})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func storageActorFrom(r *http.Request) repository.StorageActor {
	if actor, ok := r.Context().Value(projectStorageActorContextKey).(repository.StorageActor); ok {
		return actor
	}
	if actor, ok := r.Context().Value(projectActorContextKey).(projectActor); ok {
		if actor.kind == apiKeyProjectActor {
			return repository.StorageActor{Kind: repository.StorageAPIKeyActor, APIKeyID: actor.apiKeyID, APIKeyScopes: actor.scopes}
		}
		return repository.StorageActor{Kind: repository.StorageConsoleActor, AccountID: mustUUID(accountFrom(r).ID)}
	}
	return repository.StorageActor{Kind: repository.StorageAnonymousActor}
}

func managementStorageActorFrom(r *http.Request) repository.StorageActor {
	return storageActorFrom(r)
}

type storageBucketRequest struct {
	Name              string    `json:"name"`
	FileSecurity      *bool     `json:"file_security"`
	CreatePermissions *[]string `json:"create_permissions"`
	ReadPermissions   *[]string `json:"read_permissions"`
	UpdatePermissions *[]string `json:"update_permissions"`
	DeletePermissions *[]string `json:"delete_permissions"`
	// write_permissions is a compatibility alias for clients that model a
	// single write grant. It maps to both create and update when those fields
	// are omitted explicitly.
	WritePermissions *[]string `json:"write_permissions"`
	MaxFileSizeBytes *int64    `json:"max_file_size_bytes"`
	QuotaBytes       *int64    `json:"quota_bytes"`
}

func (s *Server) listStorageBuckets(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
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
	items, next, canManage, err := s.repo.ListStorageBuckets(r.Context(), projectID, managementStorageActorFrom(r), limit, cursorID)
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createStorageBucket(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req storageBucketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := validate.Slug(req.Name, "name")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	fileSecurity := true
	if req.FileSecurity != nil {
		fileSecurity = *req.FileSecurity
	}
	quota := s.config.StorageDefaultQuotaBytes
	if req.QuotaBytes != nil {
		quota = *req.QuotaBytes
	}
	maxFileSize := s.config.StorageMaxFileSize
	if req.MaxFileSizeBytes != nil {
		maxFileSize = *req.MaxFileSizeBytes
	}
	if quota <= 0 || maxFileSize <= 0 || maxFileSize > s.config.StorageMaxFileSize || maxFileSize > quota {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "max_file_size_bytes must be positive, within STORAGE_MAX_FILE_SIZE, and no larger than quota_bytes")
		return
	}
	createPermissions, readPermissions, updatePermissions, deletePermissions, err := bucketPermissionsFromRequest(req, false)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateStorageBucket(r.Context(), uuid.Must(uuid.NewV7()), projectID, managementStorageActorFrom(r), repository.StorageBucketInput{
		Name: name, FileSecurity: fileSecurity, CreatePermissions: createPermissions, ReadPermissions: readPermissions, UpdatePermissions: updatePermissions, DeletePermissions: deletePermissions, MaxFileSizeBytes: maxFileSize, QuotaBytes: quota,
	})
	if planLimitError(w, err) {
		return
	}
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.StorageBucket{"bucket": item})
}

func bucketPermissionsFromRequest(req storageBucketRequest, update bool) ([]string, []string, []string, []string, error) {
	if req.WritePermissions != nil {
		if req.CreatePermissions != nil || req.UpdatePermissions != nil {
			return nil, nil, nil, nil, errors.New("write_permissions cannot be combined with create_permissions or update_permissions")
		}
		req.CreatePermissions = req.WritePermissions
		req.UpdatePermissions = req.WritePermissions
	}
	if update {
		return dereferencePermissions(req.CreatePermissions), dereferencePermissions(req.ReadPermissions), dereferencePermissions(req.UpdatePermissions), dereferencePermissions(req.DeletePermissions), nil
	}
	return permissionsOrEmpty(req.CreatePermissions), permissionsOrEmpty(req.ReadPermissions), permissionsOrEmpty(req.UpdatePermissions), permissionsOrEmpty(req.DeletePermissions), nil
}

func permissionsOrEmpty(value *[]string) []string {
	if value == nil {
		return []string{}
	}
	return append([]string(nil), (*value)...)
}

func dereferencePermissions(value *[]string) []string {
	if value == nil {
		return nil
	}
	return append([]string(nil), (*value)...)
}

func (s *Server) getStorageBucket(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	item, err := s.repo.GetStorageBucket(r.Context(), projectID, bucketID, managementStorageActorFrom(r))
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.StorageBucket{"bucket": item})
}

func (s *Server) updateStorageBucket(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	var req storageBucketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" && req.FileSecurity == nil && req.CreatePermissions == nil && req.ReadPermissions == nil && req.UpdatePermissions == nil && req.DeletePermissions == nil && req.WritePermissions == nil && req.MaxFileSizeBytes == nil && req.QuotaBytes == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "at least one bucket setting is required")
		return
	}
	var name *string
	if req.Name != "" {
		validated, err := validate.Slug(req.Name, "name")
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		name = &validated
	}
	if req.MaxFileSizeBytes != nil && (*req.MaxFileSizeBytes <= 0 || *req.MaxFileSizeBytes > s.config.StorageMaxFileSize) {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "max_file_size_bytes must be within STORAGE_MAX_FILE_SIZE")
		return
	}
	if req.QuotaBytes != nil && *req.QuotaBytes <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "quota_bytes must be positive")
		return
	}
	createPermissions, readPermissions, updatePermissions, deletePermissions, err := bucketPermissionsFromRequest(req, true)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if req.WritePermissions != nil {
		// bucketPermissionsFromRequest normalizes the compatibility alias on a
		// copy; retain presence information so PATCH does not silently ignore it.
		req.CreatePermissions = req.WritePermissions
		req.UpdatePermissions = req.WritePermissions
	}
	item, err := s.repo.UpdateStorageBucket(r.Context(), projectID, bucketID, managementStorageActorFrom(r), repository.StorageBucketPatch{Name: name, FileSecurity: req.FileSecurity, CreatePermissions: permissionPointer(req.CreatePermissions, createPermissions), ReadPermissions: permissionPointer(req.ReadPermissions, readPermissions), UpdatePermissions: permissionPointer(req.UpdatePermissions, updatePermissions), DeletePermissions: permissionPointer(req.DeletePermissions, deletePermissions), MaxFileSizeBytes: req.MaxFileSizeBytes, QuotaBytes: req.QuotaBytes})
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.StorageBucket{"bucket": item})
}

func permissionPointer(original *[]string, value []string) *[]string {
	if original == nil {
		return nil
	}
	return &value
}

func (s *Server) deleteStorageBucket(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	paths, err := s.repo.DeleteStorageBucket(r.Context(), projectID, bucketID, managementStorageActorFrom(r))
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if s.storage == nil {
		internalError(s, w, errors.New("storage is unavailable"))
		return
	}
	for _, path := range paths {
		if err := s.storage.RemoveRelative(path); err != nil {
			internalError(s, w, fmt.Errorf("remove deleted storage blob: %w", err))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listStorageFiles(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
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
	items, next, canManage, err := s.repo.ListStorageFiles(r.Context(), projectID, bucketID, storageActorFrom(r), limit, cursorID)
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) uploadStorageFile(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	actor := storageActorFrom(r)
	bucket, err := s.repo.AuthorizeStorageBucket(r.Context(), projectID, bucketID, actor, "create")
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if s.storage == nil {
		internalError(s, w, errors.New("storage is unavailable"))
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be multipart/form-data")
		return
	}
	fileID := uuid.Must(uuid.NewV7())
	var prepared storage.PreparedFile
	var hasFile bool
	var partFilename string
	var explicitName string
	var readPermissions, updatePermissions, deletePermissions *[]string
	seenFields := make(map[string]struct{}, 4)
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cleanupPrepared(s.storage, &prepared)
			if isMaxBytesError(nextErr) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the configured upload limit")
			} else {
				writeError(w, http.StatusBadRequest, "invalid_request", "multipart body is invalid")
			}
			return
		}
		field := part.FormName()
		if _, seen := seenFields[field]; seen {
			cleanupPrepared(s.storage, &prepared)
			writeError(w, http.StatusBadRequest, "invalid_request", "multipart fields may occur only once")
			return
		}
		seenFields[field] = struct{}{}
		switch field {
		case "file":
			if hasFile {
				cleanupPrepared(s.storage, &prepared)
				writeError(w, http.StatusBadRequest, "invalid_request", "multipart body may contain only one file field")
				return
			}
			hasFile = true
			filename := part.FileName()
			if filename != "" {
				if filenameErr := storage.ValidateFilename(filename); filenameErr != nil {
					writeError(w, http.StatusUnprocessableEntity, "validation_error", "filename is invalid")
					return
				}
				partFilename = filename
			}
			declaredType := part.Header.Get("Content-Type")
			prepared, err = s.storage.BeginUploadWithLimit(r.Context(), projectID, bucketID, fileID, part, declaredType, bucket.MaxFileSizeBytes)
			if err != nil {
				cleanupPrepared(s.storage, &prepared)
				if isMaxBytesError(err) || errors.Is(err, storage.ErrTooLarge) {
					writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "file exceeds the configured maximum size")
				} else if errors.Is(err, storage.ErrInvalidMIME) {
					writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "file MIME type is invalid")
				} else {
					internalError(s, w, err)
				}
				return
			}
		case "name", "read_permissions", "update_permissions", "delete_permissions":
			value, readErr := readMultipartField(part, 16*1024)
			if readErr != nil {
				cleanupPrepared(s.storage, &prepared)
				if isMaxBytesError(readErr) {
					writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "multipart field is too large")
				} else {
					writeError(w, http.StatusBadRequest, "invalid_request", "multipart field is invalid")
				}
				return
			}
			switch field {
			case "name":
				explicitName = strings.TrimSpace(value)
			case "read_permissions":
				readPermissions, err = parseStoragePermissionsField(value)
			case "update_permissions":
				updatePermissions, err = parseStoragePermissionsField(value)
			case "delete_permissions":
				deletePermissions, err = parseStoragePermissionsField(value)
			}
			if err != nil {
				cleanupPrepared(s.storage, &prepared)
				writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
				return
			}
		default:
			cleanupPrepared(s.storage, &prepared)
			writeError(w, http.StatusBadRequest, "invalid_request", "unsupported multipart field")
			return
		}
	}
	if !hasFile {
		writeError(w, http.StatusBadRequest, "invalid_request", "multipart body must include a file field")
		return
	}
	filenameField := partFilename
	if explicitName != "" {
		if partFilename != "" {
			cleanupPrepared(s.storage, &prepared)
			writeError(w, http.StatusBadRequest, "invalid_request", "name cannot be combined with a multipart filename")
			return
		}
		filenameField = explicitName
	}
	if err := storage.ValidateFilename(filenameField); err != nil {
		cleanupPrepared(s.storage, &prepared)
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "filename is invalid")
		return
	}
	if actor.Kind == repository.StorageAnonymousActor && (readPermissions == nil || updatePermissions == nil || deletePermissions == nil) {
		cleanupPrepared(s.storage, &prepared)
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "anonymous uploads must specify read_permissions, update_permissions, and delete_permissions")
		return
	}
	// Publish first: a DB failure can leave an invisible orphan that can be
	// reconciled, whereas a committed metadata row without bytes is visible.
	if err := s.storage.Commit(&prepared); err != nil {
		cleanupPrepared(s.storage, &prepared)
		internalError(s, w, err)
		return
	}
	removeOnFailure := true
	defer func() {
		if removeOnFailure {
			if cleanupErr := s.storage.RemoveRelative(prepared.RelativePath); cleanupErr != nil {
				s.logger.Error("failed to clean rejected storage blob", "path", prepared.RelativePath, "error", cleanupErr)
			}
		}
	}()
	var creator *uuid.UUID
	if actor.Kind == repository.StorageApplicationActor {
		creatorID := actor.ProjectUserID
		creator = &creatorID
	}
	item, err := s.repo.CreateStorageFile(r.Context(), fileID, projectID, bucketID, actor, repository.StorageFileInput{Name: filenameField, MimeType: prepared.ContentType, SizeBytes: prepared.Size, ChecksumSHA256: prepared.Checksum, StoragePath: prepared.RelativePath, ReadPermissions: readPermissions, UpdatePermissions: updatePermissions, DeletePermissions: deletePermissions, CreatorProjectUserID: creator})
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	removeOnFailure = false
	writeJSON(w, http.StatusCreated, map[string]domain.StorageFile{"file": item})
}

func cleanupPrepared(store storage.BlobStore, prepared *storage.PreparedFile) {
	if store != nil {
		store.Cleanup(prepared)
	}
}

func readMultipartField(part *multipart.Part, max int64) (string, error) {
	value, err := io.ReadAll(io.LimitReader(part, max+1))
	if err != nil {
		return "", err
	}
	if int64(len(value)) > max {
		return "", &http.MaxBytesError{Limit: max}
	}
	return string(value), nil
}

func parseStoragePermissionsField(raw string) (*[]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		values := []string{}
		return &values, nil
	}
	var values []string
	if strings.HasPrefix(raw, "[") {
		decoder := json.NewDecoder(strings.NewReader(raw))
		if err := decoder.Decode(&values); err != nil {
			return nil, errors.New("permissions must be a JSON array or comma-separated list")
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, errors.New("permissions must contain one JSON value")
		}
	} else {
		for _, value := range strings.Split(raw, ",") {
			if strings.TrimSpace(value) != "" {
				values = append(values, strings.TrimSpace(value))
			}
		}
	}
	return &values, nil
}

func isMaxBytesError(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

func (s *Server) getStorageFile(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	fileID, ok := pathUUID(w, r, "fileID")
	if !ok {
		return
	}
	item, err := s.repo.GetStorageFile(r.Context(), projectID, bucketID, fileID, storageActorFrom(r))
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.StorageFile{"file": item})
}

type storageFilePatchRequest struct {
	Name              *string   `json:"name"`
	ReadPermissions   *[]string `json:"read_permissions"`
	UpdatePermissions *[]string `json:"update_permissions"`
	DeletePermissions *[]string `json:"delete_permissions"`
}

func (s *Server) updateStorageFile(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	fileID, ok := pathUUID(w, r, "fileID")
	if !ok {
		return
	}
	var req storageFilePatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.ReadPermissions == nil && req.UpdatePermissions == nil && req.DeletePermissions == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "at least one file setting is required")
		return
	}
	if req.Name != nil {
		if err := storage.ValidateFilename(*req.Name); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "filename is invalid")
			return
		}
	}
	item, err := s.repo.UpdateStorageFile(r.Context(), projectID, bucketID, fileID, storageActorFrom(r), repository.StorageFilePatch{Name: req.Name, ReadPermissions: req.ReadPermissions, UpdatePermissions: req.UpdatePermissions, DeletePermissions: req.DeletePermissions})
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.StorageFile{"file": item})
}

func (s *Server) downloadStorageFile(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	fileID, ok := pathUUID(w, r, "fileID")
	if !ok {
		return
	}
	if s.storage == nil {
		internalError(s, w, errors.New("storage is unavailable"))
		return
	}
	item, path, err := s.repo.StorageFileForDownload(r.Context(), projectID, bucketID, fileID, storageActorFrom(r))
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	file, err := s.storage.OpenRelative(path)
	if errors.Is(err, os.ErrNotExist) {
		internalError(s, w, errors.New("storage metadata references a missing blob"))
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	defer file.Close()
	contentDisposition := "attachment"
	if formatted := mime.FormatMediaType("attachment", map[string]string{"filename": item.Name}); formatted != "" {
		contentDisposition = formatted
	}
	w.Header().Set("Content-Disposition", contentDisposition)
	w.Header().Set("Content-Type", item.MimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, item.Name, item.CreatedAt, file)
}

func (s *Server) deleteStorageFile(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	bucketID, ok := pathUUID(w, r, "bucketID")
	if !ok {
		return
	}
	fileID, ok := pathUUID(w, r, "fileID")
	if !ok {
		return
	}
	path, err := s.repo.DeleteStorageFile(r.Context(), projectID, bucketID, fileID, storageActorFrom(r))
	if storageResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if s.storage == nil {
		internalError(s, w, errors.New("storage is unavailable"))
		return
	}
	if err := s.storage.RemoveRelative(path); err != nil {
		internalError(s, w, fmt.Errorf("remove deleted storage blob: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func storageResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, repository.ErrRowHidden):
		writeError(w, http.StatusNotFound, "not_found", "storage resource was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to access this storage resource")
		return true
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "storage resource conflicts with an existing resource")
		return true
	case errors.Is(err, repository.ErrStorageQuotaExceeded):
		writeError(w, http.StatusRequestEntityTooLarge, "storage_quota_exceeded", "storage bucket quota would be exceeded")
		return true
	case errors.Is(err, repository.ErrStorageFileTooLarge), errors.Is(err, storage.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "file exceeds the configured maximum size")
		return true
	case errors.Is(err, database.ErrInvalidPermissions), errors.Is(err, database.ErrDuplicatePermission), errors.Is(err, storage.ErrInvalidFilename), errors.Is(err, storage.ErrInvalidMIME):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return true
	}
	return false
}
