package repository

import (
	"errors"
	"testing"
)

func TestNormalizeAdminErrorFingerprint(t *testing.T) {
	valid := "ABCDEFabcdef0123456789ABCDEFabcdef0123456789ABCDEFabcdef01234567"
	got, err := normalizeAdminErrorFingerprint(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got != "abcdefabcdef0123456789abcdefabcdef0123456789abcdefabcdef01234567" {
		t.Fatalf("normalized fingerprint = %q", got)
	}
	for _, value := range []string{"", "not-a-fingerprint", "zz" + valid[2:], valid[:len(valid)-2]} {
		if _, err := normalizeAdminErrorFingerprint(value); !errors.Is(err, ErrInvalidAdminErrorGroup) {
			t.Fatalf("normalizeAdminErrorFingerprint(%q) error = %v, want invalid error", value, err)
		}
	}
}
