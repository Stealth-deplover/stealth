// Package functionrunner contains the trusted worker boundary for user
// Functions. Nothing in this package runs an uploaded archive on the API
// process; source files are copied into an isolated runtime container. Its
// strict archive extractor is also reused by Sites for pre-built static
// publication, where extracted files are served but never executed by API.
package functionrunner

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	ErrUnsupportedArchive = errors.New("unsupported function source archive")
	ErrArchiveTraversal   = errors.New("function source archive contains an unsafe path")
	ErrArchiveEntry       = errors.New("function source archive contains an unsafe entry")
	ErrArchiveTooLarge    = errors.New("function source archive expands beyond worker limits")
)

const (
	DefaultMaxArchiveBytes = int64(256 << 20)
	DefaultMaxArchiveFiles = 4096
	DefaultMaxEntryBytes   = int64(128 << 20)
	DefaultMaxCompressed   = int64(64 << 20)
)

// ArchiveLimits protect the worker from zip/tar bombs and pathological file
// counts. The compressed upload limit is enforced by functionstore; these
// limits apply to the expanded workspace.
type ArchiveLimits struct {
	MaxBytes      int64
	MaxFiles      int
	MaxEntry      int64
	MaxCompressed int64
	// StripTopLevel is used for provider-generated Git archives, which wrap
	// the repository in one synthetic directory. Ordinary user uploads keep
	// their archive paths unchanged.
	StripTopLevel bool
}

func (l ArchiveLimits) withDefaults() ArchiveLimits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxArchiveBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultMaxArchiveFiles
	}
	if l.MaxEntry <= 0 {
		l.MaxEntry = DefaultMaxEntryBytes
	}
	if l.MaxCompressed <= 0 {
		l.MaxCompressed = DefaultMaxCompressed
	}
	return l
}

type ArchiveStats struct {
	Files       int
	Directories int
	Bytes       int64
}

// Extract accepts zip, tar, tar.gz and tgz source names. Archive entries are
// always created below destination; absolute paths, dot segments, symlinks,
// hard links and special files are rejected before any user file is written.
func Extract(ctx context.Context, source io.Reader, sourceName, destination string, limits ArchiveLimits) (ArchiveStats, error) {
	return extractArchive(ctx, source, sourceName, destination, limits, false)
}

// ExtractTrusted reads a tar artifact produced by the isolated builder. It
// permits only lexically in-workspace relative symlinks (package managers
// commonly create these), while still rejecting absolute targets, dot-segment
// escapes, hard links, and special files. Untrusted uploads must use Extract.
func ExtractTrusted(ctx context.Context, source io.Reader, sourceName, destination string, limits ArchiveLimits) (ArchiveStats, error) {
	return extractArchive(ctx, source, sourceName, destination, limits, true)
}

func extractArchive(ctx context.Context, source io.Reader, sourceName, destination string, limits ArchiveLimits, allowSymlinks bool) (ArchiveStats, error) {
	limits = limits.withDefaults()
	destination, root, err := prepareArchiveRoot(destination)
	if err != nil {
		return ArchiveStats{}, err
	}
	defer root.Close()
	// zip.Reader requires random access. The function upload ceiling is small
	// relative to worker memory, and this bounded read avoids an unbounded
	// allocation when a caller invokes Extract directly.
	readLimit := limits.MaxCompressed
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	compressed, err := io.ReadAll(io.LimitReader(source, readLimit))
	if err != nil {
		return ArchiveStats{}, err
	}
	if int64(len(compressed)) > limits.MaxCompressed {
		return ArchiveStats{}, ErrArchiveTooLarge
	}
	name := strings.ToLower(strings.TrimSpace(sourceName))
	switch {
	case strings.HasSuffix(name, ".zip"):
		return extractZip(ctx, compressed, destination, root, limits, allowSymlinks)
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		reader, closeFn, err := gzipReader(bytes.NewReader(compressed))
		if err != nil {
			return ArchiveStats{}, err
		}
		defer closeFn()
		return extractTar(ctx, reader, destination, root, limits, allowSymlinks)
	case strings.HasSuffix(name, ".tar"):
		return extractTar(ctx, bytes.NewReader(compressed), destination, root, limits, allowSymlinks)
	default:
		return ArchiveStats{}, ErrUnsupportedArchive
	}
}

