// Package functionsecret encrypts trusted function and integration secrets at
// rest.
//
// Plaintext values are not returned by the API. The database stores an
// authenticated AES-GCM ciphertext rather than plaintext or a reversible
// ad-hoc encoding. The key is supplied by process configuration and is never
// persisted in PostgreSQL.
package functionsecret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const KeySize = 32

var ErrInvalidKey = errors.New("function secret key must be exactly 32 bytes")

// Cipher is safe for concurrent use. cipher.AEAD implementations returned by
// cipher.NewGCM do not mutate shared state during Seal/Open.
type Cipher struct {
	aead cipher.AEAD
}

func New(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM cipher: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns nonce || ciphertext. A fresh random nonce is generated for
// every value; callers should persist the returned bytes as-is.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidKey
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate function secret nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *Cipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidKey
	}
	nonceSize := c.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("invalid function secret ciphertext")
	}
	return c.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
}
