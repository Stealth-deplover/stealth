package appbuilder

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
)

const maxDockerfileBytes = 1 << 20

var ErrUnsafeDockerfileFrontend = errors.New("Dockerfile syntax frontend overrides are not allowed")

var syntaxDirective = regexp.MustCompile(`(?i)^\s*#\s*syntax\s*=`)

// ValidateBuildContext uses os.Root lookups for every path component, rejects
// symlink traversal, and confirms that the Dockerfile is a regular file in the
// selected context before BuildKit receives the directory.
func ValidateBuildContext(workspace string, spec appbuildspec.Spec) (string, error) {
	normalized, err := appbuildspec.Normalize(spec)
	if err != nil {
		return "", err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return "", appbuildspec.ErrInvalidBuildSpec
	}
	workspace = filepath.Clean(workspace)
	info, err := os.Lstat(workspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", appbuildspec.ErrInvalidBuildSpec
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return "", appbuildspec.ErrInvalidBuildSpec
	}
	defer root.Close()
	if err := checkPathComponents(root, normalized.ContextDirectory, true); err != nil {
		return "", err
	}
	dockerfile := filepath.FromSlash(normalized.DockerfilePath)
	if err := checkPathComponents(root, dockerfile, false); err != nil {
		return "", err
	}
	file, err := root.Open(dockerfile)
	if err != nil {
		return "", appbuildspec.ErrInvalidBuildSpec
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDockerfileBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxDockerfileBytes {
		return "", appbuildspec.ErrInvalidBuildSpec
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), maxDockerfileBytes)
	for scanner.Scan() {
		if syntaxDirective.MatchString(scanner.Text()) {
			return "", ErrUnsafeDockerfileFrontend
		}
	}
	if scanner.Err() != nil {
		return "", appbuildspec.ErrInvalidBuildSpec
	}
	return filepath.Join(workspace, filepath.FromSlash(normalized.ContextDirectory)), nil
}

func checkPathComponents(root *os.Root, relative string, finalMustBeDirectory bool) error {
	if relative == "." {
		info, err := root.Stat(".")
		if err != nil || !info.IsDir() {
			return appbuildspec.ErrInvalidBuildSpec
		}
		return nil
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	current := ""
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return appbuildspec.ErrInvalidBuildSpec
		}
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return appbuildspec.ErrInvalidBuildSpec
		}
		isFinal := index == len(parts)-1
		if !isFinal && !info.IsDir() {
			return appbuildspec.ErrInvalidBuildSpec
		}
		if isFinal {
			if finalMustBeDirectory && !info.IsDir() || !finalMustBeDirectory && !info.Mode().IsRegular() {
				return appbuildspec.ErrInvalidBuildSpec
			}
		}
	}
	return nil
}
