package bootstrap

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateCodeUsesCanonicalHumanFriendlyFormat(t *testing.T) {
	code, err := GenerateCode()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidCode(code) {
		t.Fatalf("generated code %q is invalid", code)
	}
	if len(code) != len("STEALTH-XXXX-XXXX-XXXX") {
		t.Fatalf("generated code length = %d, want 22", len(code))
	}
	if strings.ContainsAny(code, "IO01") {
		t.Fatalf("generated code contains an ambiguous character: %q", code)
	}
}

func TestCodeValidationNormalizesOnlyCaseAndWhitespace(t *testing.T) {
	if !ValidCode("  stealth-abcd-2345-efgh  ") {
		t.Fatal("lower-case, trimmed code should be accepted")
	}
	if ValidCode("STEALTHABCD2345EFGH") {
		t.Fatal("missing separators should be rejected")
	}
	if ValidCode("STEALTH-ABCD-2345-IO01") {
		t.Fatal("ambiguous characters should be rejected")
	}
}

func TestHashCodeDoesNotReturnPlaintext(t *testing.T) {
	code := "STEALTH-ABCD-2345-EFGH"
	hash := HashCode(code)
	if len(hash) != 32 {
		t.Fatalf("hash length = %d, want 32", len(hash))
	}
	if string(hash) == code {
		t.Fatal("hash unexpectedly contains plaintext code")
	}
	if string(HashCode(code)) != string(HashCode(" stealth-abcd-2345-efgh ")) {
		t.Fatal("hashing is not canonical")
	}
}

func TestCLIProofIsDerivedAndConstantFormat(t *testing.T) {
	key := []byte("private-installation-key")
	proof := CLIProof(key)
	decoded, err := base64.RawURLEncoding.DecodeString(proof)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("proof is not a raw base64 SHA-256 digest: %v", err)
	}
	if !VerifyCLIProof(key, proof) {
		t.Fatal("valid proof was rejected")
	}
	if VerifyCLIProof(key, CLIProof([]byte("different-key"))) {
		t.Fatal("proof for another key was accepted")
	}
	if VerifyCLIProof(nil, proof) || VerifyCLIProof(key, "not-a-proof") {
		t.Fatal("invalid proof was accepted")
	}
}