func prepareArchiveRoot(destination string) (string, *os.Root, error) {
	destination, err := filepath.Abs(destination)
	if err != nil || strings.TrimSpace(destination) == "" {
		return "", nil, ErrArchiveTraversal
	}
	destination = filepath.Clean(destination)
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(destination, 0o700); err != nil {
			return "", nil, fmt.Errorf("create function workspace: %w", err)
		}
		info, err = os.Lstat(destination)
	}
	if err != nil {
		return "", nil, fmt.Errorf("inspect function workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, ErrArchiveTraversal
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return "", nil, fmt.Errorf("open function workspace: %w", err)
	}
	return destination, root, nil
}

func gzipReader(source io.Reader) (io.Reader, func() error, error) {
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, nil, fmt.Errorf("open gzip source archive: %w", err)
	}
	return reader, reader.Close, nil
}

func extractZip(ctx context.Context, compressed []byte, destination string, root *os.Root, limits ArchiveLimits, allowSymlinks bool) (ArchiveStats, error) {
	archive, err := zip.NewReader(bytes.NewReader(compressed), int64(len(compressed)))
	if err != nil {
		return ArchiveStats{}, fmt.Errorf("open zip source archive: %w", err)
	}
	stats := ArchiveStats{}
	seen := make(map[string]struct{}, len(archive.File))
	rootPrefix := ""
	rootSeen := false
	for _, entry := range archive.File {
		if err := contextErr(ctx); err != nil {
			return ArchiveStats{}, err
		}
		relative, isDirectory, err := safeArchiveEntryPath(entry.Name)
		if err != nil {
			return ArchiveStats{}, err
		}
		isDirectory = isDirectory || entry.Mode().IsDir()
		if limits.StripTopLevel {
			relative, isDirectory, err = stripTopLevelEntry(relative, isDirectory, &rootPrefix, &rootSeen)
			if err != nil {
				return ArchiveStats{}, err
			}
			if relative == "" && isDirectory {
				continue
			}
		}
		if _, exists := seen[relative]; exists {
			return ArchiveStats{}, fmt.Errorf("%w: duplicate path %q", ErrArchiveEntry, entry.Name)
		}
		seen[relative] = struct{}{}
		if entry.Mode()&os.ModeSymlink != 0 {
			if !allowSymlinks {
				return ArchiveStats{}, fmt.Errorf("%w: links and special files are not allowed", ErrArchiveEntry)
			}
			if isDirectory || stats.Files >= limits.MaxFiles {
				return ArchiveStats{}, ErrArchiveTooLarge
			}
			reader, err := entry.Open()
			if err != nil {
				return ArchiveStats{}, fmt.Errorf("open zip symlink: %w", err)
			}
			target, readErr := io.ReadAll(io.LimitReader(reader, maxSymlinkTargetBytes+1))
			closeErr := reader.Close()
			if readErr != nil {
				return ArchiveStats{}, readErr
			}
			if closeErr != nil {
				return ArchiveStats{}, closeErr
			}
			if int64(len(target)) > maxSymlinkTargetBytes {
				return ArchiveStats{}, ErrArchiveTooLarge
			}
			if err := writeSymlink(root, destination, relative, string(target)); err != nil {
				return ArchiveStats{}, err
			}
			stats.Files++
			continue
		}
		if entry.Mode()&os.ModeType != 0 && !entry.Mode().IsDir() {
			return ArchiveStats{}, fmt.Errorf("%w: links and special files are not allowed", ErrArchiveEntry)
		}
		if isDirectory {
			if err := makeDirectory(root, destination, relative); err != nil {
				return ArchiveStats{}, err
			}
			stats.Directories++
			continue
		}
		if stats.Files >= limits.MaxFiles {
			return ArchiveStats{}, ErrArchiveTooLarge
		}
		size := int64(entry.UncompressedSize64)
		if size < 0 || size > limits.MaxEntry || size > limits.MaxBytes-stats.Bytes {
			return ArchiveStats{}, ErrArchiveTooLarge
		}
		reader, err := entry.Open()
		if err != nil {
			return ArchiveStats{}, fmt.Errorf("open zip entry: %w", err)
		}
		written, writeErr := writeEntry(ctx, root, reader, destination, relative, limits.MaxEntry, limits.MaxBytes-stats.Bytes)
		closeErr := reader.Close()
		if writeErr != nil {
			return ArchiveStats{}, writeErr
		}
		if closeErr != nil {
			return ArchiveStats{}, closeErr
		}
		stats.Files++
		stats.Bytes += written
	}
	return stats, nil
}

