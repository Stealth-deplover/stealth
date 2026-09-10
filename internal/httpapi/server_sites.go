package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionrunner"
	"github.com/Stealth-deplover/stealth/internal/functionstore"
	"github.com/Stealth-deplover/stealth/internal/gitarchive"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/sitestore"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type siteRequest struct {
	Name               *string `json:"name"`
	Framework          *string `json:"framework"`
	Enabled            *bool   `json:"enabled"`
	Status             *string `json:"status"`
	ArtifactQuotaBytes *int64  `json:"artifact_quota_bytes"`
}

const maxSiteBuildCommandBytes = 4000

type siteBuildOptions struct {
	Runtime         string
	Command         string
	OutputDirectory string
}

type siteGitDeploymentRequest struct {
	Repository      string `json:"repository"`
	Ref             string `json:"ref"`
	BuildRuntime    string `json:"build_runtime"`
	BuildCommand    string `json:"build_command"`
	OutputDirectory string `json:"output_directory"`
	Activate        *bool  `json:"activate"`
}

func siteActorFrom(r *http.Request) repository.SiteActor {
	actor, ok := r.Context().Value(projectActorContextKey).(projectActor)
	if !ok {
		return repository.SiteActor{}
	}
	if actor.kind == apiKeyProjectActor {
		return repository.SiteActor{Kind: repository.SiteAPIKeyActor, APIKeyID: actor.apiKeyID, APIKeyScopes: actor.scopes}
	}
	account, ok := r.Context().Value(accountContextKey).(domain.Account)
	if !ok {
		return repository.SiteActor{}
	}
	return repository.SiteActor{Kind: repository.SiteConsoleActor, AccountID: mustUUID(account.ID)}
}

