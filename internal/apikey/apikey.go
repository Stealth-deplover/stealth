package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	SecretPrefix       = "stl_key_"
	secretEntropyBytes = 32
	secretVisibleChars = 8
	MaxExpiry          = 365 * 24 * time.Hour
)

var supportedScopes = map[string]struct{}{
	"users.read":      {},
	"users.write":     {},
	"databases.read":  {},
	"databases.write": {},
	// Storage scopes are kept in the project-level parser below so existing
	// callers of NormalizeScopes retain its pre-Storage contract.
}

var supportedProjectScopes = map[string]struct{}{
	"users.read":      {},
	"users.write":     {},
	"databases.read":  {},
	"databases.write": {},
	"storage.read":    {},
	"storage.write":   {},
	"functions.read":  {},
	"functions.write": {},
	"sites.read":      {},
	"sites.write":     {},
	"webhooks.read":   {},
	"webhooks.write":  {},
	"realtime.read":   {},
	"messaging.read":  {},
	"messaging.write": {},
}

var (
	ErrInvalidSecret = errors.New("invalid API key secret")
	ErrInvalidScopes = errors.New("invalid API key scopes")
	ErrInvalidExpiry = errors.New("invalid API key expiry")
)

// NewSecret returns the one-time secret, its safe display prefix, and its
// SHA-256 digest. The caller must persist only the digest.
func NewSecret() (secret, prefix string, hash []byte, err error) {
	raw := make([]byte, secretEntropyBytes)
	_, err = rand.Read(raw)
	if err != nil {
		return "", "", nil, err
	}
	secret = SecretPrefix + base64.RawURLEncoding.EncodeToString(raw)
	prefix = secret[:len(SecretPrefix)+secretVisibleChars]
	hash = HashSecret(secret)
	return secret, prefix, hash, nil
}

func HashSecret(secret string) []byte {
	digest := sha256.Sum256([]byte(secret))
	return digest[:]
}

func ValidateSecret(secret string) error {
	if !strings.HasPrefix(secret, SecretPrefix) {
		return ErrInvalidSecret
	}
	encoded := strings.TrimPrefix(secret, SecretPrefix)
	if encoded == "" || len(encoded) > 64 {
		return ErrInvalidSecret
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) != secretEntropyBytes {
		return ErrInvalidSecret
	}
	return nil
}

func NormalizeScopes(raw []string) ([]string, error) {
	return normalizeScopes(raw, supportedScopes)
}

// NormalizeProjectScopes is the canonical parser for project API-key
// creation. It includes Storage while NormalizeScopes remains available to
// older integrations that only know the original user/database scope set.
func NormalizeProjectScopes(raw []string) ([]string, error) {
	return normalizeScopes(raw, supportedProjectScopes)
}

func normalizeScopes(raw []string, supported map[string]struct{}) ([]string, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidScopes
	}
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		scope := strings.TrimSpace(value)
		if _, ok := supported[scope]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidScopes, scope)
		}
		seen[scope] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, ErrInvalidScopes
	}
	scopes := make([]string, 0, len(seen))
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes, nil
}

func HasScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func ValidateExpiry(expiresAt *time.Time, now time.Time) error {
	if expiresAt == nil {
		return nil
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(MaxExpiry)) {
		return ErrInvalidExpiry
	}
	return nil
}
