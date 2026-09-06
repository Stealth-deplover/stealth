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
	destination, err := filepath.Abs(destination)
	if err != nil || strings.TrimSpace(destination) == "" {
		return ArchiveStats{}, ErrArchiveTraversal
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return ArchiveStats{}, fmt.Errorf("create function workspace: %w", err)
	}
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
		return extractZip(ctx, compressed, destination, limits, allowSymlinks)
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		reader, closeFn, err := gzipReader(bytes.NewReader(compressed))
		if err != nil {
			return ArchiveStats{}, err
		}
		defer closeFn()
		return extractTar(ctx, reader, destination, limits, allowSymlinks)
	case strings.HasSuffix(name, ".tar"):
		return extractTar(ctx, bytes.NewReader(compressed), destination, limits, allowSymlinks)
	default:
		return ArchiveStats{}, ErrUnsupportedArchive
	}
}

func gzipReader(source io.Reader) (io.Reader, func() error, error) {
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, nil, fmt.Errorf("open gzip source archive: %w", err)
	}
	return reader, reader.Close, nil
}

func extractZip(ctx context.Context, compressed []byte, destination string, limits ArchiveLimits, allowSymlinks bool) (ArchiveStats, error) {
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
		relative, isDirectory, err := safeEntryPath(entry.Name)
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
			if err := writeSymlink(destination, relative, string(target)); err != nil {
				return ArchiveStats{}, err
			}
			stats.Files++
			continue
		}
		if entry.Mode()&os.ModeType != 0 && !entry.Mode().IsDir() {
			return ArchiveStats{}, fmt.Errorf("%w: links and special files are not allowed", ErrArchiveEntry)
		}
		if isDirectory {
			if err := makeDirectory(destination, relative); err != nil {
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
		written, writeErr := writeEntry(ctx, reader, destination, relative, limits.MaxEntry, limits.MaxBytes-stats.Bytes)
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

func extractTar(ctx context.Context, source io.Reader, destination string, limits ArchiveLimits, allowSymlinks bool) (ArchiveStats, error) {
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
			relative, isDirectory, err = safeEntryPath(header.Name)
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
			if err := makeDirectory(destination, relative); err != nil {
				return ArchiveStats{}, err
			}
			stats.Directories++
		case tar.TypeReg, tar.TypeRegA:
			if stats.Files >= limits.MaxFiles || header.Size < 0 || header.Size > limits.MaxEntry || header.Size > limits.MaxBytes-stats.Bytes {
				return ArchiveStats{}, ErrArchiveTooLarge
			}
			written, writeErr := writeEntry(ctx, reader, destination, relative, limits.MaxEntry, limits.MaxBytes-stats.Bytes)
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
			if err := writeSymlink(destination, relative, header.Linkname); err != nil {
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
	return safeEntryPath(name)
}

func safeEntryPath(name string) (string, bool, error) {
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
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", false, fmt.Errorf("%w: %q", ErrArchiveTraversal, name)
	}
	isDirectory := strings.HasSuffix(name, "/")
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" {
		return "", true, fmt.Errorf("%w: empty archive path", ErrArchiveEntry)
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

func makeDirectory(destination, relative string) error {
	target, err := safeDestination(destination, relative)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}
	return ensureParentsAreDirectories(destination, relative)
}

func writeEntry(ctx context.Context, source io.Reader, destination, relative string, maxEntry, maxRemaining int64) (int64, error) {
	target, err := safeDestination(destination, relative)
	if err != nil {
		return 0, err
	}
	if err := ensureParentsAreDirectories(destination, relative); err != nil {
		return 0, err
	}
	if existing, err := os.Lstat(target); err == nil {
		if existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("%w: destination is not a regular file", ErrArchiveEntry)
		}
		return 0, fmt.Errorf("%w: duplicate destination", ErrArchiveEntry)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".extract-*")
	if err != nil {
		return 0, fmt.Errorf("create archive file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return 0, err
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
		_ = os.Remove(temporaryPath)
		return 0, err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		_ = os.Remove(temporaryPath)
		return 0, err
	}
	return written, nil
}

func writeSymlink(destination, relative, target string) error {
	if relative == "" || target == "" || strings.ContainsAny(target, "\\\x00\r\n") || strings.HasPrefix(target, "/") {
		return fmt.Errorf("%w: unsafe symlink target", ErrArchiveEntry)
	}
	linkPath, err := safeDestination(destination, relative)
	if err != nil {
		return err
	}
	linkDirectory := path.Dir(relative)
	combined := path.Join(linkDirectory, target)
	if linkDirectory == "." {
		combined = path.Clean(target)
	}
	if _, _, err := safeEntryPath(combined); err != nil {
		return fmt.Errorf("%w: unsafe symlink target", ErrArchiveEntry)
	}
	if err := ensureParentsAreDirectories(destination, relative); err != nil {
		return err
	}
	if _, err := os.Lstat(linkPath); err == nil {
		return fmt.Errorf("%w: duplicate destination", ErrArchiveEntry)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(target, linkPath); err != nil {
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
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrArchiveTraversal
	}
	return resolved, nil
}

func ensureParentsAreDirectories(destination, relative string) error {
	parts := strings.Split(relative, "/")
	if len(parts) < 2 {
		return nil
	}
	current := destination
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if err := os.Mkdir(current, 0o700); err != nil {
					return err
				}
				continue
			}
			return err
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