func parseSiteCreateRequest(s *Server, req siteRequest) (repository.SiteInput, error) {
	if req.Name == nil {
		return repository.SiteInput{}, errors.New("name is required")
	}
	name, err := validateSiteName(*req.Name)
	if err != nil {
		return repository.SiteInput{}, err
	}
	framework := "static"
	if req.Framework != nil {
		framework = strings.ToLower(strings.TrimSpace(*req.Framework))
	}
	if framework != "static" {
		return repository.SiteInput{}, errors.New("framework must be static")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	status := "active"
	if req.Status != nil {
		status = strings.ToLower(strings.TrimSpace(*req.Status))
	}
	if req.Enabled != nil && req.Status == nil {
		if enabled {
			status = "active"
		} else {
			status = "disabled"
		}
	} else if req.Enabled == nil && req.Status != nil {
		enabled = status == "active"
	}
	if status != "active" && status != "disabled" || (status == "active") != enabled {
		return repository.SiteInput{}, errors.New("status and enabled must describe an active or disabled site consistently")
	}
	quota := s.config.SitesDefaultQuotaBytes
	if req.ArtifactQuotaBytes != nil {
		quota = *req.ArtifactQuotaBytes
	}
	if quota <= 0 {
		return repository.SiteInput{}, errors.New("artifact_quota_bytes must be positive")
	}
	return repository.SiteInput{Name: name, Framework: framework, Enabled: enabled, Status: status, ArtifactQuotaBytes: quota}, nil
}

func parseSitePatchRequest(req siteRequest) (repository.SitePatch, error) {
	patch := repository.SitePatch{}
	changed := false
	if req.Name != nil {
		value, err := validateSiteName(*req.Name)
		if err != nil {
			return patch, err
		}
		patch.Name = &value
		changed = true
	}
	if req.Framework != nil {
		value := strings.ToLower(strings.TrimSpace(*req.Framework))
		if value != "static" {
			return patch, errors.New("framework must be static")
		}
		patch.Framework = &value
		changed = true
	}
	if req.Enabled != nil {
		patch.Enabled = req.Enabled
		changed = true
	}
	if req.Status != nil {
		value := strings.ToLower(strings.TrimSpace(*req.Status))
		if value != "active" && value != "disabled" {
			return patch, errors.New("status must be active or disabled")
		}
		patch.Status = &value
		changed = true
	}
	if req.ArtifactQuotaBytes != nil {
		if *req.ArtifactQuotaBytes <= 0 {
			return patch, errors.New("artifact_quota_bytes must be positive")
		}
		patch.ArtifactQuotaBytes = req.ArtifactQuotaBytes
		changed = true
	}
	if !changed {
		return patch, errors.New("at least one site setting is required")
	}
	return patch, nil
}

func validateSiteName(value string) (string, error) {
	return validate.Slug(value, "name")
}

func parseSiteBuildOptions(runtime, command, outputDirectory string) (siteBuildOptions, error) {
	command = strings.TrimSpace(command)
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	outputDirectory = strings.TrimSpace(outputDirectory)
	if command == "" && runtime == "" && outputDirectory == "" {
		return siteBuildOptions{}, nil
	}
	if command == "" {
		return siteBuildOptions{}, errors.New("build_command is required when build options are provided")
	}
	if len(command) > maxSiteBuildCommandBytes || strings.ContainsRune(command, 0) {
		return siteBuildOptions{}, errors.New("build_command must be at most 4000 bytes and cannot contain NUL")
	}
	if runtime == "" {
		runtime = "node-22"
	}
	switch runtime {
	case "node-22", "python-3.13", "go-1.24":
	default:
		return siteBuildOptions{}, errors.New("build_runtime must be one of node-22, python-3.13, or go-1.24")
	}
	if outputDirectory == "" {
		outputDirectory = "."
	}
	if len(outputDirectory) > 255 || strings.HasPrefix(outputDirectory, "/") || strings.ContainsAny(outputDirectory, "\\\x00\r\n") {
		return siteBuildOptions{}, errors.New("output_directory must be a safe relative path")
	}
	if outputDirectory != "." {
		for _, part := range strings.Split(outputDirectory, "/") {
			if part == "" || part == "." || part == ".." {
				return siteBuildOptions{}, errors.New("output_directory must be a safe relative path")
			}
			for _, char := range part {
				if !(char == '-' || char == '_' || char == '.' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
					return siteBuildOptions{}, errors.New("output_directory must be a safe relative path")
				}
			}
		}
	}
	return siteBuildOptions{Runtime: runtime, Command: command, OutputDirectory: outputDirectory}, nil
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
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
	items, next, canManage, err := s.repo.ListSites(r.Context(), projectID, siteActorFrom(r), limit, cursorID)
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) getSite(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetSite(r.Context(), projectID, siteID, siteActorFrom(r))
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Site{"site": item})
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	var req siteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	input, err := parseSiteCreateRequest(s, req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.CreateSite(r.Context(), uuid.Must(uuid.NewV7()), projectID, siteActorFrom(r), input)
	if planLimitError(w, err) {
		return
	}
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.Site{"site": item})
}

func (s *Server) updateSite(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return
	}
	var req siteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	patch, err := parseSitePatchRequest(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	item, err := s.repo.UpdateSite(r.Context(), projectID, siteID, siteActorFrom(r), patch)
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.Site{"site": item})
}

