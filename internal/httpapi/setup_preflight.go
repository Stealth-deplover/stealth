package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Stealth-deplover/stealth/internal/preflight"
)

func (s *Server) setupPreflight(w http.ResponseWriter, r *http.Request) {
	checks := make([]setupCheck, 0, 14)
	add := func(name, detail string, ok, required bool) {
		status := "pass"
		if !ok {
			status = "fail"
			if !required {
				status = "warn"
			}
		}
		checks = append(checks, setupCheck{Name: name, Detail: detail, Status: status, Required: required})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	state, err := s.setupState.Load(ctx)
	if err != nil {
		internalError(s, w, err)
		return
	}
	if len(state.HostPreflight) == 0 {
		add("Host preflight", "the host CLI has not published system checks", false, true)
	} else {
		for _, check := range state.HostPreflight {
			add(check.Name, check.Detail, check.OK, check.Required)
		}
	}

	// The setup container may verify services it legitimately owns, but it has
	// no Docker runner. In particular, Docker and resource checks never execute
	// inside the temporary web-facing container.
	probes := preflight.NewProbes(nil, http.DefaultClient)

	if s.repo == nil {
		add("Database", "database dependency is not configured", false, true)
	} else if err := s.repo.Ping(ctx); err != nil {
		add("Database", "PostgreSQL is not ready", false, true)
	} else {
		add("Database", "PostgreSQL is reachable", true, true)
	}
	if s.storage == nil || !s.storageReady {
		add("Storage", "storage is not configured", false, true)
	} else if err := s.storage.Ping(ctx); err != nil {
		add("Storage", "storage is not reachable", false, true)
	} else {
		add("Storage", "storage is reachable", true, true)
	}
	if s.limiter == nil {
		add("Redis", "rate limiter is not configured", false, true)
	} else if err := s.limiter.Ping(ctx); err != nil {
		add("Redis", "Redis is not reachable", false, true)
	} else {
		add("Redis", "Redis is reachable", true, true)
	}

	for _, check := range preflight.CloudflareChecks(ctx, probes, preflight.DefaultCloudflareTunnelHosts) {
		add(check.Name, check.Detail, check.OK, check.Required)
	}

	writeJSON(w, http.StatusOK, setupPreflightResponse{Checks: checks})
}
