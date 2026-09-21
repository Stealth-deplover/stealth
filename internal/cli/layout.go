package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/installengine"
)

type InstallLayout = installengine.Layout

func newInstallLayout(root string) InstallLayout {
	layout, err := installengine.NewLayout(root)
	if err == nil {
		return layout
	}
	return InstallLayout{
		Root:                root,
		EnvFile:             filepath.Join(root, "config.env"),
		ComposeFile:         filepath.Join(root, "compose.production.yaml"),
		SetupComposeFile:    filepath.Join(root, "compose.setup.yaml"),
		TelemetryDir:        filepath.Join(root, "telemetry"),
		ProxyFile:           filepath.Join(root, "console", "deploy", "nginx.conf"),
		TraefikDir:          filepath.Join(root, "traefik"),
		TraefikStatic:       filepath.Join(root, "traefik", "traefik.yaml"),
		TraefikDynamic:      filepath.Join(root, "traefik", "dynamic"),
		TraefikCore:         filepath.Join(root, "traefik", "dynamic", "core.yaml"),
		TraefikGenerated:    filepath.Join(root, "traefik", "dynamic", "generated"),
		TraefikReloadMarker: filepath.Join(root, "traefik", "dynamic", ".reload.yaml"),
		VersionFile:         filepath.Join(root, "VERSION"),
		StateDir:            filepath.Join(root, "state"),
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
		regularFile(layout.TraefikStatic) ||
		regularFile(layout.TraefikCore) ||
		regularFile(layout.VersionFile) ||
		directoryExists(layout.StateDir)
}

func partialInstallationExists(layout InstallLayout) bool {
	if regularFile(layout.ComposeFile) || regularFile(layout.ProxyFile) || regularFile(layout.TraefikStatic) || regularFile(layout.TraefikCore) {
		return true
	}
	info, err := os.Stat(layout.StateDir)
	return err == nil && info.IsDir()
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