func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return
	}
	paths, err := s.repo.DeleteSite(r.Context(), projectID, siteID, siteActorFrom(r))
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if s.sites == nil {
		internalError(s, w, errors.New("site artifact storage is unavailable"))
		return
	}
	for _, pathsItem := range paths {
		if err := s.sites.RemoveRelative(pathsItem.ArtifactPath); err != nil {
			internalError(s, w, fmt.Errorf("remove deleted site artifact: %w", err))
			return
		}
		if pathsItem.SourcePath != "" && s.siteArchives != nil {
			if err := s.siteArchives.RemoveRelative(pathsItem.SourcePath); err != nil {
				internalError(s, w, fmt.Errorf("remove deleted site source: %w", err))
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSiteDeployments(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, ok := sitePathIDs(w, r)
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
	items, next, canManage, err := s.repo.ListSiteDeployments(r.Context(), projectID, siteID, siteActorFrom(r), limit, cursorID)
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) listSiteBuildLogs(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, deploymentID, ok := siteDeploymentPathIDs(w, r)
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
	items, err := s.repo.ListSiteBuildLogs(r.Context(), projectID, siteID, deploymentID, siteActorFrom(r), limit, after)
	if siteResourceError(w, err) {
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

func (s *Server) getSiteDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, deploymentID, ok := siteDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetSiteDeployment(r.Context(), projectID, siteID, deploymentID, siteActorFrom(r))
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.SiteDeployment{"deployment": item})
}

func (s *Server) uploadSiteDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return
	}
	if s.siteArchives == nil || s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "site artifact storage is not ready")
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be multipart/form-data")
		return
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	deploymentID := uuid.Must(uuid.NewV7())
	var prepared functionstore.PreparedArtifact
	var sourceFilename, sourceNameOverride string
	var buildRuntime, buildCommand, outputDirectory string
	activate := true
	haveSource, haveActivate := false, false
	sourceCommitted := false
	defer func() {
		if !sourceCommitted {
			s.siteArchives.Cleanup(&prepared)
		}
	}()
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			if isMaxBytesError(nextErr) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the configured upload limit")
			} else {
				writeError(w, http.StatusBadRequest, "invalid_request", "invalid multipart upload")
			}
			return
		}
		switch part.FormName() {
		case "source":
			if haveSource {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "source may only be provided once")
				return
			}
			haveSource = true
			sourceFilename = part.FileName()
			if sourceFilename != "" {
				if err := storage.ValidateFilename(sourceFilename); err != nil {
					_ = part.Close()
					writeError(w, http.StatusUnprocessableEntity, "validation_error", "source filename is invalid")
					return
				}
			}
			prepared, err = s.siteArchives.BeginUploadWithLimit(r.Context(), projectID, siteID, deploymentID, part, s.config.SitesMaxArtifactSize)
			_ = part.Close()
			if errors.Is(err, functionstore.ErrTooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "site archive exceeds the configured maximum size")
				return
			}
			if err != nil {
				internalError(s, w, err)
				return
			}
		case "source_name":
			value, readErr := readFunctionMultipartField(part, 512)
			_ = part.Close()
			if readErr != nil || storage.ValidateFilename(value) != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "source_name is invalid")
				return
			}
			sourceNameOverride = value
		case "activate":
			if haveActivate {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "activate may only be provided once")
				return
			}
			haveActivate = true
			value, readErr := readFunctionMultipartField(part, 16)
			_ = part.Close()
			if readErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "activate must be true or false")
				return
			}
			activate, err = strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "activate must be true or false")
				return
			}
		case "build_runtime":
			if buildRuntime != "" {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "build_runtime may only be provided once")
				return
			}
			value, readErr := readFunctionMultipartField(part, 64)
			_ = part.Close()
			if readErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "build_runtime is invalid")
				return
			}
			buildRuntime = value
		case "build_command":
			if buildCommand != "" {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "build_command may only be provided once")
				return
			}
			value, readErr := readFunctionMultipartField(part, maxSiteBuildCommandBytes+1)
			_ = part.Close()
			if readErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "build_command is invalid")
				return
			}
			buildCommand = value
		case "output_directory":
			if outputDirectory != "" {
				_ = part.Close()
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "output_directory may only be provided once")
				return
			}
			value, readErr := readFunctionMultipartField(part, 256)
			_ = part.Close()
			if readErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_error", "output_directory is invalid")
				return
			}
			outputDirectory = value
		default:
			_ = part.Close()
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "unsupported deployment field")
			return
		}
	}
	if !haveSource || prepared.TempPath == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source is required")
		return
	}
	sourceName := sourceFilename
	if sourceNameOverride != "" {
		sourceName = sourceNameOverride
	}
	if sourceName == "" || storage.ValidateFilename(sourceName) != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "source filename is required and must be safe")
		return
	}
	buildOptions, err := parseSiteBuildOptions(buildRuntime, buildCommand, outputDirectory)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if buildOptions.Command != "" {
		artifactPath, pathErr := sitestore.ArtifactRelativePath(projectID, siteID, deploymentID)
		if pathErr != nil {
			internalError(s, w, pathErr)
			return
		}
		if err := s.siteArchives.Commit(&prepared); err != nil {
			internalError(s, w, err)
			return
		}
		sourceCommitted = true
		actor := siteActorFrom(r)
		var createdBy *uuid.UUID
		if actor.Kind == repository.SiteConsoleActor && actor.AccountID != uuid.Nil {
			createdBy = &actor.AccountID
		}
		item, err := s.repo.CreateSiteDeployment(r.Context(), deploymentID, projectID, siteID, actor, repository.SiteDeploymentInput{
			Source:             "upload",
			SourceName:         &sourceName,
			SizeBytes:          0,
			ArchiveSizeBytes:   prepared.Size,
			ChecksumSHA256:     prepared.Checksum,
			ArtifactPath:       artifactPath,
			SourcePath:         &prepared.RelativePath,
			BuildRuntime:       buildOptions.Runtime,
			BuildCommand:       buildOptions.Command,
			OutputDirectory:    buildOptions.OutputDirectory,
			ReservedBytes:      s.config.SitesMaxExpandedBytes,
			CreatedByAccountID: createdBy,
			Activate:           activate,
		})
		if siteResourceError(w, err) {
			_ = s.siteArchives.RemoveRelative(prepared.RelativePath)
			return
		}
		if err != nil {
			_ = s.siteArchives.RemoveRelative(prepared.RelativePath)
			internalError(s, w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]domain.SiteDeployment{"deployment": item})
		return
	}
	archive, err := os.Open(prepared.TempPath)
	if err != nil {
		internalError(s, w, err)
		return
	}
	staging, artifactPath, err := s.sites.BeginStaging(projectID, siteID, deploymentID)
	if err != nil {
		_ = archive.Close()
		internalError(s, w, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			s.sites.CleanupStaging(staging)
		}
	}()
	limits := functionrunner.ArchiveLimits{MaxBytes: s.config.SitesMaxExpandedBytes, MaxFiles: s.config.SitesMaxFiles, MaxEntry: s.config.SitesMaxExpandedBytes, MaxCompressed: s.config.SitesMaxArtifactSize}
	stats, extractErr := functionrunner.Extract(r.Context(), archive, sourceName, staging, limits)
	closeErr := archive.Close()
	if extractErr != nil {
		writeSiteArchiveError(s, w, extractErr)
		return
	}
	if closeErr != nil {
		internalError(s, w, closeErr)
		return
	}
	if stats.Files == 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "site archive must contain at least one file")
		return
	}
	if err := sitestore.ValidateEntrypoint(staging, "index.html"); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "site archive must contain a regular index.html at its root")
		return
	}
	if err := s.sites.CommitDirectory(staging, artifactPath); err != nil {
		internalError(s, w, err)
		return
	}
	committed = true
	actor := siteActorFrom(r)
	var createdBy *uuid.UUID
	if actor.Kind == repository.SiteConsoleActor && actor.AccountID != uuid.Nil {
		createdBy = &actor.AccountID
	}
	item, err := s.repo.CreateSiteDeployment(r.Context(), deploymentID, projectID, siteID, actor, repository.SiteDeploymentInput{Source: "upload", SourceName: &sourceName, SizeBytes: stats.Bytes, ArchiveSizeBytes: prepared.Size, ChecksumSHA256: prepared.Checksum, ArtifactPath: artifactPath, CreatedByAccountID: createdBy, Activate: activate})
	if siteResourceError(w, err) {
		_ = s.sites.RemoveRelative(artifactPath)
		return
	}
	if err != nil {
		_ = s.sites.RemoveRelative(artifactPath)
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.SiteDeployment{"deployment": item})
}

