package appruntime

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/repository"
)

const (
	runtimeEnvironmentDirectory = "/dev/shm"
	runtimeEnvironmentFileLimit = repository.AppEnvironmentVariableMaxCount * (120 + repository.AppEnvironmentVariableMaxValueBytes + 2)
)

// writeRuntimeEnvironmentFile creates a short-lived Docker --env-file in the
// worker container's memory-backed shared-memory directory. The caller must
// invoke the returned cleanup function after Docker has consumed the file.
func writeRuntimeEnvironmentFile(values []RuntimeEnvironmentVariable) (string, func() error, error) {
	if len(values) == 0 || len(values) > repository.AppEnvironmentVariableMaxCount {
		return "", nil, ErrContainerCreate
	}
	ordered := append([]RuntimeEnvironmentVariable(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })
	seen := make(map[string]struct{}, len(ordered))
	total := 0
	for _, item := range ordered {
		if !appRuntimeEnvironmentKey.MatchString(item.Key) || len(item.Value) > repository.AppEnvironmentVariableMaxValueBytes || bytes.IndexAny(item.Value, "\x00\r\n") >= 0 {
			return "", nil, ErrContainerCreate
		}
		if _, duplicate := seen[item.Key]; duplicate {
			return "", nil, ErrContainerCreate
		}
		seen[item.Key] = struct{}{}
		total += len(item.Key) + len(item.Value) + 2
		if total > runtimeEnvironmentFileLimit {
			return "", nil, ErrContainerCreate
		}
	}
	if info, err := os.Stat(runtimeEnvironmentDirectory); err != nil || !info.IsDir() {
		return "", nil, ErrContainerCreate
	}
	file, err := os.CreateTemp(runtimeEnvironmentDirectory, ".stealth-app-env-*")
	if err != nil {
		return "", nil, ErrContainerCreate
	}
	path := file.Name()
	cleanup := func() error { return wipeAndRemoveRuntimeEnvironmentFile(path) }
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		_ = cleanup()
		return "", nil, ErrContainerCreate
	}
	contents := make([]byte, 0, total)
	for _, item := range ordered {
		contents = append(contents, item.Key...)
		contents = append(contents, '=')
		contents = append(contents, item.Value...)
		contents = append(contents, '\n')
	}
	_, writeErr := file.Write(contents)
	clear(contents)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = cleanup()
		return "", nil, ErrContainerCreate
	}
	return path, cleanup, nil
}

func validRuntimeEnvironmentFilePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && strings.HasPrefix(path, runtimeEnvironmentDirectory+"/.stealth-app-env-")
}

func wipeAndRemoveRuntimeEnvironmentFile(path string) error {
	if !validRuntimeEnvironmentFilePath(path) {
		return ErrContainerCreate
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrContainerCreate
	}
	if err == nil {
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return ErrContainerCreate
		}
		zeroes := make([]byte, 32*1024)
		remaining := info.Size()
		for remaining > 0 {
			chunk := int64(len(zeroes))
			if remaining < chunk {
				chunk = remaining
			}
			written, writeErr := file.Write(zeroes[:chunk])
			if writeErr != nil || written == 0 {
				clear(zeroes)
				_ = file.Close()
				return ErrContainerCreate
			}
			remaining -= int64(written)
		}
		clear(zeroes)
		if err := file.Truncate(0); err != nil {
			_ = file.Close()
			return ErrContainerCreate
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return ErrContainerCreate
		}
		if err := file.Close(); err != nil {
			return ErrContainerCreate
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove temporary App environment file: %w", ErrContainerCreate)
	}
	return nil
}
