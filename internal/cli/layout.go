package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type InstallLayout struct {
	Root        string
	EnvFile     string
	ComposeFile string
	ProxyFile   string
	VersionFile string
	StateDir    string
}

func newInstallLayout(root string) InstallLayout {
	return InstallLayout{
		Root:        root,
		EnvFile:     filepath.Join(root, "config.env"),
		ComposeFile: filepath.Join(root, "compose.production.yaml"),
		ProxyFile:   filepath.Join(root, "console", "deploy", "nginx.conf"),
		VersionFile: filepath.Join(root, "VERSION"),
		StateDir:    filepath.Join(root, "state"),
	}
}

func (a *App) layout() (InstallLayout, error) {
	root := os.Getenv("STEALTH_INSTALL_DIR")
	if root == "" {
		if a.homeDir == "" {
			return InstallLayout{}, fmt.Errorf("home directory is not available")
		}
		root = filepath.Join(a.homeDir, defaultHomeName)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return InstallLayout{}, fmt.Errorf("installation directory must be an absolute path: %w", err)
	}
	if root == string(filepath.Separator) || root == filepath.Clean(a.homeDir) {
		return InstallLayout{}, fmt.Errorf("refusing unsafe installation directory %s", root)
	}
	return newInstallLayout(root), nil
}

func installationExists(layout InstallLayout) bool {
	return regularFile(layout.EnvFile) ||
		regularFile(layout.ComposeFile) ||
		regularFile(layout.ProxyFile) ||
		regularFile(layout.VersionFile) ||
		directoryExists(layout.StateDir)
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileIsPrivate(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0
}

func readVersion(layout InstallLayout) string {
	version, _, err := readInstalledVersion(layout)
	if err != nil {
		return ""
	}
	return version
}

func readInstalledVersion(layout InstallLayout) (string, bool, error) {
	contents, err := os.ReadFile(layout.VersionFile)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	return strings.TrimSpace(string(contents)), true, nil
}
