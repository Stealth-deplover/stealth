package auth

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct-horse-battery-staple") {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword(hash, "incorrect-password") {
		t.Fatal("incorrect password verified")
	}
}

func TestPasswordValidation(t *testing.T) {
	if err := ValidatePassword("too-short"); err == nil {
		t.Fatal("expected short password error")
	}
}

func TestVerifyPasswordOrDummy(t *testing.T) {
	if VerifyPasswordOrDummy("", "any-password") {
		t.Fatal("dummy credential must never verify")
	}
}
