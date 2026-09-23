// Package platformhostname owns the small, instance-global namespace used by
// platform workload hostnames. It deliberately does not know about PostgreSQL
// or routing; persistence and route materialization remain separate capabilities.
package platformhostname

import (
	"errors"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domainname"
	"github.com/google/uuid"
)

const (
	MaxLabelLength = 63
	// The UUID suffix is the complete immutable resource identity. Keeping the
	// complete value makes collision variants deterministic without random
	// retry state or a process-local allocator.
	stableSuffixLength = 32
)

var ErrInvalidLabel = errors.New("invalid platform hostname label")

// Reserved reports labels that belong to the instance infrastructure rather
// than to a project workload. This is a short namespace, not a general
// blacklist of words an operator might want to use.
func Reserved(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "api", "admin", "console", "status", "www":
		return true
	default:
		return false
	}
}

// Candidates returns the compatibility-preserving Site allocation order. The
// first candidate preserves the Site name when it is safe and available.
// Later candidates are globally distinguishable by the immutable Site UUID.
func Candidates(name string, resourceID uuid.UUID) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	suffix := uuidSuffix(resourceID)
	if len(suffix) != stableSuffixLength {
		return nil
	}
	collision := labelWithSuffix(name, suffix)
	candidates := make([]string, 0, 4)
	if !Reserved(name) && Validate(name) == nil {
		candidates = append(candidates, name)
	}
	candidates = append(candidates,
		collision,
		"site-"+suffix,
		suffix,
	)
	return candidates
}

// AppCandidates returns a bounded, deterministic allocation order for a
// persistent App. The App-specific fallback names keep its collision sequence
// distinct from Site's established candidate order.
func AppCandidates(name string, resourceID uuid.UUID) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	suffix := uuidSuffix(resourceID)
	if len(suffix) != stableSuffixLength {
		return nil
	}
	candidates := make([]string, 0, 4)
	if !Reserved(name) && Validate(name) == nil {
		candidates = append(candidates, name)
	}
	candidates = append(candidates,
		labelWithSuffix(name, suffix),
		"app-"+suffix,
		suffix,
	)
	return candidates
}

func uuidSuffix(resourceID uuid.UUID) string {
	return strings.ReplaceAll(resourceID.String(), "-", "")
}

func labelWithSuffix(human, suffix string) string {
	maxHuman := MaxLabelLength - 1 - len(suffix)
	if maxHuman < 1 {
		return suffix[:MaxLabelLength]
	}
	human = human[:min(len(human), maxHuman)]
	human = strings.TrimRight(human, "-")
	if human == "" {
		return suffix[:MaxLabelLength]
	}
	return human + "-" + suffix
}

// Validate checks the canonical ASCII DNS-label form used in PostgreSQL.
func Validate(label string) error {
	if label == "" || len(label) > MaxLabelLength || strings.TrimSpace(label) != label {
		return ErrInvalidLabel
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return ErrInvalidLabel
	}
	for _, character := range label {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return ErrInvalidLabel
	}
	return nil
}

// Hostname combines a persisted label with a canonical workload base domain.
// It revalidates the complete result so malformed database state cannot leak
// into an HTTP Host rule or response.
func Hostname(label, workloadBaseDomain string) (string, error) {
	if err := Validate(label); err != nil {
		return "", err
	}
	baseDomain, err := domainname.NormalizeDomain(workloadBaseDomain)
	if err != nil {
		return "", err
	}
	normalized, err := domainname.NormalizeHostname(label + "." + baseDomain)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
