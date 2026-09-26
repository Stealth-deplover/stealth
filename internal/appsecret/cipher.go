// Package appsecret encrypts per-App environment values under a dedicated
// operator key. Each value authenticates its project, App, and variable
// identity so ciphertext cannot be moved to a different record.
package appsecret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

const (
	KeySize           = 32
	ciphertextVersion = byte(1)
)

var (
	ErrInvalidKey        = errors.New("App secret key must be exactly 32 bytes")
	ErrInvalidCiphertext = errors.New("invalid App environment ciphertext")
)

type Cipher struct {
	aead cipher.AEAD
}

func New(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create App secret AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create App secret AES-GCM cipher: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns version || nonce || authenticated ciphertext. A fresh
// nonce is generated for every value.
func (c *Cipher) Encrypt(projectID, appID, variableID uuid.UUID, plaintext []byte) ([]byte, error) {
	if c == nil || c.aead == nil || projectID == uuid.Nil || appID == uuid.Nil || variableID == uuid.Nil {
		return nil, ErrInvalidKey
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate App secret nonce: %w", err)
	}
	result := make([]byte, 1, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	result[0] = ciphertextVersion
	result = append(result, nonce...)
	result = c.aead.Seal(result, nonce, plaintext, associatedData(projectID, appID, variableID))
	return result, nil
}

func (c *Cipher) Decrypt(projectID, appID, variableID uuid.UUID, encoded []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidKey
	}
	nonceSize := c.aead.NonceSize()
	if projectID == uuid.Nil || appID == uuid.Nil || variableID == uuid.Nil || len(encoded) < 1+nonceSize+c.aead.Overhead() || encoded[0] != ciphertextVersion {
		return nil, ErrInvalidCiphertext
	}
	nonce := encoded[1 : 1+nonceSize]
	plaintext, err := c.aead.Open(nil, nonce, encoded[1+nonceSize:], associatedData(projectID, appID, variableID))
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return plaintext, nil
}

func associatedData(projectID, appID, variableID uuid.UUID) []byte {
	return []byte("stealth/app-environment/v1\x00" + projectID.String() + "\x00" + appID.String() + "\x00" + variableID.String())
}
