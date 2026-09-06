// Package storage owns the local filesystem side of Storage. Database rows
// contain only opaque, UUID-derived relative paths; user supplied filenames
// are metadata and are never used to address a filesystem object.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const (
	defaultRoot      = "/var/lib/stealth/storage"
	maxFilenameBytes = 255
	maxSniffBytes    = 512
)

var (
	ErrTooLarge        = errors.New("file exceeds the configured maximum size")
	ErrInvalidPath     = errors.New("invalid internal storage path")
	ErrInvalidFilename = errors.New("invalid filename")
	ErrInvalidMIME     = errors.New("invalid MIME type")
)

// ReadSeekCloser is the stream returned to HTTP handlers. Local storage uses
// an *os.File; remote backends may materialize a temporary download so
// http.ServeContent can still support range/seek semantics safely.
type ReadSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

// BlobStore is the storage contract shared by local and S3-compatible
// backends. Database metadata remains the source of truth for ownership and
// permissions; the store only publishes and retrieves opaque blob paths.
type BlobStore interface {
	Ping(context.Context) error
	BeginUploadWithLimit(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, io.Reader, string, int64) (PreparedFile, error)
	Commit(*PreparedFile) error
	Cleanup(*PreparedFile)
	RemoveRelative(string) error
	RemoveProject(uuid.UUID) error
	OpenRelative(string) (ReadSeekCloser, error)
}

// Store is a self-hosted local blob store. A Store is safe for concurrent use;
// PostgreSQL owns quota/accounting serialization while this type owns atomic
// file creation and path validation.
type Store struct {
	root    string
	maxSize int64
}

// PreparedFile is a durable, fsynced temporary upload. It must be either
// Commit'ed or Cleanup'ed by its caller.
type PreparedFile struct {
	TempPath     string
	RelativePath string
	Size         int64
	Checksum     string
	ContentType  string
	committed    bool
}

func New(root string, maxSize int64) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = defaultRoot
	}
	if maxSize <= 0 {
		return nil, fmt.Errorf("storage max size must be positive")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &Store{root: filepath.Clean(abs), maxSize: maxSize}, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) Ping(context.Context) error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return ErrInvalidPath
	}
	return nil
}

// ValidateFilename validates a display filename without normalizing it into a
// path. It rejects separators, control characters, dot-segments, and header
// injection characters. Unicode letters/spaces are allowed.
func ValidateFilename(name string) error {
	if name == "" || len([]byte(name)) > maxFilenameBytes || strings.TrimSpace(name) != name || name == "." || name == ".." {
		return ErrInvalidFilename
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == '\x00' || r == '\r' || r == '\n' || unicode.IsControl(r) {
			return ErrInvalidFilename
		}
	}
	return nil
}

// NormalizeContentType accepts a single media type and strips parameters.
// Invalid or unsafe values are rejected instead of being copied into a
// response header.
func NormalizeContentType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || mediaType == "" || strings.ContainsAny(mediaType, "\r\n") {
		return "", ErrInvalidMIME
	}
	return strings.ToLower(mediaType), nil
}

// BeginUpload streams src to a same-directory temporary file, enforcing the
// byte limit and hashing while writing. The destination is not visible until
// Commit performs an atomic rename.
func (s *Store) BeginUpload(ctx context.Context, projectID, bucketID, fileID uuid.UUID, src io.Reader, declaredType string) (PreparedFile, error) {
	return s.BeginUploadWithLimit(ctx, projectID, bucketID, fileID, src, declaredType, s.maxSize)
}

