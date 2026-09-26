package appsecret

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

func TestCipherRoundTripUsesFreshNonceAndAuthenticatedIdentity(t *testing.T) {
	cipher, err := New(bytes.Repeat([]byte{0x4d}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	projectID, appID, variableID := uuid.New(), uuid.New(), uuid.New()
	first, err := cipher.Encrypt(projectID, appID, variableID, []byte("example secret"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt(projectID, appID, variableID, []byte("example secret"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("Encrypt reused a nonce")
	}
	if first[0] != ciphertextVersion {
		t.Fatalf("ciphertext version = %d, want %d", first[0], ciphertextVersion)
	}
	plaintext, err := cipher.Decrypt(projectID, appID, variableID, first)
	if err != nil || string(plaintext) != "example secret" {
		t.Fatalf("Decrypt() = %q, %v", plaintext, err)
	}
	for _, identity := range [][3]uuid.UUID{
		{uuid.New(), appID, variableID},
		{projectID, uuid.New(), variableID},
		{projectID, appID, uuid.New()},
	} {
		if _, err := cipher.Decrypt(identity[0], identity[1], identity[2], first); err == nil {
			t.Fatal("Decrypt accepted ciphertext under a different authenticated identity")
		}
	}
	tampered := append([]byte(nil), first...)
	tampered[len(tampered)-1] ^= 1
	if _, err := cipher.Decrypt(projectID, appID, variableID, tampered); err == nil {
		t.Fatal("Decrypt accepted modified ciphertext")
	}
}

func TestCipherRejectsInvalidKeyAndVersion(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("New accepted a non-32-byte key")
	}
	cipher, err := New(bytes.Repeat([]byte{0x71}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	projectID, appID, variableID := uuid.New(), uuid.New(), uuid.New()
	encoded, err := cipher.Encrypt(projectID, appID, variableID, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded[0]++
	if _, err := cipher.Decrypt(projectID, appID, variableID, encoded); err == nil {
		t.Fatal("Decrypt accepted an unknown ciphertext version")
	}
}