func extractTar(ctx context.Context, source io.Reader, destination string, root *os.Root, limits ArchiveLimits, allowSymlinks bool) (ArchiveStats, error) {
	reader := tar.NewReader(source)
	stats := ArchiveStats{}
	seen := map[string]struct{}{}
	rootPrefix := ""
	rootSeen := false
	for {
		if err := contextErr(ctx); err != nil {
			return ArchiveStats{}, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ArchiveStats{}, fmt.Errorf("read tar source archive: %w", err)
		}
		if header.Typeflag == tar.TypeXHeader || header.Typeflag == tar.TypeXGlobalHeader || header.Typeflag == tar.TypeGNULongName || header.Typeflag == tar.TypeGNULongLink {
			// archive/tar consumes PAX/GNU metadata while iterating. These
			// records do not represent a filesystem object.
			continue
		}
		var relative string
		var isDirectory bool
		if allowSymlinks {
			relative, isDirectory, err = trustedEntryPath(header.Name)
		} else {
			relative, isDirectory, err = safeArchiveEntryPath(header.Name)
		}
		if err != nil {
			return ArchiveStats{}, err
		}
		isDirectory = isDirectory || header.Typeflag == tar.TypeDir
		if limits.StripTopLevel {
			relative, isDirectory, err = stripTopLevelEntry(relative, isDirectory, &rootPrefix, &rootSeen)
			if err != nil {
				return ArchiveStats{}, err
			}
			if relative == "" && isDirectory {
				continue
			}
		}
		if relative == "" && isDirectory {
			continue
		}
		if _, exists := seen[relative]; exists {
			return ArchiveStats{}, fmt.Errorf("%w: duplicate path %q", ErrArchiveEntry, header.Name)
		}
		seen[relative] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := makeDirectory(root, destination, relative); err != nil {
				return ArchiveStats{}, err
			}
			stats.Directories++
		case tar.TypeReg, tar.TypeRegA:
			if stats.Files >= limits.MaxFiles || header.Size < 0 || header.Size > limits.MaxEntry || header.Size > limits.MaxBytes-stats.Bytes {
				return ArchiveStats{}, ErrArchiveTooLarge
			}
			written, writeErr := writeEntry(ctx, root, reader, destination, relative, limits.MaxEntry, limits.MaxBytes-stats.Bytes)
			if writeErr != nil {
				return ArchiveStats{}, writeErr
			}
			if written != header.Size {
				return ArchiveStats{}, fmt.Errorf("%w: tar entry size changed", ErrArchiveEntry)
			}
			stats.Files++
			stats.Bytes += written
		case tar.TypeSymlink:
			if !allowSymlinks || stats.Files >= limits.MaxFiles {
				return ArchiveStats{}, fmt.Errorf("%w: links and special files are not allowed", ErrArchiveEntry)
			}
			if err := writeSymlink(root, destination, relative, header.Linkname); err != nil {
				return ArchiveStats{}, err
			}
			stats.Files++
		default:
			return ArchiveStats{}, fmt.Errorf("%w: tar links and special files are not allowed", ErrArchiveEntry)
		}
	}
	return stats, nil
}