// createGitSiteDeployment downloads one immutable public Git archive and then
// feeds it through the same queued, network-isolated Site builder used by
// multipart source uploads. The fetcher reconstructs provider URLs from
// validated components; clients never supply an arbitrary upstream URL.
func (s *Server) createGitSiteDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return
	}
	if s.siteArchives == nil || s.siteGitFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "Git deployment storage is not ready")
		return
	}
	var req siteGitDeploymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Repository) == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "repository is required")
		return
	}
	buildOptions, err := parseSiteBuildOptions(req.BuildRuntime, req.BuildCommand, req.OutputDirectory)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if buildOptions.Command == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "build_command is required for a Git deployment")
		return
	}
	if s.siteGitSlots == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "Git deployment capacity is not ready")
		return
	}
	select {
	case s.siteGitSlots <- struct{}{}:
		defer func() { <-s.siteGitSlots }()
	default:
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, "git_deployment_busy", "too many Git deployments are downloading; retry later")
		return
	}
	activate := true
	if req.Activate != nil {
		activate = *req.Activate
	}
	deploymentID := uuid.Must(uuid.NewV7())
	archive, err := s.siteGitFetcher.Fetch(r.Context(), req.Repository, req.Ref, s.config.SitesMaxArtifactSize)
	if err != nil {
		switch {
		case errors.Is(err, gitarchive.ErrInvalidRepository), errors.Is(err, gitarchive.ErrInvalidRef):
			writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, gitarchive.ErrTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Git archive exceeds the configured maximum size")
		case errors.Is(err, gitarchive.ErrUnavailable):
			writeError(w, http.StatusBadGateway, "git_archive_unavailable", "the Git provider archive could not be downloaded")
		default:
			internalError(s, w, err)
		}
		return
	}
	if archive.Body == nil || storage.ValidateFilename(archive.Filename) != nil || (archive.Provider != "github" && archive.Provider != "gitlab") || archive.Repository == "" || archive.Ref == "" {
		if archive.Body != nil {
			_ = archive.Body.Close()
		}
		internalError(s, w, errors.New("Git provider returned an invalid archive descriptor"))
		return
	}
	defer archive.Body.Close()
	prepared, err := s.siteArchives.BeginUploadWithLimit(r.Context(), projectID, siteID, deploymentID, archive.Body, s.config.SitesMaxArtifactSize)
	if errors.Is(err, functionstore.ErrTooLarge) || errors.Is(err, gitarchive.ErrTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Git archive exceeds the configured maximum size")
		return
	}
	if errors.Is(err, gitarchive.ErrUnavailable) {
		writeError(w, http.StatusBadGateway, "git_archive_unavailable", "the Git provider archive could not be downloaded")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			s.siteArchives.Cleanup(&prepared)
		}
	}()
	artifactPath, err := sitestore.ArtifactRelativePath(projectID, siteID, deploymentID)
	if err != nil {
		internalError(s, w, err)
		return
	}
	if err := s.siteArchives.Commit(&prepared); err != nil {
		internalError(s, w, err)
		return
	}
	committed = true
	actor := siteActorFrom(r)
	var createdBy *uuid.UUID
	if actor.Kind == repository.SiteConsoleActor && actor.AccountID != uuid.Nil {
		createdBy = &actor.AccountID
	}
	gitRepository, gitRef := archive.Repository, archive.Ref
	item, err := s.repo.CreateSiteDeployment(r.Context(), deploymentID, projectID, siteID, actor, repository.SiteDeploymentInput{
		Source:             archive.Provider,
		SourceName:         &archive.Filename,
		GitRepository:      &gitRepository,
		GitRef:             &gitRef,
		SizeBytes:          0,
		ArchiveSizeBytes:   prepared.Size,
		ChecksumSHA256:     prepared.Checksum,
		ArtifactPath:       artifactPath,
		SourcePath:         &prepared.RelativePath,
		BuildRuntime:       buildOptions.Runtime,
		BuildCommand:       buildOptions.Command,
		OutputDirectory:    buildOptions.OutputDirectory,
		ReservedBytes:      s.config.SitesMaxExpandedBytes,
		CreatedByAccountID: createdBy,
		Activate:           activate,
	})
	if siteResourceError(w, err) {
		_ = s.siteArchives.RemoveRelative(prepared.RelativePath)
		return
	}
	if err != nil {
		_ = s.siteArchives.RemoveRelative(prepared.RelativePath)
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.SiteDeployment{"deployment": item})
}

