package auth

import "testing"

func TestNewSessionTokenReturnsHashOnlyDerivableFromToken(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || len(hash) != 32 {
		t.Fatal("unexpected session token result")
	}
	if string(hash) != string(HashSessionToken(token)) {
		t.Fatal("session hashes differ")
	}
}

func TestValidateTokenRejectsMalformedValues(t *testing.T) {
	token, _, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateToken(token); err != nil {
		t.Fatalf("generated token rejected: %v", err)
	}
	for _, invalid := range []string{"", "short", token[:42], token[:42] + "!"} {
		if err := ValidateToken(invalid); err == nil {
			t.Fatalf("ValidateToken(%q) unexpectedly succeeded", invalid)
		}
	}
}
