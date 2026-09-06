package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidCORSOrigin = errors.New("invalid CORS origin")

const maxCORSOrigins = 32

// NormalizeCORSOrigin returns the canonical scheme/host/port origin used by
// browser Origin headers. Paths, credentials, wildcards, and query strings
// are deliberately rejected because they do not describe an origin and would
// make a credentialed allowlist ambiguous.
func NormalizeCORSOrigin(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", ErrInvalidCORSOrigin
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", ErrInvalidCORSOrigin
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrInvalidCORSOrigin
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.ContainsAny(hostname, "*%/") {
		return "", ErrInvalidCORSOrigin
	}
	if port := parsed.Port(); port != "" {
		for _, ch := range port {
			if ch < '0' || ch > '9' {
				return "", ErrInvalidCORSOrigin
			}
		}
		parsedPort, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", ErrInvalidCORSOrigin
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Host)
	if (scheme == "http" && parsed.Port() == "80") || (scheme == "https" && parsed.Port() == "443") {
		host = strings.TrimSuffix(host, ":"+parsed.Port())
	}
	canonical := scheme + "://" + host
	if len(canonical) > 2048 || strings.ContainsAny(canonical, "\x00\r\n\t @") {
		return "", ErrInvalidCORSOrigin
	}
	return canonical, nil
}

// NormalizeCORSOrigins canonicalizes and sorts a project allowlist. Duplicate
// values are rejected so an update cannot hide an accidental configuration
// mistake behind an order-only diff.
func NormalizeCORSOrigins(raw []string) ([]string, error) {
	if len(raw) > maxCORSOrigins {
		return nil, fmt.Errorf("%w: at most %d origins", ErrInvalidCORSOrigin, maxCORSOrigins)
	}
	if len(raw) == 0 {
		return []string{}, nil
	}
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		origin, err := NormalizeCORSOrigin(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[origin]; exists {
			return nil, fmt.Errorf("%w: duplicate origin", ErrInvalidCORSOrigin)
		}
		seen[origin] = struct{}{}
		result = append(result, origin)
	}
	sort.Strings(result)
	return result, nil
}

// ProjectCORSOrigins is intentionally unauthenticated: the CORS middleware
// needs it before the request's project actor can be established. It only
// returns an allowlist, never project metadata or data.
func (r *Repository) ProjectCORSOrigins(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	var origins []string
	err := r.pool.QueryRow(ctx, `SELECT cors_origins FROM project_auth_settings WHERE project_id=$1`, projectID).Scan(&origins)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if origins == nil {
		origins = []string{}
	}
	return origins, err
}
