// Package sitestore owns the private filesystem namespace for published
// Sites. A deployment is an immutable directory addressed only by three
// server-generated UUIDv7 values; requested URL paths are metadata and never
// become database or artifact locators.
package sitestore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

var (
	ErrInvalidPath = errors.New("invalid site artifact path")
	ErrInvalidFile = errors.New("invalid site file path")
)

type Store struct{ root string }

func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, ErrInvalidPath
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve site storage root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create site storage root: %w", err)
	}
	return &Store{root: filepath.Clean(abs)}, nil
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func ArtifactRelativePath(projectID, siteID, deploymentID uuid.UUID) (string, error) {
	if projectID == uuid.Nil || siteID == uuid.Nil || deploymentID == uuid.Nil || projectID.Version() != uuid.Version(7) || siteID.Version() != uuid.Version(7) || deploymentID.Version() != uuid.Version(7) {
		return "", ErrInvalidPath
	}
	return strings.Join([]string{projectID.String(), siteID.String(), deploymentID.String()}, "/"), nil
}

// BeginStaging creates an unpublished directory under the site root. The
// caller must either CommitDirectory or CleanupStaging it.
func (s *Store) BeginStaging(projectID, siteID, deploymentID uuid.UUID) (string, string, error) {
	if s == nil || s.root == "" {
		return "", "", ErrInvalidPath
	}
	relative, err := ArtifactRelativePath(projectID, siteID, deploymentID)
	if err != nil {
		return "", "", err
	}
	parent := filepath.Join(s.root, projectID.String(), siteID.String())
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", "", fmt.Errorf("create site staging parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".site-upload-*")
	if err != nil {
		return "", "", fmt.Errorf("create site staging directory: %w", err)
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		_ = os.RemoveAll(staging)
		return "", "", fmt.Errorf("protect site staging directory: %w", err)
	}
	return staging, relative, nil
}

func (s *Store) CleanupStaging(staging string) {
	if s == nil || staging == "" {
		return
	}
	if s.insideRoot(staging) {
		_ = os.RemoveAll(staging)
	}
}

// CommitDirectory atomically publishes an extracted directory. The final
// path must not already exist; this makes deployment IDs immutable.
func (s *Store) CommitDirectory(staging, relative string) error {
	if s == nil || !s.insideRoot(staging) {
		return ErrInvalidPath
	}
	destination, err := s.resolveArtifact(relative)
	if err != nil {
		return err
	}
	info, err := os.Stat(staging)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrInvalidPath
	}
	// Site workers run with the Docker daemon's credentials while the API
	// serves the same volume as the unprivileged `stealth` user. Normalize
	// permissions at the publication boundary so a worker-created artifact is
	// readable/traversable by the API. Sites are public by definition; files
	// are therefore deliberately read-only after publication. Symlinks are
	// left untouched and remain rejected by OpenFile/checkedArtifact.
	ownerUID, ownerGID := s.publicOwner()
	if err := makePublicReadable(staging, ownerUID, ownerGID); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("site deployment destination already exists: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create site deployment parent: %w", err)
	}
	if err := makePublicParents(s.root, filepath.Dir(destination), ownerUID, ownerGID); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("publish site deployment: %w", err)
	}
	if directory, err := os.Open(filepath.Dir(destination)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func (s *Store) publicOwner() (int, int) {
	if s == nil || s.root == "" || os.Geteuid() != 0 {
		return -1, -1
	}
	info, err := os.Stat(s.root)
	if err != nil {
		return -1, -1
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, -1
	}
	return int(stat.Uid), int(stat.Gid)
}

func makePublicReadable(root string, ownerUID, ownerGID int) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Do not follow or mutate links. The public serving path performs a
		// second Lstat check and rejects them, preserving the defense in depth
		// even if a caller hands CommitDirectory an unexpected staging tree.
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if ownerUID >= 0 {
			if err := os.Chown(path, ownerUID, ownerGID); err != nil {
				return fmt.Errorf("set site artifact ownership: %w", err)
			}
		}
		mode := entry.Type()
		switch {
		case entry.IsDir():
			if err := os.Chmod(path, 0o755); err != nil {
				return fmt.Errorf("make site directory readable: %w", err)
			}
		case mode.IsRegular():
			if err := os.Chmod(path, 0o644); err != nil {
				return fmt.Errorf("make site file readable: %w", err)
			}
		}
		return nil
	})
}

