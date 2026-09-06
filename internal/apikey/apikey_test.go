package apikey

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewSecretIsRecognizableHighEntropyAndHashOnly(t *testing.T) {
	secret, prefix, hash, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSecret(secret); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, SecretPrefix) || !strings.HasPrefix(secret, prefix) || len(hash) != 32 {
		t.Fatalf("unexpected secret material: secret prefix=%q prefix=%q hash length=%d", secret[:len(SecretPrefix)], prefix, len(hash))
	}
	if len(secret) < len(SecretPrefix)+43 {
		t.Fatalf("secret is too short for 256-bit entropy: %d", len(secret))
	}
	if string(hash) == secret {
		t.Fatal("secret was returned as its own hash")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, SecretPrefix))
	if err != nil || len(raw) != 32 {
		t.Fatalf("secret payload is not 32 random bytes: %v", err)
	}
	other, _, _, err := NewSecret()
	if err != nil || secret == other {
		t.Fatal("two generated API keys unexpectedly matched")
	}
}

func TestNormalizeScopesDeduplicatesAndSorts(t *testing.T) {
	scopes, err := NormalizeScopes([]string{"users.write", "users.read", "users.write"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(scopes, ","); got != "users.read,users.write" {
		t.Fatalf("scopes = %q", got)
	}
	for _, invalid := range [][]string{nil, {}, {""}, {"users.delete"}} {
		if _, err := NormalizeScopes(invalid); err == nil {
			t.Fatalf("NormalizeScopes(%v) unexpectedly succeeded", invalid)
		}
	}
}

func TestNormalizeDatabaseScopes(t *testing.T) {
	got, err := NormalizeScopes([]string{"databases.write", "users.read", "databases.read", "databases.write"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "databases.read" || got[1] != "databases.write" || got[2] != "users.read" {
		t.Fatalf("unexpected normalized scopes: %#v", got)
	}
	if _, err := NormalizeScopes([]string{"storage.read"}); !errors.Is(err, ErrInvalidScopes) {
		t.Fatalf("unsupported scope error = %v", err)
	}
}

func TestNormalizeProjectScopesIncludesStorage(t *testing.T) {
	got, err := NormalizeProjectScopes([]string{"storage.write", "storage.read", "storage.write"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "storage.read,storage.write" {
		t.Fatalf("normalized storage scopes = %#v", got)
	}
}

func TestValidateExpiryFutureAndBounded(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := ValidateExpiry(nil, now); err != nil {
		t.Fatal(err)
	}
	valid := now.Add(24 * time.Hour)
	if err := ValidateExpiry(&valid, now); err != nil {
		t.Fatal(err)
	}
	for _, expiry := range []*time.Time{
		func() *time.Time { value := now; return &value }(),
		func() *time.Time { value := now.Add(-time.Second); return &value }(),
		func() *time.Time { value := now.Add(MaxExpiry + time.Second); return &value }(),
	} {
		if err := ValidateExpiry(expiry, now); err == nil {
			t.Fatalf("expiry %v unexpectedly accepted", expiry)
		}
	}
}
