package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
)

type siteDomainRequest struct {
	Hostname string `json:"hostname"`
}

func (s *Server) listSiteDomains(w http.ResponseWriter, r *http.Request) {
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
	items, next, canManage, err := s.repo.ListSiteDomains(r.Context(), projectID, siteID, siteActorFrom(r), limit, cursorID)
	if siteDomainResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": items, "pagination": paginationOf(limit, next), "can_manage": canManage})
}

func (s *Server) createSiteDomain(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return
	}
	var req siteDomainRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	hostname, err := repository.NormalizeSiteHostname(req.Hostname)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "hostname must be a lowercase DNS hostname with at least two labels")
		return
	}
	item, err := s.repo.CreateSiteDomain(r.Context(), uuid.Must(uuid.NewV7()), projectID, siteID, siteActorFrom(r), repository.SiteDomainInput{Hostname: hostname})
	if siteDomainResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domain.SiteDomain{"domain": item})
}

func (s *Server) getSiteDomain(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, domainID, ok := siteDomainPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.GetSiteDomain(r.Context(), projectID, siteID, domainID, siteActorFrom(r))
	if siteDomainResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.SiteDomain{"domain": item})
}

func (s *Server) verifySiteDomain(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, domainID, ok := siteDomainPathIDs(w, r)
	if !ok {
		return
	}
	item, err := s.repo.VerifySiteDomain(r.Context(), projectID, siteID, domainID, siteActorFrom(r))
	if siteDomainResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]domain.SiteDomain{"domain": item})
}

func (s *Server) deleteSiteDomain(w http.ResponseWriter, r *http.Request) {
	projectID, siteID, domainID, ok := siteDomainPathIDs(w, r)
	if !ok {
		return
	}
	err := s.repo.DeleteSiteDomain(r.Context(), projectID, siteID, domainID, siteActorFrom(r))
	if siteDomainResourceError(w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveCustomDomainFile is intentionally unauthenticated. A trusted reverse
// proxy forwards the original Host header, and PostgreSQL only resolves a
// hostname that has passed the DNS TXT challenge and points at an active Site.
func (s *Server) serveCustomDomainFile(w http.ResponseWriter, r *http.Request) {
	hostname := requestHostname(r)
	if hostname == "" {
		writeError(w, http.StatusNotFound, "not_found", "site was not found")
		return
	}
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "site artifact storage is not ready")
		return
	}
	artifact, err := s.repo.GetActiveSiteArtifactByHostname(r.Context(), hostname)
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

func requestHostname(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else if strings.Contains(host, ":") {
		// A raw IPv6 literal or malformed host cannot be a supported custom
		// hostname. Do not attempt to interpret it as a domain label.
		return ""
	}
	hostname, err := repository.NormalizeSiteHostname(host)
	if err != nil {
		return ""
	}
	return hostname
}

func siteDomainPathIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	projectID, siteID, ok := sitePathIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	domainID, ok := pathUUID(w, r, "domainID")
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return projectID, siteID, domainID, true
}

func siteDomainResourceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project, site, or domain was not found")
		return true
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to manage Site domains")
		return true
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "the hostname is already bound to a Site")
		return true
	case errors.Is(err, repository.ErrInvalidSiteDomain):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "hostname is not a valid DNS hostname")
		return true
	case errors.Is(err, repository.ErrSiteDomainVerificationFailed):
		writeError(w, http.StatusConflict, "verification_failed", "the required DNS TXT verification record was not found")
		return true
	default:
		return false
	}
}
