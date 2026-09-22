package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domainname"
	"github.com/Stealth-deplover/stealth/internal/repository"
)

type instanceDomainSettingsRequest struct {
	WorkloadBaseDomain optionalStringValue `json:"workload_base_domain"`
}

type optionalStringValue struct {
	Set   bool
	Value *string
}

func (value *optionalStringValue) UnmarshalJSON(raw []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		value.Value = nil
		return nil
	}
	var decoded string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

func (s *Server) getInstanceDomainSettings(w http.ResponseWriter, r *http.Request) {
	instanceHostname, err := s.instanceHostname()
	if err != nil {
		internalError(s, w, err)
		return
	}
	settings, err := s.repo.GetInstanceDomainSettings(r.Context(), instanceHostname)
	if instanceDomainSettingsError(s, w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateInstanceDomainSettings(w http.ResponseWriter, r *http.Request) {
	var request instanceDomainSettingsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !request.WorkloadBaseDomain.Set {
		writeError(w, http.StatusBadRequest, "validation_error", "workload_base_domain must be provided")
		return
	}
	instanceHostname, err := s.instanceHostname()
	if err != nil {
		internalError(s, w, err)
		return
	}
	settings, err := s.repo.UpdateInstanceDomainSettings(r.Context(), mustUUID(accountFrom(r).ID), instanceHostname, request.WorkloadBaseDomain.Value)
	if instanceDomainSettingsError(s, w, err) {
		return
	}
	if err != nil {
		internalError(s, w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) instanceHostname() (string, error) {
	raw := strings.TrimSpace(s.config.PublicAppURL)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("PUBLIC_APP_URL is invalid for instance domain settings")
	}
	hostname := parsed.Hostname()
	if ip := net.ParseIP(hostname); ip != nil {
		return strings.ToLower(hostname), nil
	}
	normalized, err := domainname.NormalizeHostname(hostname)
	if err != nil {
		return "", errors.New("PUBLIC_APP_URL hostname is invalid for instance domain settings")
	}
	return normalized, nil
}

func instanceDomainSettingsError(s *Server, w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "instance owner permission is required")
		return true
	case errors.Is(err, repository.ErrInvalidInstanceDomain), errors.Is(err, domainname.ErrInvalidHostname), errors.Is(err, domainname.ErrInvalidRegistrableDomain):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "workload_base_domain is not a valid operator-controlled domain")
		return true
	case errors.Is(err, repository.ErrInstanceDomainConflict):
		writeError(w, http.StatusConflict, "platform_namespace_conflict", "workload_base_domain conflicts with an existing Site custom domain")
		return true
	case errors.Is(err, repository.ErrNotFound):
		internalError(s, w, errors.New("instance domain settings are unavailable"))
		return true
	default:
		return false
	}
}
