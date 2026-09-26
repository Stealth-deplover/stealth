// Package secretkey contains the shared encoding rules for operator-managed
// 32-byte encryption keys.
package secretkey

import (
	"encoding/base64"
	"errors"
)

const KeySize = 32

var ErrInvalidKey = errors.New("secret key must decode to exactly 32 bytes")

// Decode32ByteKey accepts the encodings supported by runtime configuration.
// Callers decide whether surrounding whitespace is meaningful before calling.
func Decode32ByteKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != KeySize {
		clear(key)
		return nil, ErrInvalidKey
	}
	return key, nil
}