func writeSiteArchiveError(s *Server, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, functionrunner.ErrArchiveTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "site archive expands beyond the configured limits")
	case errors.Is(err, functionrunner.ErrUnsupportedArchive):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "site archive must be a .zip, .tar, .tar.gz, or .tgz file")
	case errors.Is(err, functionrunner.ErrArchiveTraversal), errors.Is(err, functionrunner.ErrArchiveEntry):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "site archive contains an unsafe or duplicate entry")
	default:
		internalError(s, w, err)
	}
}

func (s *Server) deleteSiteDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, deploymentID, ok := siteDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	paths, err := s.repo.DeleteSiteDeploymentWithArtifact(r.Context(), projectID, siteID, deploymentID, siteActorFrom(r))
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	if s.sites == nil {
		internalError(s, w, errors.New("site artifact storage is unavailable"))
		return
	}
	if err := s.sites.RemoveRelative(paths.ArtifactPath); err != nil {
		internalError(s, w, fmt.Errorf("remove deleted site artifact: %w", err))
		return
	}
	if paths.SourcePath != "" && s.siteArchives != nil {
		if err := s.siteArchives.RemoveRelative(paths.SourcePath); err != nil {
			internalError(s, w, fmt.Errorf("remove deleted site source: %w", err))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) activateSiteDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, deploymentID, ok := siteDeploymentPathIDs(w, r)
	if !ok {
		return
	}
	item, site, err := s.repo.ActivateSiteDeployment(r.Context(), projectID, siteID, deploymentID, siteActorFrom(r))
	if siteResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": site, "deployment": item})
}

