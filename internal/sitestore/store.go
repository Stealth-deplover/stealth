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

type Store struct {
	root    string
	rootDir *os.Root
}

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
	abs = filepath.Clean(abs)
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("inspect site storage root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidPath
	}
	rootDir, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open site storage root: %w", err)
	}
	return &Store{root: abs, rootDir: rootDir}, nil
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
	if s == nil || s.root == "" || s.rootDir == nil {
		return "", "", ErrInvalidPath
	}
	relative, err := ArtifactRelativePath(projectID, siteID, deploymentID)
	if err != nil {
		return "", "", err
	}
	parentRelative := filepath.Join(projectID.String(), siteID.String())
	if err := ensureDirectoryTree(s.rootDir, parentRelative, 0o700); err != nil {
		return "", "", fmt.Errorf("create site staging parent: %w", err)
	}
	var stagingRelative string
	for attempt := 0; attempt < 3; attempt++ {
		stagingID, err := uuid.NewV7()
		if err != nil {
			return "", "", fmt.Errorf("create site staging identifier: %w", err)
		}
		stagingRelative = filepath.Join(parentRelative, ".site-upload-"+stagingID.String())
		if err := s.rootDir.Mkdir(stagingRelative, 0o700); err == nil {
			break
		} else if !errors.Is(err, os.ErrExist) {
			return "", "", fmt.Errorf("create site staging directory: %w", err)
		}
		stagingRelative = ""
	}
	if stagingRelative == "" {
		return "", "", fmt.Errorf("create site staging directory: temporary name collision")
	}
	staging := filepath.Join(s.root, stagingRelative)
	return staging, relative, nil
}

func (s *Store) CleanupStaging(staging string) {
	if s == nil || staging == "" {
		return
	}
	relative, err := s.relativePath(staging)
	if err != nil || s.rootDir == nil {
		return
	}
	_ = s.rootDir.RemoveAll(filepath.FromSlash(relative))
}

// CommitDirectory atomically publishes an extracted directory. The final
// path must not already exist; this makes deployment IDs immutable.
func (s *Store) CommitDirectory(staging, relative string) error {
	if s == nil || s.rootDir == nil || !s.insideRoot(staging) {
		return ErrInvalidPath
	}
	destination, err := s.resolveArtifact(relative)
	if err != nil {
		return err
	}
	stagingRelative, err := s.relativePath(staging)
	if err != nil || !strings.HasPrefix(filepath.Base(stagingRelative), ".site-upload-") {
		return ErrInvalidPath
	}
	if err := validateDirectoryTree(s.rootDir, stagingRelative); err != nil {
		return err
	}
	destinationRelative, err := s.relativePath(destination)
	if err != nil {
		return ErrInvalidPath
	}
	info, err := s.rootDir.Lstat(filepath.FromSlash(stagingRelative))
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidPath
	}
	// Site workers run with the Docker daemon's credentials while the API
	// serves the same volume as the unprivileged `stealth` user. Normalize
	// permissions at the publication boundary so a worker-created artifact is
	// readable/traversable by the API. Sites are public by definition; files
	// are therefore deliberately read-only after publication. Symlinks are
	// left untouched and remain rejected by OpenFile/checkedArtifact.
	ownerUID, ownerGID := s.publicOwner()
	if err := makePublicReadable(s.rootDir, stagingRelative, ownerUID, ownerGID); err != nil {
		return err
	}
	if _, err := s.rootDir.Lstat(filepath.FromSlash(destinationRelative)); err == nil {
		return fmt.Errorf("site deployment destination already exists: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	destinationParent := filepath.Dir(filepath.FromSlash(destinationRelative))
	if err := ensureDirectoryTree(s.rootDir, destinationParent, 0o755); err != nil {
		return fmt.Errorf("create site deployment parent: %w", err)
	}
	if err := makePublicParents(s.rootDir, destinationParent, ownerUID, ownerGID); err != nil {
		return err
	}
	if err := s.rootDir.Rename(filepath.FromSlash(stagingRelative), filepath.FromSlash(destinationRelative)); err != nil {
		return fmt.Errorf("publish site deployment: %w", err)
	}
	if directory, err := s.rootDir.Open(destinationParent); err == nil {
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

func makePublicReadable(root *os.Root, relative string, ownerUID, ownerGID int) error {
	if root == nil || relative == "" {
		return ErrInvalidPath
	}
	return fs.WalkDir(root.FS(), filepath.ToSlash(relative), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Do not follow or mutate links. The public serving path performs a
		// second Lstat check and rejects them, preserving the defense in depth
		// even if a caller hands CommitDirectory an unexpected staging tree.
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		mode := entry.Type()
		if !entry.IsDir() && !mode.IsRegular() {
			return nil
		}
		file, err := root.Open(filepath.FromSlash(path))
		if err != nil {
			return err
		}
		if ownerUID >= 0 {
			if err := file.Chown(ownerUID, ownerGID); err != nil {
				_ = file.Close()
				return fmt.Errorf("set site artifact ownership: %w", err)
			}
		}
		switch {
		case entry.IsDir():
			if err := file.Chmod(0o755); err != nil {
				_ = file.Close()
				return fmt.Errorf("make site directory readable: %w", err)
			}
		case mode.IsRegular():
			if err := file.Chmod(0o644); err != nil {
				_ = file.Close()
				return fmt.Errorf("make site file readable: %w", err)
			}
		}
		return file.Close()
	})
}

func makePublicParents(root *os.Root, destination string, ownerUID, ownerGID int) error {
	if root == nil || destination == "" || destination == "." {
		return ErrInvalidPath
	}
	if err := validateDirectoryTree(root, destination); err != nil {
		return err
	}
	current := ""
	for _, segment := range strings.Split(filepath.ToSlash(destination), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidPath
		}
		current = filepath.Join(current, segment)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidPath
		}
		file, err := root.Open(current)
		if err != nil {
			return err
		}
		if err := file.Chmod(0o755); err != nil {
			_ = file.Close()
			return fmt.Errorf("make site parent readable: %w", err)
		}
		if ownerUID >= 0 {
			if err := file.Chown(ownerUID, ownerGID); err != nil {
				_ = file.Close()
				return fmt.Errorf("set site parent ownership: %w", err)
			}
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

// ensureDirectoryTree creates each component independently through the open
// root. MkdirAll may follow a pre-existing symlink to another directory in
// the root, which would break the project/site namespace even when the link
// cannot escape the storage root.
func ensureDirectoryTree(root *os.Root, relative string, perm os.FileMode) error {
	if root == nil || relative == "" || relative == "." {
		return ErrInvalidPath
	}
	current := ""
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidPath
		}
		current = filepath.Join(current, segment)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(current, perm); err == nil {
				continue
			} else if !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidPath
		}
	}
	return nil
}