func stripTopLevelEntry(relative string, isDirectory bool, rootPrefix *string, rootSeen *bool) (string, bool, error) {
	if relative == "" {
		return "", isDirectory, fmt.Errorf("%w: empty Git archive path", ErrArchiveEntry)
	}
	if !*rootSeen {
		if !isDirectory {
			return "", false, fmt.Errorf("%w: Git archive must contain one repository root directory", ErrArchiveEntry)
		}
		parts := strings.Split(relative, "/")
		if len(parts) == 0 || parts[0] == "" {
			return "", false, fmt.Errorf("%w: Git archive root directory is invalid", ErrArchiveEntry)
		}
		*rootPrefix = parts[0]
		*rootSeen = true
	}
	if relative == *rootPrefix {
		return "", true, nil
	}
	prefix := *rootPrefix + "/"
	if !strings.HasPrefix(relative, prefix) {
		return "", false, fmt.Errorf("%w: Git archive contains multiple top-level directories", ErrArchiveEntry)
	}
	stripped := strings.TrimPrefix(relative, prefix)
	if stripped == "" {
		return "", false, fmt.Errorf("%w: Git archive path is empty", ErrArchiveEntry)
	}
	return stripped, isDirectory, nil
}

const maxSymlinkTargetBytes = int64(4096)

func trustedEntryPath(name string) (string, bool, error) {
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	if name == "." || name == "" {
		return "", true, nil
	}
	return safeArchiveEntryPath(name)
}

// safeArchiveEntryPath returns a canonical, relative archive path. It is the
// only entry-name sanitizer used before an archive path reaches the
// filesystem boundary. The lexical checks are intentionally stricter than
// filepath.Clean so dot segments and platform-specific absolute paths cannot
// be normalized into an escape.
func safeArchiveEntryPath(name string) (string, bool, error) {
	// Archivers commonly include an explicit root directory (`.` or `./`)
	// when packaging the current working directory. It does not address a
	// user-controlled filesystem object, so accept that single root marker;
	// dot segments anywhere else remain rejected below.
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	if name == "." || name == "" {
		return "", true, nil
	}
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || hasWindowsDrivePrefix(name) {
		return "", false, fmt.Errorf("%w: %q", ErrArchiveTraversal, name)
	}
	isDirectory := strings.HasSuffix(name, "/")
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" {
		return "", true, fmt.Errorf("%w: empty archive path", ErrArchiveEntry)
	}
	native := filepath.FromSlash(trimmed)
	if filepath.IsAbs(native) || filepath.VolumeName(native) != "" {
		return "", false, fmt.Errorf("%w: %q", ErrArchiveTraversal, name)
	}
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false, fmt.Errorf("%w: %q", ErrArchiveTraversal, name)
		}
	}
	clean := path.Join(parts...)
	if clean != trimmed || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("%w: %q", ErrArchiveTraversal, name)
	}
	return clean, isDirectory, nil
}

func hasWindowsDrivePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':'
}

func makeDirectory(root *os.Root, destination, relative string) error {
	if _, err := safeDestination(destination, relative); err != nil {
		return err
	}
	if err := ensureParentsAreDirectories(root, relative); err != nil {
		return err
	}
	native := filepath.FromSlash(relative)
	info, err := root.Lstat(native)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(native, 0o700); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create archive directory: %w", err)
			}
			info, err = root.Lstat(native)
		} else {
			return nil
		}
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: archive directory is not a directory", ErrArchiveEntry)
	}
	return nil
}

