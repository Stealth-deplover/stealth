package functionsecret

import (
	"bytes"
	"errors"
	"testing"
)

func TestCipherRoundTripAndRandomNonce(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, KeySize)
	cipher, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cipher.Encrypt([]byte("do-not-return-this"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt([]byte("do-not-return-this"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte("do-not-return-this")) {
		t.Fatal("ciphertext reused a nonce or contains plaintext")
	}
	plain, err := cipher.Decrypt(first)
	if err != nil || string(plain) != "do-not-return-this" {
		t.Fatalf("Decrypt() = %q, %v", plain, err)
	}
	first[len(first)-1] ^= 1
	if _, err := cipher.Decrypt(first); err == nil {
		t.Fatal("tampered ciphertext unexpectedly decrypted")
	}
}

func TestCipherRejectsInvalidKeysAndCiphertexts(t *testing.T) {
	if _, err := New(make([]byte, KeySize-1)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid key error = %v", err)
	}
	cipher, err := New(bytes.Repeat([]byte{1}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.Decrypt([]byte("short")); err == nil {
		t.Fatal("short ciphertext unexpectedly decrypted")
	}
}