func validateDirectoryTree(root *os.Root, relative string) error {
	if root == nil || relative == "" || relative == "." {
		return ErrInvalidPath
	}
	current := ""
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidPath
		}
		current = filepath.Join(current, segment)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidPath
		}
	}
	return nil
}

func (s *Store) RemoveRelative(relative string) error {
	canonical, err := artifactRelativePath(relative)
	if err != nil {
		return err
	}
	if s == nil || s.rootDir == nil {
		return ErrInvalidPath
	}
	return s.rootDir.RemoveAll(filepath.FromSlash(canonical))
}

// RemoveProject removes every published site and staged source artifact under
// a project namespace. The namespace is derived from a UUIDv7 and is checked
// with Lstat before recursive removal so custom-domain serving paths cannot be
// used to escape the store root.
func (s *Store) RemoveProject(projectID uuid.UUID) error {
	if s == nil || s.rootDir == nil || projectID == uuid.Nil || projectID.Version() != uuid.Version(7) {
		return ErrInvalidPath
	}
	relative := projectID.String()
	info, err := s.rootDir.Lstat(filepath.FromSlash(relative))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidPath
	}
	return s.rootDir.RemoveAll(filepath.FromSlash(relative))
}

// OpenFile validates every path component with Lstat before opening. The
// untrusted archive extractor rejects links, and the rooted file handle keeps
// the final open inside the immutable artifact even if a local process races
// the validation with a rename.
func (s *Store) OpenFile(relative, requested string) (*os.File, fs.FileInfo, error) {
	artifact, canonical, err := s.checkedArtifact(relative)
	if err != nil {
		return nil, nil, err
	}
	clean, segments, err := cleanRequestedPath(requested)
	if err != nil {
		return nil, nil, err
	}
	target, err := safePathWithin(artifact, clean)
	if err != nil {
		return nil, nil, ErrInvalidFile
	}
	targetRelative, err := filepath.Rel(artifact, target)
	if err != nil || targetRelative == "." || targetRelative == ".." || strings.HasPrefix(targetRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(targetRelative) || filepath.ToSlash(targetRelative) != clean {
		return nil, nil, ErrInvalidFile
	}
	artifactRoot, err := s.rootDir.OpenRoot(filepath.FromSlash(canonical))
	if err != nil {
		return nil, nil, err
	}
	defer artifactRoot.Close()
	for index := range segments {
		segmentPath := filepath.Join(segments[:index+1]...)
		info, statErr := artifactRoot.Lstat(segmentPath)
		if statErr != nil {
			return nil, nil, statErr
		}
		if (!info.Mode().IsRegular() && !info.IsDir()) || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, ErrInvalidPath
		}
		if index < len(segments)-1 && !info.IsDir() {
			return nil, nil, ErrInvalidPath
		}
	}
	file, err := artifactRoot.Open(filepath.FromSlash(clean))
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
// is opened. The root handle checks each component without following a
// symlink, preventing a host-level link from turning a valid-looking artifact
// path into an escape.
func (s *Store) checkedArtifact(relative string) (string, string, error) {
	canonical, err := artifactRelativePath(relative)
	if err != nil {
		return "", "", err
	}
	if s == nil || s.rootDir == nil {
		return "", "", ErrInvalidPath
	}
	current := ""
	for _, segment := range strings.Split(canonical, "/") {
		current = path.Join(current, segment)
		info, statErr := s.rootDir.Lstat(filepath.FromSlash(current))
		if statErr != nil {
			return "", "", statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", ErrInvalidPath
		}
	}
	artifact, err := s.resolveArtifact(canonical)
	if err != nil {
		return "", "", err
	}
	return artifact, canonical, nil
}

func ValidateEntrypoint(staging, entrypoint string) error {
	clean, segments, err := cleanRequestedPath(entrypoint)
	if err != nil || clean != "index.html" || len(segments) != 1 {
		return ErrInvalidFile
	}
	target, err := safePathWithin(staging, clean)
	if err != nil {
		return ErrInvalidFile
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidFile
	}
	return nil
}

func (s *Store) resolveArtifact(relative string) (string, error) {
	canonical, err := artifactRelativePath(relative)
	if err != nil {
		return "", ErrInvalidPath
	}
	if s == nil || s.root == "" {
		return "", ErrInvalidPath
	}
	full, err := safePathWithin(s.root, canonical)
	if err != nil {
		return "", ErrInvalidPath
	}
	return full, nil
}

func (s *Store) insideRoot(value string) bool {
	_, err := s.relativePath(value)
	return err == nil
}

func (s *Store) relativePath(value string) (string, error) {
	if s == nil || s.root == "" || strings.TrimSpace(value) == "" {
		return "", ErrInvalidPath
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", ErrInvalidPath
	}
	rel, err := filepath.Rel(filepath.Clean(s.root), filepath.Clean(abs))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrInvalidPath
	}
	return filepath.ToSlash(rel), nil
}

