package setuphandoff

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
)

func TestFileStoreEncryptsAndConsumesHandoffOnce(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x3a}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "handoff.enc")
	store, err := NewFileStore(path, cipher)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), token, sessionToken, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte(sessionToken)) || bytes.Contains(contents, []byte(token)) {
		t.Fatal("handoff file contains a plaintext credential")
	}
	if mode := fileMode(t, path); mode&0o077 != 0o040 {
		t.Fatalf("handoff file mode = %o, want group-readable and no other access", mode)
	}
	consumed, err := store.Consume(context.Background(), token)
	if err != nil || consumed != sessionToken {
		t.Fatalf("Consume() = %q, %v", consumed, err)
	}
	if _, err := store.Consume(context.Background(), token); err == nil {
		t.Fatal("handoff was reusable")
	}
}

func TestFileStoreRejectsExpiredOrWrongToken(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x4b}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(filepath.Join(t.TempDir(), "handoff.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	token, _, _ := auth.NewSessionToken()
	sessionToken, _, _ := auth.NewSessionToken()
	if err := store.Save(context.Background(), token, sessionToken, time.Now().UTC().Add(-time.Minute)); err == nil {
		t.Fatal("expired handoff was saved")
	}
	if err := store.Save(context.Background(), token, sessionToken, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	wrong, _, _ := auth.NewSessionToken()
	if _, err := store.Consume(context.Background(), wrong); err == nil {
		t.Fatal("wrong handoff token was accepted")
	}
	if err := store.Discard(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreIssuesReplacementTicketWithoutChangingSession(t *testing.T) {
	cipher, err := functionsecret.New(bytes.Repeat([]byte{0x57}, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(filepath.Join(t.TempDir(), "handoff.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, _, _ := auth.NewSessionToken()
	sessionToken, _, _ := auth.NewSessionToken()
	if err := store.Save(context.Background(), oldToken, sessionToken, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	newToken, err := store.Issue(context.Background())
	if err != nil || newToken == oldToken {
		t.Fatalf("Issue() = %q, %v", newToken, err)
	}
	if _, err := store.Consume(context.Background(), oldToken); err == nil {
		t.Fatal("rotated handoff accepted the old ticket")
	}
	consumed, err := store.Consume(context.Background(), newToken)
	if err != nil || consumed != sessionToken {
		t.Fatalf("Consume() = %q, %v", consumed, err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
