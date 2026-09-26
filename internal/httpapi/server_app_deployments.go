package httpapi

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
	"github.com/Stealth-deplover/stealth/internal/appstore"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

func (s *Server) createAppDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	if s.apps == nil || !s.appsReady {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "App artifact storage is not ready")
		return
	}
	actor := appActorFrom(r)
	if err := s.repo.AuthorizeAppWrite(r.Context(), projectID, appID, actor); appResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be multipart/form-data")
		return
	}
	platform, err := appbuildspec.CurrentHostPlatform()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "App build platform is not supported on this worker")
		return
	}
	definition := appbuildspec.Spec{DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: platform}
	selectAfterBuild := false
	haveSource := false
	seenFields := map[string]bool{}
	deploymentID := uuid.Must(uuid.NewV7())
	var sourceName string
	var prepared appstore.PreparedArtifact
	preparedSet := false
	committed := false
	defer func() {
		if preparedSet && !committed {
			s.apps.Sources.Cleanup(&prepared)
		}
	}()
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			if isMaxBytesError(nextErr) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "App source archive exceeds the configured maximum size")
			} else {
				writeError(w, http.StatusBadRequest, "invalid_request", "invalid multipart upload")
			}
			return
		}
		field := part.FormName()
		if seenFields[field] {
			_ = part.Close()
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "multipart fields may only be provided once")
			return
		}
		seenFields[field] = true
		switch field {
		case "source":
			haveSource = true
			sourceName = part.FileName()
			if storage.ValidateFilename(sourceName) != nil || !supportedAppArchive(sourceName) {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "source must be a supported .zip, .tar, .tar.gz, or .tgz archive with a valid filename")
				return
			}
			prepared, err = s.apps.Sources.BeginUpload(r.Context(), projectID, appID, deploymentID, part, s.config.AppsMaxSourceArchiveBytes)
			preparedSet = err == nil
			_ = part.Close()
			if errors.Is(err, appstore.ErrTooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "App source archive exceeds the configured maximum size")
				return
			}
			if err != nil {
				if isMaxBytesError(err) {
					writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "App source archive exceeds the configured maximum size")
				} else {
					internalError(s, w, err)
				}
				return
			}
		case "dockerfile_path", "context_directory", "target":
			value, readErr := readFunctionMultipartField(part, appbuildspec.MaxRelativePathBytes)
			_ = part.Close()
			if readErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "build options are invalid")
				return
			}
			switch field {
			case "dockerfile_path":
				definition.DockerfilePath = value
			case "context_directory":
				definition.ContextDirectory = value
			case "target":
				if value != "" {
					definition.Target = &value
				}
			}
		case "select":
			value, readErr := readFunctionMultipartField(part, 16)
			_ = part.Close()
			if readErr != nil || (value != "true" && value != "false") {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "select must be true or false")
				return
			}
			selectAfterBuild = value == "true"
		default:
			_ = part.Close()
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "unknown App deployment upload field")
			return
		}
	}
	if !haveSource || !preparedSet {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source archive is required")
		return
	}
	if prepared.Size <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source archive must not be empty")
		return
	}
	definition, err = appbuildspec.Normalize(definition)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	cleanup := repository.ArtifactCleanupInput{ProjectID: projectID, StoreKind: repository.ArtifactCleanupAppSources, Operation: repository.ArtifactCleanupRelative, RelativePath: prepared.RelativePath}
	if err := s.repo.ReserveAppSourceUpload(r.Context(), projectID, appID, actor, prepared.Size, cleanup); err != nil {
		if appDeploymentResourceError(w, err) || appResourceError(w, err) {
			return
		}
		internalError(s, w, err)
		return
	}
	if err := s.apps.Sources.Commit(r.Context(), &prepared); err != nil {
		_ = s.repo.AbandonAppSourceUpload(r.Context(), cleanup)
		internalError(s, w, err)
		return
	}
	committed = true
	var createdBy *uuid.UUID
	if actor.Kind == repository.AppConsoleActor && actor.AccountID != uuid.Nil {
		accountID := actor.AccountID
		createdBy = &accountID
	}
	item, err := s.repo.CreateAppDeployment(r.Context(), deploymentID, projectID, appID, actor, repository.AppDeploymentInput{
		SourceName: sourceName, SourceSizeBytes: prepared.Size, SourceChecksum: prepared.Checksum,
		SourcePath: prepared.RelativePath, SourceReserved: true, BuildSpec: definition, Select: selectAfterBuild,
		CreatedByAccount: createdBy, PublishCleanup: &cleanup,
	})
	if err != nil {
		_ = s.repo.AbandonAppSourceUpload(r.Context(), cleanup)
		if appResourceError(w, err) || appDeploymentResourceError(w, err) {
			return
		}
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "an App deployment could not be created")
			return
		}
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]domain.AppDeployment{"deployment": item})
}