func safePathWithin(root, relative string) (string, error) {
	base, err := filepath.Abs(root)
	if err != nil || strings.TrimSpace(base) == "" {
		return "", ErrInvalidPath
	}
	nativeRelative := filepath.FromSlash(relative)
	if relative == "" || filepath.IsAbs(nativeRelative) || hasWindowsDrivePrefix(relative) {
		return "", ErrInvalidPath
	}
	candidate, err := filepath.Abs(filepath.Join(base, nativeRelative))
	if err != nil {
		return "", ErrInvalidPath
	}
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(candidate))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || filepath.ToSlash(rel) != relative {
		return "", ErrInvalidPath
	}
	return candidate, nil
}

func artifactRelativePath(relative string) (string, error) {
	parts := strings.Split(relative, "/")
	if len(parts) != 3 {
		return "", ErrInvalidPath
	}
	canonical := make([]string, len(parts))
	for index, part := range parts {
		id, err := uuid.Parse(part)
		if err != nil || id == uuid.Nil || id.Version() != uuid.Version(7) {
			return "", ErrInvalidPath
		}
		canonical[index] = id.String()
	}
	return strings.Join(canonical, "/"), nil
}

func hasWindowsDrivePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':'
}

func cleanRequestedPath(value string) (string, []string, error) {
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		value = "index.html"
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || hasWindowsDrivePrefix(value) || filepath.IsAbs(filepath.FromSlash(value)) {
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