// BeginUploadWithLimit is the bucket-aware variant. The configured global
// limit remains a hard ceiling even when a caller supplies a larger bucket
// value.
func (s *Store) BeginUploadWithLimit(ctx context.Context, projectID, bucketID, fileID uuid.UUID, src io.Reader, declaredType string, maxSize int64) (PreparedFile, error) {
	if projectID == uuid.Nil || bucketID == uuid.Nil || fileID == uuid.Nil {
		return PreparedFile{}, ErrInvalidPath
	}
	if maxSize <= 0 || maxSize > s.maxSize {
		maxSize = s.maxSize
	}
	relative := internalRelativePath(projectID, bucketID, fileID)
	directory := filepath.Dir(filepath.Join(s.root, filepath.FromSlash(relative)))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return PreparedFile{}, fmt.Errorf("create upload directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return PreparedFile{}, fmt.Errorf("create upload temporary file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return PreparedFile{}, fmt.Errorf("protect upload temporary file: %w", err)
	}

	readLimit := maxSize
	if maxSize < math.MaxInt64 {
		readLimit++
	}
	limited := &contextReader{ctx: ctx, reader: io.LimitReader(src, readLimit)}
	hasher := sha256.New()
	tee := io.TeeReader(limited, hasher)
	var sniff sniffWriter
	written, err := io.CopyBuffer(io.MultiWriter(temp, &sniff), tee, make([]byte, 32*1024))
	if err != nil {
		cleanup()
		return PreparedFile{}, err
	}
	if written > maxSize {
		cleanup()
		return PreparedFile{}, ErrTooLarge
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return PreparedFile{}, fmt.Errorf("sync upload temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return PreparedFile{}, fmt.Errorf("close upload temporary file: %w", err)
	}
	contentType, err := NormalizeContentType(declaredType)
	if err != nil {
		_ = os.Remove(tempPath)
		return PreparedFile{}, err
	}
	if contentType == "" || contentType == "application/octet-stream" {
		contentType, err = NormalizeContentType(http.DetectContentType(sniff.bytes()))
		if err != nil {
			_ = os.Remove(tempPath)
			return PreparedFile{}, err
		}
	}
	return PreparedFile{
		TempPath:     tempPath,
		RelativePath: relative,
		Size:         written,
		Checksum:     hex.EncodeToString(hasher.Sum(nil)),
		ContentType:  contentType,
	}, nil
}

// Commit atomically publishes a prepared file under its UUID-derived path and
// fsyncs its parent directory where the platform supports directory sync.
func (s *Store) Commit(file *PreparedFile) error {
	if file == nil || file.TempPath == "" || file.RelativePath == "" || file.committed {
		return ErrInvalidPath
	}
	destination, err := s.resolveRelative(file.RelativePath)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("storage destination already exists: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(file.TempPath, destination); err != nil {
		return fmt.Errorf("publish upload: %w", err)
	}
	file.committed = true
	directory, _ := filepath.Abs(filepath.Dir(destination))
	if dir, err := os.Open(directory); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *Store) Cleanup(file *PreparedFile) {
	if file == nil || file.committed || file.TempPath == "" {
		return
	}
	_ = os.Remove(file.TempPath)
}

func (s *Store) RemoveRelative(relative string) error {
	path, err := s.resolveRelative(relative)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveProject removes the complete filesystem namespace for one project.
// Project IDs are server-generated UUIDv7 values and are the only accepted
// directory selector; symlinked namespaces are rejected before RemoveAll so
// a project deletion can never escape this store's root.
func (s *Store) RemoveProject(projectID uuid.UUID) error {
	if s == nil || projectID == uuid.Nil || projectID.Version() != uuid.Version(7) {
		return ErrInvalidPath
	}
	path, err := s.resolveRelative(projectID.String())
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidPath
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return nil
}

func (s *Store) OpenRelative(relative string) (ReadSeekCloser, error) {
	path, err := s.resolveRelative(relative)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrInvalidPath
	}
	return file, nil
}

func (s *Store) resolveRelative(relative string) (string, error) {
	relative = filepath.Clean(filepath.FromSlash(relative))
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	full := filepath.Join(s.root, relative)
	base, err := filepath.Rel(s.root, full)
	if err != nil || base != relative || base == ".." || strings.HasPrefix(base, ".."+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	return full, nil
}

func internalRelativePath(projectID, bucketID, fileID uuid.UUID) string {
	return filepath.ToSlash(filepath.Join(projectID.String(), bucketID.String(), fileID.String()))
}

type sniffWriter struct{ data []byte }

func (w *sniffWriter) Write(p []byte) (int, error) {
	if len(w.data) < maxSniffBytes {
		remaining := maxSniffBytes - len(w.data)
		if remaining > len(p) {
			remaining = len(p)
		}
		w.data = append(w.data, p[:remaining]...)
	}
	return len(p), nil
}

func (w *sniffWriter) bytes() []byte { return w.data }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}
