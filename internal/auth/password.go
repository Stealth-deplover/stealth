package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// dummyHash is a fixed, valid Argon2id hash for a non-secret dummy password.
// It equalizes work for unknown-account login attempts and is never returned.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=1$c3RlYWx0aC1kdW1teS12MQ$TodtfDboXafOTSGtigwnZseuf2tpD5FNiLbK37KtOlg"

const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
)

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonTime, argonThreads) {
		return false
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[4])
	expected, hashErr := base64.RawStdEncoding.DecodeString(parts[5])
	if saltErr != nil || hashErr != nil || len(expected) != int(argonKeyLen) {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// VerifyPasswordOrDummy performs the same Argon2id work when no account hash
// is available. Callers must still return an identical invalid-credentials
// response for either outcome.
func VerifyPasswordOrDummy(encoded, password string) bool {
	if encoded == "" {
		encoded = dummyHash
	}
	return VerifyPassword(encoded, password)
}

func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 256 {
		return fmt.Errorf("password must be between 12 and 256 characters")
	}
	return nil
}
