// Package appstore owns private, immutable source and OCI artifacts for App
// deployments. Both namespaces use UUID-derived paths and the shared storage
// implementation's fsync and atomic-publication guarantees.
package appstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrTooLarge    = storage.ErrTooLarge
	ErrInvalidPath = storage.ErrInvalidPath
)

type Store struct {
	Sources *Namespace
	Images  *Namespace
}

type Namespace struct {
	inner    *storage.Store
	maxBytes int64
}

type PreparedArtifact struct {
	TempPath     string
	RelativePath string
	Size         int64
	Checksum     string
	inner        storage.PreparedFile
	committed    bool
}

func New(root string, maxSourceBytes, maxImageBytes int64) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		root = "/var/lib/stealth/storage"
	}
	sources, err := newNamespace(filepath.Join(root, "app-sources"), maxSourceBytes)
	if err != nil {
		return nil, err
	}
	images, err := newNamespace(filepath.Join(root, "app-images"), maxImageBytes)
	if err != nil {
		return nil, err
	}
	return &Store{Sources: sources, Images: images}, nil
}

func newNamespace(root string, maxBytes int64) (*Namespace, error) {
	inner, err := storage.New(root, maxBytes)
	if err != nil {
		return nil, err
	}
	return &Namespace{inner: inner, maxBytes: maxBytes}, nil
}

func (n *Namespace) MaxBytes() int64 {
	if n == nil {
		return 0
	}
	return n.maxBytes
}

func (n *Namespace) Root() string {
	if n == nil || n.inner == nil {
		return ""
	}
	return n.inner.Root()
}

func (n *Namespace) Ping(ctx context.Context) error {
	if n == nil || n.inner == nil {
		return ErrInvalidPath
	}
	return n.inner.Ping(ctx)
}

func (n *Namespace) BeginUpload(ctx context.Context, projectID, appID, artifactID uuid.UUID, src io.Reader, maxBytes int64) (PreparedArtifact, error) {
	if n == nil || n.inner == nil {
		return PreparedArtifact{}, ErrInvalidPath
	}
	prepared, err := n.inner.BeginUploadWithLimit(ctx, projectID, appID, artifactID, src, "application/octet-stream", maxBytes)
	if err != nil {
		return PreparedArtifact{}, err
	}
	return PreparedArtifact{
		TempPath: prepared.TempPath, RelativePath: prepared.RelativePath,
		Size: prepared.Size, Checksum: prepared.Checksum, inner: prepared,
	}, nil
}

func (n *Namespace) Commit(ctx context.Context, artifact *PreparedArtifact) error {
	if n == nil || n.inner == nil || artifact == nil || artifact.committed {
		return ErrInvalidPath
	}
	if err := n.inner.Commit(ctx, &artifact.inner); err != nil {
		return err
	}
	artifact.committed = true
	return nil
}

func (n *Namespace) Cleanup(artifact *PreparedArtifact) {
	if n == nil || n.inner == nil || artifact == nil || artifact.committed {
		return
	}
	n.inner.Cleanup(&artifact.inner)
}

func (n *Namespace) OpenRelative(ctx context.Context, relative string) (storage.ReadSeekCloser, error) {
	if n == nil || n.inner == nil {
		return nil, ErrInvalidPath
	}
	return n.inner.OpenRelative(ctx, relative)
}

func (n *Namespace) RemoveRelative(ctx context.Context, relative string) error {
	if n == nil || n.inner == nil {
		return ErrInvalidPath
	}
	return n.inner.RemoveRelative(ctx, relative)
}

func (n *Namespace) RemoveProject(ctx context.Context, projectID uuid.UUID) error {
	if n == nil || n.inner == nil {
		return ErrInvalidPath
	}
	return n.inner.RemoveProject(ctx, projectID)
}

// CleanupStaleUploads removes hidden, unpublished upload staging files left
// when a process exits before its normal PreparedArtifact cleanup runs. Only
// old regular files with the storage implementation's private prefix are
// eligible; committed UUID paths and symlinks are never traversed.
func (n *Namespace) CleanupStaleUploads(ctx context.Context, maxAge time.Duration) (int, error) {
	if n == nil || n.inner == nil || maxAge <= 0 {
		return 0, ErrInvalidPath
	}
	root := n.inner.Root()
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return 0, ErrInvalidPath
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".upload-") {
			return nil
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrInvalidPath
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}
