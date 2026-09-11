// Package bootstrap contains the small, shared primitives used by the
// first-run instance-owner flow. It deliberately does not contain HTTP or
// CLI concerns so the code format and proof protocol cannot drift between
// those surfaces.
package bootstrap

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	// CodeLifetime is intentionally short because the code is displayed in an
	// operator terminal and is only needed to create the first owner.
	CodeLifetime = 15 * time.Minute

	// CLIProofHeader authenticates the local CLI's request to mint a setup
	// session. The header carries a derived proof, never the config secret.
	CLIProofHeader = "X-Stealth-Bootstrap-Proof"

	cliProofMessage = "stealth-bootstrap-session-v1"
	codePrefix      = "STEALTH-"
	codeAlphabet    = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	codeParts       = 3
	codePartLength  = 4
	deviceKeyLabel  = "stealth-bootstrap-github-device-code-v1"
)

// GenerateCode creates a 12-character, human-friendly code. The alphabet has
// 32 symbols (5 bits each), so every code contains 60 bits of randomness while
// excluding characters commonly confused when typed manually: I, O, 0, and 1.
func GenerateCode() (string, error) {
	parts := make([]string, 0, codeParts)
	for part := 0; part < codeParts; part++ {
		var builder strings.Builder
		builder.Grow(codePartLength)
		for index := 0; index < codePartLength; index++ {
			value, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
			if err != nil {
				return "", fmt.Errorf("generate bootstrap code: %w", err)
			}
			builder.WriteByte(codeAlphabet[value.Int64()])
		}
		parts = append(parts, builder.String())
	}
	return codePrefix + strings.Join(parts, "-"), nil
}

// NormalizeCode accepts surrounding whitespace and lower-case manual input,
// but leaves the separators significant so malformed strings are rejected
// instead of being silently repaired.
func NormalizeCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// ValidCode checks the complete canonical format before it is hashed or sent
// to the repository. Validation errors intentionally do not distinguish an
// expired, used, or incorrect code at the API boundary.
func ValidCode(raw string) bool {
	value := NormalizeCode(raw)
	if !strings.HasPrefix(value, codePrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, codePrefix), "-")
	if len(parts) != codeParts {
		return false
	}
	for _, part := range parts {
		if len(part) != codePartLength {
			return false
		}
		for _, character := range part {
			if !strings.ContainsRune(codeAlphabet, character) {
				return false
			}
		}
	}
	return true
}

// HashCode returns the only representation of a setup code that should be
// persisted. SHA-256 is used over the canonical code; the code has 60 bits of
// CSPRNG entropy and the endpoint is additionally rate-limited.
func HashCode(raw string) []byte {
	digest := sha256.Sum256([]byte(NormalizeCode(raw)))
	return digest[:]
}

// CLIProof derives a request proof from the installation's private CLI key.
func CLIProof(key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(cliProofMessage))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCLIProof compares a supplied proof without leaking timing based on
// the first differing byte.
func VerifyCLIProof(key []byte, supplied string) bool {
	if len(key) == 0 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(supplied))
	if err != nil || len(decoded) != sha256.Size {
		return false
	}
	expected, err := base64.RawURLEncoding.DecodeString(CLIProof(key))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(decoded, expected) == 1
}

// SealDeviceCode encrypts GitHub's temporary device_code for database-backed
// restart recovery. The CLI key is a dedicated bootstrap secret; a separate
// HMAC context derives the AES key so the proof and encryption uses cannot be
// confused. The returned bytes contain only nonce+ciphertext.
func SealDeviceCode(key []byte, deviceCode string) ([]byte, error) {
	if len(key) != 32 || deviceCode == "" {
		return nil, fmt.Errorf("invalid bootstrap device-code encryption input")
	}
	cipherKey := deriveDeviceCodeKey(key)
	block, err := aes.NewCipher(cipherKey[:])
	if err != nil {
		return nil, fmt.Errorf("create device-code cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create device-code cipher mode: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate device-code nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(deviceCode), []byte(deviceKeyLabel)), nil
}

// OpenDeviceCode decrypts a persisted device code only for the server-side
// poll that needs to contact GitHub. It is never returned by any API.
func OpenDeviceCode(key, sealed []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("invalid bootstrap device-code decryption key")
	}
	cipherKey := deriveDeviceCodeKey(key)
	block, err := aes.NewCipher(cipherKey[:])
	if err != nil {
		return "", fmt.Errorf("create device-code cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create device-code cipher mode: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid sealed device code")
	}
	plaintext, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(deviceKeyLabel))
	if err != nil {
		return "", fmt.Errorf("open sealed device code: %w", err)
	}
	return string(plaintext), nil
}

func deriveDeviceCodeKey(key []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(deviceKeyLabel))
	var derived [sha256.Size]byte
	copy(derived[:], mac.Sum(nil))
	return derived
}
