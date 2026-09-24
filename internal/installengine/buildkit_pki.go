package installengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Stealth-deplover/stealth/internal/buildkitpki"
)

// ensureBuildKitPKI moves the pre-release PKI bundle out of setup StateDir
// before any Compose service can start. The directory rename preserves the
// complete CA/leaf identity atomically; cross-device copying is deliberately
// unsupported so a partial bundle can never become authoritative.
func ensureBuildKitPKI(layout Layout) (bool, error) {
	if strings.TrimSpace(layout.Root) == "" || filepath.Clean(layout.PrivateDir) != filepath.Join(filepath.Clean(layout.Root), "private") ||
		filepath.Clean(layout.BuildKitPKIDir) != filepath.Join(filepath.Clean(layout.PrivateDir), buildkitpki.DirectoryName) ||
		filepath.Clean(layout.StateDir) != filepath.Join(filepath.Clean(layout.Root), "state") {
		return false, errors.New("installation layout has invalid BuildKit PKI paths")
	}

	legacyPath := filepath.Join(layout.StateDir, buildkitpki.DirectoryName)
	legacyInfo, legacyErr := os.Lstat(legacyPath)
	_, newErr := os.Lstat(layout.BuildKitPKIDir)
	legacyExists := legacyErr == nil
	newExists := newErr == nil
	if legacyErr != nil && !errors.Is(legacyErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect legacy BuildKit PKI: %w", legacyErr)
	}
	if newErr != nil && !errors.Is(newErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect private BuildKit PKI: %w", newErr)
	}
	if legacyExists && newExists {
		return false, errors.New("legacy and private BuildKit PKI bundles both exist; refusing to choose or rotate a trust identity")
	}
	if legacyExists {
		if legacyInfo.Mode()&os.ModeSymlink != 0 || !legacyInfo.IsDir() {
			return false, fmt.Errorf("%w: legacy BuildKit PKI path is not a real directory", buildkitpki.ErrInvalidState)
		}
		for _, transactionPath := range []string{legacyPath + ".pending", legacyPath + ".previous", layout.BuildKitPKIDir + ".pending", layout.BuildKitPKIDir + ".previous"} {
			if _, err := os.Lstat(transactionPath); err == nil {
				return false, fmt.Errorf("%w: cannot relocate BuildKit PKI while an interrupted identity transaction exists", buildkitpki.ErrInvalidState)
			} else if !errors.Is(err, os.ErrNotExist) {
				return false, fmt.Errorf("inspect BuildKit PKI transaction path: %w", err)
			}
		}
		if err := buildkitpki.ValidateForRelocation(legacyPath); err != nil {
			return false, fmt.Errorf("legacy BuildKit PKI is incomplete or corrupt; refusing to issue a replacement CA: %w", err)
		}
		if err := ensurePrivateDirectory(layout.PrivateDir); err != nil {
			return false, fmt.Errorf("prepare private installation directory: %w", err)
		}
		if err := os.Rename(legacyPath, layout.BuildKitPKIDir); err != nil {
			if errors.Is(err, syscall.EXDEV) {
				return false, fmt.Errorf("legacy BuildKit PKI is on a different filesystem; refusing a non-atomic key copy: %w", err)
			}
			return false, fmt.Errorf("atomically relocate legacy BuildKit PKI: %w", err)
		}
		if err := syncDirectory(layout.StateDir); err != nil {
			return false, fmt.Errorf("sync legacy BuildKit PKI removal: %w", err)
		}
		if err := syncDirectory(layout.PrivateDir); err != nil {
			return false, fmt.Errorf("sync relocated BuildKit PKI: %w", err)
		}
		if err := buildkitpki.ValidateForRelocation(layout.BuildKitPKIDir); err != nil {
			return false, fmt.Errorf("validate relocated BuildKit PKI: %w", err)
		}
		if _, err := buildkitpki.Ensure(layout.BuildKitPKIDir); err != nil {
			return false, fmt.Errorf("renew relocated BuildKit PKI if required: %w", err)
		}
		return true, nil
	}
	return buildkitpki.Ensure(layout.BuildKitPKIDir)
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("private installation path is not a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
