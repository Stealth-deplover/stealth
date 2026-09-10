// Package functionstore owns the local filesystem side of Function source
// deployments. Source archives are treated as opaque bytes: this package
// never extracts, inspects, or executes an upload.
//
// Paths are derived solely from tenant and object UUIDs. A user supplied
// filename is metadata and can never address a filesystem object.
package functionstore

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrTooLarge    = storage.ErrTooLarge
	ErrInvalidPath = storage.ErrInvalidPath
)

// Store is a separate namespace from user Storage. It delegates the
// hardened same-directory temporary-file, fsync, atomic-rename, and relative
// path checks to the common local blob implementation.
type Store struct {
	inner *storage.Store
}

// PreparedArtifact is a durable, fsynced temporary upload. Callers must
// Commit or Cleanup it exactly once.
type PreparedArtifact struct {
	TempPath     string
	RelativePath string
	Size         int64
	Checksum     string
	committed    bool
	inner        storage.PreparedFile
}

// PreparedFile is kept as an ergonomic alias for callers that use the same
// terminology as the Storage package.
type PreparedFile = PreparedArtifact

func New(root string, maxSize int64) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		root = filepath.Join("/var/lib/stealth/storage", "functions")
	}
	inner, err := storage.New(root, maxSize)
	if err != nil {
		return nil, err
	}
	return &Store{inner: inner}, nil
}

func (s *Store) Root() string {
	if s == nil || s.inner == nil {
		return ""
	}
	return s.inner.Root()
}

// BeginUpload writes the opaque source bytes into a temporary file. The
// temporary file is not visible at the final UUID path until Commit.
func (s *Store) BeginUpload(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, src io.Reader) (PreparedArtifact, error) {
	return s.BeginUploadWithLimit(ctx, projectID, functionID, deploymentID, src, 0)
}

// BeginUploadWithLimit enforces a per-request limit while retaining the
// Store's configured maximum as a hard ceiling.
func (s *Store) BeginUploadWithLimit(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, src io.Reader, maxSize int64) (PreparedArtifact, error) {
	if s == nil || s.inner == nil {
		return PreparedArtifact{}, ErrInvalidPath
	}
	prepared, err := s.inner.BeginUploadWithLimit(ctx, projectID, functionID, deploymentID, src, "application/octet-stream", maxSize)
	if err != nil {
		return PreparedArtifact{}, err
	}
	return PreparedArtifact{
		TempPath:     prepared.TempPath,
		RelativePath: prepared.RelativePath,
		Size:         prepared.Size,
		Checksum:     prepared.Checksum,
		inner:        prepared,
	}, nil
}

func (s *Store) Commit(artifact *PreparedArtifact) error {
	if artifact == nil || artifact.committed {
		return ErrInvalidPath
	}
	if s == nil || s.inner == nil {
		return ErrInvalidPath
	}
	if err := s.inner.Commit(&artifact.inner); err != nil {
		return err
	}
	artifact.committed = true
	return nil
}

func (s *Store) Cleanup(artifact *PreparedArtifact) {
	if artifact == nil || artifact.committed || s == nil || s.inner == nil {
		return
	}
	s.inner.Cleanup(&artifact.inner)
}

func (s *Store) RemoveRelative(relative string) error {
	if s == nil || s.inner == nil {
		return ErrInvalidPath
	}
	return s.inner.RemoveRelative(relative)
}

// RemoveProject removes all source/build artifacts belonging to a project.
// The underlying store validates the UUID-derived namespace before deleting.
func (s *Store) RemoveProject(projectID uuid.UUID) error {
	if s == nil || s.inner == nil {
		return ErrInvalidPath
	}
	return s.inner.RemoveProject(projectID)
}

func (s *Store) OpenRelative(relative string) (io.ReadCloser, error) {
	if s == nil || s.inner == nil {
		return nil, ErrInvalidPath
	}
	return s.inner.OpenRelative(relative)
}