// serveSiteFile is intentionally unauthenticated: a published Site is a
// public web origin. PostgreSQL resolves the active deployment first, then
// sitestore validates every filesystem component before opening the file.
func (s *Server) serveSiteFile(w http.ResponseWriter, r *http.Request) {
	siteID, err := repository.ParseUUID(chi.URLParam(r, "siteID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "site was not found")
		return
	}
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "site artifact storage is not ready")
		return
	}
	artifact, err := s.repo.GetActiveSiteArtifact(r.Context(), siteID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "site was not found")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	s.servePublishedSiteFile(w, r, artifact)
}

// serveSiteDeploymentFile exposes a ready immutable release at a preview URL.
// It is public by design: the deployment UUID is the capability-like URL and
// the Site must still be enabled. The route never accepts a filesystem path
// from the client as an artifact locator.
func (s *Server) serveSiteDeploymentFile(w http.ResponseWriter, r *http.Request) {
	siteID, err := repository.ParseUUID(chi.URLParam(r, "siteID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "site was not found")
		return
	}
	deploymentID, err := repository.ParseUUID(chi.URLParam(r, "deploymentID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "site deployment was not found")
		return
	}
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "site artifact storage is not ready")
		return
	}
	artifact, err := s.repo.GetSiteDeploymentArtifact(r.Context(), siteID, deploymentID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "site deployment was not found")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	s.servePublishedSiteFile(w, r, artifact)
}

func (s *Server) servePublishedSiteFile(w http.ResponseWriter, r *http.Request, artifact repository.SitePublicArtifact) {
	requested := chi.URLParam(r, "*")
	if requested == "" {
		requested = "index.html"
	}
	file, info, err := s.sites.OpenFile(artifact.ArtifactPath, requested)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "not_found", "site file was not found")
		return
	}
	if errors.Is(err, sitestore.ErrInvalidFile) || errors.Is(err, sitestore.ErrInvalidPath) {
		writeError(w, http.StatusNotFound, "not_found", "site file was not found")
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	defer file.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("ETag", `"`+artifact.Deployment.ChecksumSHA256+`"`)
	ext := strings.ToLower(filepath.Ext(requested))
	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	if ext == ".html" || ext == ".htm" || requested == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}
	http.ServeContent(w, r, filepath.Base(requested), info.ModTime(), file)
}

func sitePathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	siteID, ok := pathUUID(w, r, "siteID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, siteID, true
}

func siteDeploymentPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	deploymentID, ok := pathUUID(w, r, "deploymentID")
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return projectID, siteID, deploymentID, true
}

func siteResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project or site was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage Sites")
		return true
	case errors.Is(err, repository.ErrSiteQuotaExceeded):
		writeError(w, http.StatusConflict, "quota_exceeded", "site artifact quota would be exceeded")
		return true
	case errors.Is(err, repository.ErrSiteDeploymentActive):
		writeError(w, http.StatusConflict, "deployment_active", "the active site deployment cannot be deleted")
		return true
	case errors.Is(err, repository.ErrSiteDisabled):
		writeError(w, http.StatusConflict, "site_disabled", "the site is disabled")
		return true
	case errors.Is(err, repository.ErrInvalidSiteTransition):
		writeError(w, http.StatusConflict, "invalid_transition", "the site deployment is not ready to activate")
		return true
	case errors.Is(err, repository.ErrInvalidSiteSettings):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "site settings are invalid")
		return true
	default:
		return false
	}
}