func (s *Server) listAppDeployments(w http.ResponseWriter, r *http.Request) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return
	}
	limit, rawCursor, ok := page(w, r)
	if !ok {
		return
	}
	var cursor *int64
	if rawCursor != "" {
		version, err := strconv.ParseInt(rawCursor, 10, 64)
		if err != nil || version < 1 {
			writeError(w, http.StatusBadRequest, "validation_error", "deployment cursor must be a positive version")
			return
		}
		cursor = &version
	}
	items, next, canManage, err := s.repo.ListAppDeployments(r.Context(), projectID, appID, appActorFrom(r), limit, cursor)
	if appResourceError(w, err) || appDeploymentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) getAppDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, appID, deploymentID, ok := appDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetAppDeployment(r.Context(), projectID, appID, deploymentID, appActorFrom(r))
	if appResourceError(w, err) || appDeploymentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.AppDeployment{"deployment": item})
}

func (s *Server) listAppBuildLogs(w http.ResponseWriter, r *http.Request) {
	projectID, appID, deploymentID, ok := appDeploymentPathIDs(w, r)
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
	items, err := s.repo.ListAppBuildLogs(r.Context(), projectID, appID, deploymentID, appActorFrom(r), limit, after)
	if appResourceError(w, err) || appDeploymentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	var next *string
	if len(items) == limit {
		value := strconv.FormatInt(items[len(items)-1].Sequence, 10)
		next = &value
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": items, "pagination": pagination{Limit: limit, NextCursor: next}})
}

func (s *Server) selectAppDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, appID, deploymentID, ok := appDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.SelectAppDeployment(r.Context(), projectID, appID, deploymentID, appActorFrom(r))
	if appResourceError(w, err) || appDeploymentResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.AppDeployment{"deployment": item})
}

func (s *Server) deleteAppDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, appID, deploymentID, ok := appDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeleteAppDeployment(r.Context(), projectID, appID, deploymentID, appActorFrom(r)); appResourceError(w, err) || appDeploymentResourceError(w, err) {
		return
	} else if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func appDeploymentPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	projectID, appID, ok := appPathIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	deploymentID, ok := pathUUID(w, r, "deploymentID")
	return projectID, appID, deploymentID, ok
}

func supportedAppArchive(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	return strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

func appDeploymentResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, repository.ErrAppArtifactQuotaExceeded):
		writeError(w, http.StatusConflict, "quota_exceeded", "App artifact quota would be exceeded")
	case errors.Is(err, repository.ErrAppDeploymentSelected):
		writeError(w, http.StatusConflict, "deployment_selected", "the selected App deployment cannot be deleted")
	case errors.Is(err, repository.ErrAppDeploymentRunning):
		writeError(w, http.StatusConflict, "deployment_building", "an App deployment with an active build cannot be deleted")
	case errors.Is(err, repository.ErrAppDeploymentNotReady):
		writeError(w, http.StatusConflict, "deployment_not_ready", "only a completed verified App image can be selected")
	case errors.Is(err, repository.ErrAppDeploymentAlreadySelected):
		writeError(w, http.StatusConflict, "already_selected", "the selected deployment is already the desired App release")
	case errors.Is(err, repository.ErrAppRollbackNotAvailable):
		writeError(w, http.StatusConflict, "rollback_not_available", "the deployment is not an eligible older release with a verified artifact")
	case errors.Is(err, repository.ErrAppRollbackGenerationLimit):
		writeError(w, http.StatusConflict, "generation_limit", "the App desired generation cannot be advanced")
	case errors.Is(err, repository.ErrInvalidAppDeployment), errors.Is(err, appbuildspec.ErrInvalidBuildSpec):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "App deployment settings are invalid")
	default:
		return false
	}
	return true
}
