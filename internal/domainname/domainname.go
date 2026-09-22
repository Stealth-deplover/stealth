// Package domainname owns canonical DNS hostname and registrable-domain
// semantics used by the control plane.
package domainname

import (
	"errors"
	"net"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

var (
	ErrInvalidHostname          = errors.New("invalid DNS hostname")
	ErrInvalidRegistrableDomain = errors.New("invalid registrable domain")
)

// NormalizeHostname returns the canonical ASCII representation of a DNS
// hostname. It accepts a syntactically valid single-label hostname, while
// callers that require public registrability should use NormalizeDomain.
func NormalizeHostname(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", ErrInvalidHostname
	}
	for _, character := range raw {
		if unicode.IsControl(character) {
			return "", ErrInvalidHostname
		}
	}

	value := strings.TrimSpace(raw)
	if value == "" || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", ErrInvalidHostname
	}
	if strings.ContainsAny(value, ":/@?#\\") {
		return "", ErrInvalidHostname
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", ErrInvalidHostname
	}
	if net.ParseIP(value) != nil {
		return "", ErrInvalidHostname
	}
	if looksLikeIPv4(value) {
		return "", ErrInvalidHostname
	}

	canonical, err := idna.Lookup.ToASCII(value)
	if err != nil {
		return "", ErrInvalidHostname
	}
	canonical = strings.ToLower(canonical)
	if canonical == "" || len(canonical) > 253 {
		return "", ErrInvalidHostname
	}
	labels := strings.Split(canonical, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidHostname
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return "", ErrInvalidHostname
		}
	}
	return canonical, nil
}

func looksLikeIPv4(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 3 {
			return false
		}
		var octet int
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
			octet = octet*10 + int(character-'0')
		}
		if octet > 255 {
			return false
		}
	}
	return true
}

// RegistrableDomain returns the effective TLD plus one for a hostname. It
// rejects a public suffix itself, so callers can use the result as evidence
// that an operator-owned registrable domain exists.
func RegistrableDomain(raw string) (string, error) {
	normalized, err := NormalizeHostname(raw)
	if err != nil {
		return "", err
	}
	registrable, err := publicsuffix.EffectiveTLDPlusOne(normalized)
	if err != nil {
		return "", ErrInvalidRegistrableDomain
	}
	return registrable, nil
}

// NormalizeDomain canonicalizes a hostname and requires it to have a
// registrable domain. It preserves the complete hostname, not just its
// eTLD+1, so subdomains remain usable as configured domains.
func NormalizeDomain(raw string) (string, error) {
	normalized, err := NormalizeHostname(raw)
	if err != nil {
		return "", err
	}
	if _, err := RegistrableDomain(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// IsSubdomain reports whether hostname is a strict subdomain of parent.
// Invalid inputs return false.
func IsSubdomain(hostname, parent string) bool {
	host, err := NormalizeHostname(hostname)
	if err != nil {
		return false
	}
	base, err := NormalizeHostname(parent)
	if err != nil || host == base {
		return false
	}
	return strings.HasSuffix(host, "."+base)
}