func makePublicParents(root, destination string, ownerUID, ownerGID int) error {
	root = filepath.Clean(root)
	destination = filepath.Clean(destination)
	rel, err := filepath.Rel(root, destination)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrInvalidPath
	}
	current := root
	for _, segment := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		if err := os.Chmod(current, 0o755); err != nil {
			return fmt.Errorf("make site parent readable: %w", err)
		}
		if ownerUID >= 0 {
			if err := os.Chown(current, ownerUID, ownerGID); err != nil {
				return fmt.Errorf("set site parent ownership: %w", err)
			}
		}
	}
	return nil
}

func (s *Store) RemoveRelative(relative string) error {
	destination, err := s.resolveArtifact(relative)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return nil
}

// RemoveProject removes every published site and staged source artifact under
// a project namespace. The namespace is derived from a UUIDv7 and is checked
// with Lstat before recursive removal so custom-domain serving paths cannot be
// used to escape the store root.
func (s *Store) RemoveProject(projectID uuid.UUID) error {
	if s == nil || s.root == "" || projectID == uuid.Nil || projectID.Version() != uuid.Version(7) {
		return ErrInvalidPath
	}
	destination := filepath.Join(s.root, projectID.String())
	relative, err := filepath.Rel(s.root, destination)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ErrInvalidPath
	}
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidPath
	}
	return os.RemoveAll(destination)
}

// OpenFile validates every path component with Lstat before opening. The
// untrusted archive extractor rejects links, and this second check prevents a
// manually introduced link from turning a public request into a file read.
func (s *Store) OpenFile(relative, requested string) (*os.File, fs.FileInfo, error) {
	artifact, err := s.checkedArtifact(relative)
	if err != nil {
		return nil, nil, err
	}
	clean, segments, err := cleanRequestedPath(requested)
	if err != nil {
		return nil, nil, err
	}
	_ = clean
	current := artifact
	for _, segment := range segments[:len(segments)-1] {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return nil, nil, statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, ErrInvalidPath
		}
	}
	target := filepath.Join(append([]string{artifact}, segments...)...)
	info, err := os.Lstat(target)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrInvalidPath
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !openedInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, ErrInvalidPath
	}
	return file, openedInfo, nil
}

// checkedArtifact verifies the UUID namespace itself before a requested file
// is opened. The archive extractor rejects links, but checking the project,
// Site, and deployment directories as well prevents a host-level symlink from
// turning a valid-looking artifact path into an escape.
func (s *Store) checkedArtifact(relative string) (string, error) {
	artifact, err := s.resolveArtifact(relative)
	if err != nil {
		return "", err
	}
	current := s.root
	rootInfo, err := os.Lstat(current)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", ErrInvalidPath
	}
	for _, segment := range strings.Split(relative, "/") {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return "", statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrInvalidPath
		}
	}
	return artifact, nil
}

func ValidateEntrypoint(staging, entrypoint string) error {
	clean, segments, err := cleanRequestedPath(entrypoint)
	if err != nil || clean != "index.html" || len(segments) != 1 {
		return ErrInvalidFile
	}
	info, err := os.Lstat(filepath.Join(staging, segments[0]))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidFile
	}
	return nil
}

func (s *Store) resolveArtifact(relative string) (string, error) {
	if s == nil || s.root == "" {
		return "", ErrInvalidPath
	}
	parts := strings.Split(relative, "/")
	if len(parts) != 3 {
		return "", ErrInvalidPath
	}
	for _, part := range parts {
		id, err := uuid.Parse(part)
		if err != nil || id == uuid.Nil || id.Version() != uuid.Version(7) {
			return "", ErrInvalidPath
		}
	}
	full := filepath.Join(s.root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(s.root, full)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	return full, nil
}

func (s *Store) insideRoot(value string) bool {
	if s == nil || s.root == "" {
		return false
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(s.root, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cleanRequestedPath(value string) (string, []string, error) {
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		value = "index.html"
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", nil, ErrInvalidFile
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", nil, ErrInvalidFile
		}
	}
	clean := path.Join(parts...)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", nil, ErrInvalidFile
	}
	return clean, parts, nil
}
