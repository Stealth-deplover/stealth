// Package setuphandoff contains the short-lived, encrypted bridge from the
// setup host to the final production host. The bridge stores an existing
// session token only inside ciphertext and consumes it before returning it.
package setuphandoff

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
)

const (
	sharedFileMode = 0o640
	lockFileMode   = 0o640
)

var (
	ErrUnavailable = errors.New("setup handoff is unavailable")
	ErrInvalid     = errors.New("setup handoff is invalid or expired")
)

type Store interface {
	Save(context.Context, string, string, time.Time) error
	Issue(context.Context) (string, error)
	Consume(context.Context, string) (string, error)
	Discard(context.Context) error
}

type FileStore struct {
	path   string
	cipher *functionsecret.Cipher
	mu     sync.Mutex
}

type handoff struct {
	TokenHash    []byte    `json:"token_hash"`
	SessionToken string    `json:"session_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func NewFileStore(path string, cipher *functionsecret.Cipher) (*FileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" || cipher == nil {
		return nil, ErrUnavailable
	}
	abs, err := filepath.Abs(path)
	if err != nil || filepath.Clean(abs) == string(filepath.Separator) {
		return nil, ErrUnavailable
	}
	return &FileStore{path: filepath.Clean(abs), cipher: cipher}, nil
}

func (s *FileStore) Save(ctx context.Context, token, sessionToken string, expiresAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.cipher == nil || !validToken(token) || !validToken(sessionToken) || !expiresAt.After(time.Now().UTC()) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	value := handoff{TokenHash: hash(token), SessionToken: sessionToken, ExpiresAt: expiresAt}
	return s.save(value)
}

func (s *FileStore) Consume(ctx context.Context, token string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s == nil || s.cipher == nil || !validToken(token) {
		return "", ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	value, err := s.load()
	if err != nil || subtle.ConstantTimeCompare(value.TokenHash, hash(token)) != 1 {
		return "", ErrInvalid
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("consume setup handoff: %w", err)
	}
	return value.SessionToken, nil
}

// Pending reports whether an unexpired handoff is still waiting for the
// production API to consume it. It is intentionally a FileStore method rather
// than part of Store: only the setup-side cleanup watcher needs to observe the
// shared file, while production only needs the consume operation.
func (s *FileStore) Pending(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s == nil || s.cipher == nil {
		return false, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	_, err = s.load()
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrInvalid) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Issue rotates the single-use browser ticket without exposing the underlying
// production session token. This lets a refreshed setup browser recover a
// handoff while its authenticated setup claim is still valid.
func (s *FileStore) Issue(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s == nil || s.cipher == nil {
		return "", ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	value, err := s.load()
	if err != nil {
		return "", ErrInvalid
	}
	token, _, err := auth.NewSessionToken()
	if err != nil {
		return "", fmt.Errorf("generate setup handoff: %w", err)
	}
	value.TokenHash = hash(token)
	if err := s.save(value); err != nil {
		return "", err
	}
	return token, nil
}

func (s *FileStore) Discard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("discard setup handoff: %w", err)
	}
	return nil
}

func (s *FileStore) save(value handoff) error {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode setup handoff: %w", err)
	}
	ciphertext, err := s.cipher.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypt setup handoff: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o770); err != nil {
		return fmt.Errorf("create setup handoff directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".setup-handoff-*")
	if err != nil {
		return fmt.Errorf("create setup handoff: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(sharedFileMode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(ciphertext); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write setup handoff: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("commit setup handoff: %w", err)
	}
	if err := setSharedGroup(s.path); err != nil {
		return err
	}
	return os.Chmod(s.path, sharedFileMode)
}

func (s *FileStore) lock() (func(), error) {
	lockFile, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("open setup handoff lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock setup handoff: %w", err)
	}
	_ = setSharedGroup(s.path + ".lock")
	return func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}, nil
}

func (s *FileStore) load() (handoff, error) {
	contents, err := os.ReadFile(s.path)
	if err != nil {
		return handoff{}, err
	}
	plaintext, err := s.cipher.Decrypt(contents)
	if err != nil {
		return handoff{}, ErrInvalid
	}
	var value handoff
	if err := json.Unmarshal(plaintext, &value); err != nil || !validToken(value.SessionToken) || len(value.TokenHash) != sha256.Size || !value.ExpiresAt.After(time.Now().UTC()) {
		return handoff{}, ErrInvalid
	}
	return value, nil
}

func setSharedGroup(path string) error {
	group, err := osuser.LookupGroup("stealth")
	if err != nil {
		return nil
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("resolve Stealth group id: %w", err)
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return fmt.Errorf("set Stealth group on setup handoff: %w", err)
	}
	return nil
}

func validToken(value string) bool {
	return auth.ValidateToken(strings.TrimSpace(value)) == nil
}

func hash(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}
