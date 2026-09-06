package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func NewSessionToken() (token string, hash []byte, err error) {
	bytes, err := randomBytes(32)
	if err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(bytes)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}

func HashSessionToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ValidateToken accepts the same 32-byte, URL-safe opaque value generated for
// sessions and recovery links. Callers should hash it immediately and never
// persist the plaintext.
func ValidateToken(token string) error {
	if len(token) != 43 {
		return fmt.Errorf("token must be a 32-byte URL-safe value")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("token must be a 32-byte URL-safe value")
	}
	return nil
}

func randomBytes(length int) ([]byte, error) {
	bytes := make([]byte, length)
	_, err := rand.Read(bytes)
	return bytes, err
}