func writeEntry(ctx context.Context, root *os.Root, source io.Reader, destination, relative string, maxEntry, maxRemaining int64) (int64, error) {
	if _, err := safeDestination(destination, relative); err != nil {
		return 0, err
	}
	if err := ensureParentsAreDirectories(root, relative); err != nil {
		return 0, err
	}
	nativeRelative := filepath.FromSlash(relative)
	if existing, err := root.Lstat(nativeRelative); err == nil {
		if existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("%w: destination is not a regular file", ErrArchiveEntry)
		}
		return 0, fmt.Errorf("%w: duplicate destination", ErrArchiveEntry)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	parent := filepath.ToSlash(filepath.Dir(relative))
	if parent == "." {
		parent = ""
	}
	temporaryRelative, temporary, err := createArchiveTemporaryFile(root, parent)
	if err != nil {
		return 0, err
	}
	cleanup := func() {
		_ = temporary.Close()
		_ = root.Remove(filepath.FromSlash(temporaryRelative))
	}
	limit := maxEntry
	if maxRemaining < limit {
		limit = maxRemaining
	}
	written, err := io.Copy(temporary, io.LimitReader(source, limit+1))
	if err != nil {
		cleanup()
		return 0, err
	}
	if written > limit {
		cleanup()
		return 0, ErrArchiveTooLarge
	}
	if err := contextErr(ctx); err != nil {
		cleanup()
		return 0, err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return 0, err
	}
	if err := temporary.Close(); err != nil {
		_ = root.Remove(filepath.FromSlash(temporaryRelative))
		return 0, err
	}
	if err := root.Rename(filepath.FromSlash(temporaryRelative), nativeRelative); err != nil {
		_ = root.Remove(filepath.FromSlash(temporaryRelative))
		return 0, err
	}
	return written, nil
}

func createArchiveTemporaryFile(root *os.Root, parent string) (string, *os.File, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, fmt.Errorf("generate archive temporary name: %w", err)
		}
		relative := path.Join(parent, ".extract-"+hex.EncodeToString(random[:]))
		file, err := root.OpenFile(filepath.FromSlash(relative), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return relative, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, fmt.Errorf("create archive file: %w", err)
		}
	}
	return "", nil, fmt.Errorf("create archive file: temporary name collision")
}

func writeSymlink(root *os.Root, destination, relative, target string) error {
	if relative == "" || target == "" || strings.ContainsAny(target, "\\\x00\r\n") || strings.HasPrefix(target, "/") || hasWindowsDrivePrefix(target) || filepath.IsAbs(filepath.FromSlash(target)) || filepath.VolumeName(filepath.FromSlash(target)) != "" {
		return fmt.Errorf("%w: unsafe symlink target", ErrArchiveEntry)
	}
	if _, err := safeDestination(destination, relative); err != nil {
		return err
	}
	linkDirectory := path.Dir(relative)
	combined := path.Join(linkDirectory, target)
	if linkDirectory == "." {
		combined = path.Clean(target)
	}
	if _, _, err := safeArchiveEntryPath(combined); err != nil {
		return fmt.Errorf("%w: unsafe symlink target", ErrArchiveEntry)
	}
	if err := ensureParentsAreDirectories(root, relative); err != nil {
		return err
	}
	nativeRelative := filepath.FromSlash(relative)
	if _, err := root.Lstat(nativeRelative); err == nil {
		return fmt.Errorf("%w: duplicate destination", ErrArchiveEntry)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := root.Symlink(target, nativeRelative); err != nil {
		return fmt.Errorf("create archive symlink: %w", err)
	}
	return nil
}

func safeDestination(destination, relative string) (string, error) {
	base, err := filepath.Abs(destination)
	if err != nil {
		return "", ErrArchiveTraversal
	}
	target := filepath.Join(base, filepath.FromSlash(relative))
	resolved, err := filepath.Abs(target)
	if err != nil {
		return "", ErrArchiveTraversal
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrArchiveTraversal
	}
	return resolved, nil
}

func ensureParentsAreDirectories(root *os.Root, relative string) error {
	if root == nil {
		return ErrArchiveTraversal
	}
	parts := strings.Split(relative, "/")
	if len(parts) < 2 {
		return nil
	}
	current := ""
	for _, part := range parts[:len(parts)-1] {
		current = path.Join(current, part)
		native := filepath.FromSlash(current)
		info, err := root.Lstat(native)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if err := root.Mkdir(native, 0o700); err != nil {
					if !errors.Is(err, os.ErrExist) {
						return err
					}
					info, err = root.Lstat(native)
					if err != nil {
						return err
					}
				} else {
					continue
				}
			} else {
				return err
			}
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: archive parent is not a directory", ErrArchiveEntry)
		}
	}
	return nil
}

func contextErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
