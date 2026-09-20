package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/buildinfo"
	"github.com/charmbracelet/lipgloss"
)

const (
	maxReleaseMetadataSize = 1 << 20
	maxUpdateArchiveSize   = 64 << 20
	maxUpdateBinarySize    = 32 << 20
)

type githubRelease struct {
	TagName    string               `json:"tag_name"`
	Draft      bool                 `json:"draft"`
	Prerelease bool                 `json:"prerelease"`
	Assets     []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type semanticVersion struct {
	major uint64
	minor uint64
	patch uint64
}

type platformMigratedUpdateError struct {
	targetVersion string
	err           error
}

func (e *platformMigratedUpdateError) Error() string { return e.err.Error() }
func (e *platformMigratedUpdateError) Unwrap() error { return e.err }

func (a *App) runUpdate(args []string) int {
	initTerminalStyles()

	fs := flag.NewFlagSet("stealth update", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	check := fs.Bool("check", false, "check for an available CLI update without downloading it")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.errOut, "update does not accept positional arguments")
		return 2
	}

	current := buildinfo.Version
	if a.currentVersion != nil {
		current = a.currentVersion()
	}
	currentLabel := displayCurrentVersion(current)
	a.printUpdateHeader()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	release, err := a.latestStableRelease(ctx)
	if err != nil {
		if *check {
			fmt.Fprintf(a.errOut, "stealth update check failed: %v\n", err)
		} else {
			a.printUpdateFailure(err)
		}
		return 1
	}

	latest := release.TagName
	fmt.Fprintf(a.out, "Current version   %s\nLatest version    %s\n\n", currentLabel, latest)

	comparison, comparable := compareReleaseVersions(current, latest)
	if comparable && comparison == 0 {
		if !*check {
			migrated, migrationErr := a.migrateInstalledRelease(ctx, latest)
			if migrationErr != nil {
				a.printUpdateFailure(migrationErr)
				return 1
			}
			if migrated {
				a.printUpdateStep("✓", "Managed production assets synchronized", successStyle)
			}
		}
		a.printUpdateSuccess("Stealth is already up to date")
		fmt.Fprintf(a.out, "%s\n", latest)
		return 0
	}
	if comparable && comparison > 0 {
		fmt.Fprintf(a.out, "Current version %s is newer than latest stable %s.\nNo update performed.\n", currentLabel, latest)
		return 0
	}

	if *check {
		fmt.Fprintln(a.out, "Update available.")
		return 1
	}

	asset, err := releaseAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		a.printUpdateFailure(err)
		return 1
	}
	a.printUpdateStep("⠹", "Downloading Stealth "+latest, cyanStyle)
	if err := a.performUpdateWithTargetMigration(ctx, release, asset); err != nil {
		a.printUpdateFailure(err)
		return 1
	}
	a.printUpdateStep("✓", "Downloaded Stealth "+latest, successStyle)
	a.printUpdateStep("✓", "SHA-256 checksum verified", successStyle)
	a.printUpdateStep("✓", "Target release migration completed", successStyle)
	a.printUpdateStep("✓", "CLI updated", successStyle)
	fmt.Fprintf(a.out, "\n%s → %s\n", currentLabel, latest)
	return 0
}

func displayCurrentVersion(version string) string {
	version = strings.TrimSpace(version)
	if _, err := parseSemanticVersion(version); err != nil {
		return "dev"
	}
	return version
}

func compareReleaseVersions(current, latest string) (int, bool) {
	currentVersion, currentErr := parseSemanticVersion(strings.TrimSpace(current))
	latestVersion, latestErr := parseSemanticVersion(strings.TrimSpace(latest))
	if currentErr != nil || latestErr != nil {
		return 0, false
	}
	switch {
	case currentVersion.major != latestVersion.major:
		return compareUint64(currentVersion.major, latestVersion.major), true
	case currentVersion.minor != latestVersion.minor:
		return compareUint64(currentVersion.minor, latestVersion.minor), true
	default:
		return compareUint64(currentVersion.patch, latestVersion.patch), true
	}
}

func compareUint64(left, right uint64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func parseSemanticVersion(version string) (semanticVersion, error) {
	if err := validateStableReleaseVersion(version); err != nil {
		return semanticVersion{}, err
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	values := [3]uint64{}
	for index, part := range parts {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semanticVersion{}, fmt.Errorf("invalid release version %q: %w", version, err)
		}
		values[index] = value
	}
	return semanticVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func (a *App) latestStableRelease(ctx context.Context) (githubRelease, error) {
	endpoint, err := appendReleasePath(a.releaseAPIBase, "releases", "latest")
	if err != nil {
		return githubRelease{}, fmt.Errorf("invalid GitHub API endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, fmt.Errorf("create GitHub release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", a.updateUserAgent())
	response, err := a.updateHTTPClient().Do(request)
	if err != nil {
		return githubRelease{}, fmt.Errorf("resolve latest stable release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return githubRelease{}, githubHTTPError("resolve latest stable release", response)
	}
	contents, err := readBounded(response.Body, maxReleaseMetadataSize)
	if err != nil {
		return githubRelease{}, fmt.Errorf("read latest release metadata: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(contents, &release); err != nil {
		return githubRelease{}, fmt.Errorf("parse latest release metadata: %w", err)
	}
	if release.Draft || release.Prerelease {
		return githubRelease{}, fmt.Errorf("latest GitHub release %q is not stable", release.TagName)
	}
	if strings.TrimSpace(release.TagName) != release.TagName {
		return githubRelease{}, fmt.Errorf("latest GitHub release has invalid tag %q", release.TagName)
	}
	if err := validateStableReleaseVersion(release.TagName); err != nil {
		return githubRelease{}, fmt.Errorf("latest GitHub release has invalid tag: %w", err)
	}
	return release, nil
}

func (a *App) performUpdate(ctx context.Context, release githubRelease, asset string) error {
	return a.performUpdateWithTargetMigration(ctx, release, asset)
}

// performUpdateWithTargetMigration executes a target-owned migration after
// validating the downloaded release binary and before replacing the installed
// executable. The currently running binary deliberately does not interpret the
// target manifest: future releases can evolve their managed assets safely.
func (a *App) performUpdateWithTargetMigration(ctx context.Context, release githubRelease, asset string) error {
	return a.performUpdateWithTargetMigrationHook(ctx, release, asset, func(binary string) error {
		if err := a.invokeTargetMigration(ctx, binary, release.TagName); err != nil {
			return fmt.Errorf("run target release migration: %w", err)
		}
		return nil
	})
}

func (a *App) performUpdateWithTargetMigrationHook(ctx context.Context, release githubRelease, asset string, beforeReplace func(string) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("update canceled: %w", err)
	}
	if !hasReleaseAsset(release, asset) {
		return fmt.Errorf("latest release %s does not contain %s", release.TagName, asset)
	}
	if !hasReleaseAsset(release, "checksums.txt") {
		return fmt.Errorf("latest release %s does not contain checksums.txt", release.TagName)
	}

	archiveURL, err := appendReleasePath(a.releaseDownloadBase, release.TagName, asset)
	if err != nil {
		return fmt.Errorf("invalid release download endpoint: %w", err)
	}
	checksumsURL, err := appendReleasePath(a.releaseDownloadBase, release.TagName, "checksums.txt")
	if err != nil {
		return fmt.Errorf("invalid checksum download endpoint: %w", err)
	}
	archive, err := a.fetchUpdateAsset(ctx, archiveURL, maxUpdateArchiveSize)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	checksums, err := a.fetchUpdateAsset(ctx, checksumsURL, 128<<10)
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	expected, err := checksumForAsset(string(checksums), asset)
	if err != nil {
		return err
	}
	if err := verifySHA256(archive, expected); err != nil {
		return err
	}

	temporaryDir, temporaryBinary, err := extractUpdateBinary(archive)
	if err != nil {
		return fmt.Errorf("extract release archive: %w", err)
	}
	defer os.RemoveAll(temporaryDir)
	if err := a.validateDownloadedBinary(ctx, temporaryBinary, release.TagName); err != nil {
		return err
	}
	target, err := a.currentExecutablePath()
	if err != nil {
		return err
	}
	if beforeReplace != nil {
		if err := beforeReplace(temporaryBinary); err != nil {
			return fmt.Errorf("migrate existing installation: %w", err)
		}
	}
	if err := a.replaceExecutable(ctx, temporaryBinary, target); err != nil {
		return &platformMigratedUpdateError{
			targetVersion: release.TagName,
			err:           fmt.Errorf("target platform migration completed, but replacing the CLI executable failed: %w; the installed stack is at %s and a later `stealth update` will reconcile the CLI", err, release.TagName),
		}
	}
	return nil
}

func (a *App) invokeTargetMigration(ctx context.Context, binaryPath, targetVersion string) error {
	if a.runTargetMigration != nil {
		return a.runTargetMigration(ctx, binaryPath, targetVersion)
	}
	runner := a.runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	return runner.Run(ctx, "", a.out, a.errOut, binaryPath, "internal", "migrate-installation", "--target-version", targetVersion)
}

// migrateInstalledRelease runs the trusted host-side install engine for an
// existing installation. A self-update is still possible on a host without an
// installation, but an installation that is present is never left on a
// pre-release topology merely because the CLI binary changed.
func (a *App) migrateInstalledRelease(ctx context.Context, targetVersion string) (bool, error) {
	layout, err := a.layout()
	if err != nil {
		return false, fmt.Errorf("inspect installation layout: %w", err)
	}
	if !installationExists(layout) {
		return false, nil
	}
	// A state directory alone can be left by an interrupted first install and
	// does not contain enough release metadata to select a target asset set.
	// Let the CLI self-update in that case; the explicit repair flow remains the
	// recovery owner once config.env exists.
	if !regularFile(layout.EnvFile) {
		return false, nil
	}
	plan, err := a.loadExistingPlan(layout)
	if err != nil {
		return false, fmt.Errorf("load existing installation for migration: %w", err)
	}
	installedVersion := plan.Version
	plan.Version = targetVersion
	plan.InstalledVersion = installedVersion
	plan.Existing = true
	if err := a.installEngine().Install(ctx, *plan, nil); err != nil {
		return false, err
	}
	return true, nil
}

// runInternal exposes one deliberately narrow host-only handoff used by a
// verified downloaded release during self-update. It accepts neither scripts
// nor URLs: the target binary uses its compiled-in managed-asset manifest and
// trusted release base, then takes the ordinary install.lock itself.
func (a *App) runInternal(args []string) int {
	if len(args) == 0 || args[0] != "migrate-installation" {
		fmt.Fprintln(a.errOut, "unknown internal command")
		return 2
	}
	fs := flag.NewFlagSet("stealth internal migrate-installation", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	targetVersion := fs.String("target-version", "", "target release version")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*targetVersion) == "" {
		fmt.Fprintln(a.errOut, "internal migration requires exactly --target-version vX.Y.Z")
		return 2
	}
	if err := validateStableReleaseVersion(*targetVersion); err != nil {
		fmt.Fprintf(a.errOut, "invalid internal migration target: %v\n", err)
		return 2
	}
	current := buildinfo.Version
	if a.currentVersion != nil {
		current = a.currentVersion()
	}
	if strings.TrimSpace(current) != strings.TrimSpace(*targetVersion) {
		fmt.Fprintln(a.errOut, "internal migration target does not match this Stealth binary")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	migrated, err := a.migrateInstalledRelease(ctx, *targetVersion)
	if err != nil {
		fmt.Fprintf(a.errOut, "internal installation migration failed: %v\n", err)
		return 1
	}
	if migrated {
		fmt.Fprintln(a.out, "managed installation migration completed")
	}
	return 0
}

func hasReleaseAsset(release githubRelease, name string) bool {
	for _, asset := range release.Assets {
		if asset.Name == name {
			return true
		}
	}
	return false
}

func (a *App) fetchUpdateAsset(ctx context.Context, assetURL string, maxSize int64) ([]byte, error) {
	if err := validateHTTPSURL(assetURL); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	request.Header.Set("User-Agent", a.updateUserAgent())
	response, err := a.updateHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, githubHTTPError("download release asset", response)
	}
	return readBounded(response.Body, maxSize)
}

func readBounded(reader io.Reader, maxSize int64) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maxSize {
		return nil, fmt.Errorf("response exceeds the %d-byte limit", maxSize)
	}
	return contents, nil
}

func githubHTTPError(operation string, response *http.Response) error {
	if response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0") {
		return fmt.Errorf("%s was rate limited by GitHub (HTTP %d); try again later", operation, response.StatusCode)
	}
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s returned HTTP 404; the stable release or asset may be unavailable", operation)
	}
	return fmt.Errorf("%s returned HTTP %d", operation, response.StatusCode)
}

func appendReleasePath(base string, elements ...string) (string, error) {
	if err := validateHTTPSURL(base); err != nil {
		return "", err
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	for _, element := range elements {
		if element == "" || element == "." || element == ".." || strings.ContainsAny(element, "/\\") {
			return "", fmt.Errorf("invalid release path component %q", element)
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(element)
	}
	return parsed.String(), nil
}

func validateHTTPSURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("release downloads must use an HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("release URL must not contain credentials, query parameters, or fragments")
	}
	return nil
}

func (a *App) updateHTTPClient() *http.Client {
	if a.httpClient != nil {
		return a.httpClient
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (a *App) updateUserAgent() string {
	version := buildinfo.Version
	if a.currentVersion != nil {
		version = a.currentVersion()
	}
	version = strings.TrimSpace(version)
	if version == "" || strings.ContainsAny(version, "\r\n") {
		version = "dev"
	}
	return "Stealth/" + version + " (self-update)"
}

func (a *App) currentExecutablePath() (string, error) {
	getExecutable := a.executablePath
	if getExecutable == nil {
		getExecutable = os.Executable
	}
	executable, err := getExecutable()
	if err != nil {
		return "", fmt.Errorf("cannot determine the running Stealth executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the running Stealth executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the running Stealth executable %s: %w", executable, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot inspect the running Stealth executable %s: %w", resolved, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return "", fmt.Errorf("running Stealth executable %s is not a regular executable file", resolved)
	}
	parentInfo, err := os.Stat(filepath.Dir(resolved))
	if err != nil || !parentInfo.IsDir() {
		return "", fmt.Errorf("cannot access the installation directory for %s", resolved)
	}
	return resolved, nil
}

func extractUpdateBinary(archive []byte) (string, string, error) {
	temporaryDir, err := os.MkdirTemp("", "stealth-update-")
	if err != nil {
		return "", "", fmt.Errorf("create secure temporary directory: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(temporaryDir)
	}

	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		cleanup()
		return "", "", fmt.Errorf("read gzip archive: %w", err)
	}
	tarReader := tar.NewReader(reader)
	found := false
	temporaryBinary := filepath.Join(temporaryDir, "stealth")
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			_ = reader.Close()
			cleanup()
			return "", "", fmt.Errorf("read tar archive: %w", nextErr)
		}
		if header.Name != "stealth" || path.IsAbs(header.Name) || filepath.IsAbs(header.Name) || path.Clean(header.Name) != header.Name {
			_ = reader.Close()
			cleanup()
			return "", "", fmt.Errorf("release archive contains unexpected path %q", header.Name)
		}
		if found {
			_ = reader.Close()
			cleanup()
			return "", "", fmt.Errorf("release archive contains multiple Stealth binaries")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			_ = reader.Close()
			cleanup()
			return "", "", fmt.Errorf("release archive entry %q is not a regular file", header.Name)
		}
		if header.Size <= 0 || header.Size > maxUpdateBinarySize {
			_ = reader.Close()
			cleanup()
			return "", "", fmt.Errorf("release binary has an invalid size")
		}
		file, openErr := os.OpenFile(temporaryBinary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if openErr != nil {
			_ = reader.Close()
			cleanup()
			return "", "", fmt.Errorf("create extracted binary: %w", openErr)
		}
		written, copyErr := io.CopyN(file, tarReader, header.Size)
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil || written != header.Size || syncErr != nil || closeErr != nil {
			cleanup()
			if copyErr != nil {
				return "", "", fmt.Errorf("extract Stealth binary: %w", copyErr)
			}
			return "", "", fmt.Errorf("extract Stealth binary: incomplete or unsafely persisted file")
		}
		if err := os.Chmod(temporaryBinary, 0755); err != nil {
			_ = reader.Close()
			cleanup()
			return "", "", fmt.Errorf("make extracted binary executable: %w", err)
		}
		found = true
	}
	if err := reader.Close(); err != nil {
		cleanup()
		return "", "", fmt.Errorf("close release archive: %w", err)
	}
	if !found {
		cleanup()
		return "", "", fmt.Errorf("release archive does not contain the Stealth binary")
	}
	return temporaryDir, temporaryBinary, nil
}

func (a *App) validateDownloadedBinary(ctx context.Context, binaryPath, expectedVersion string) error {
	info, err := os.Lstat(binaryPath)
	if err != nil {
		return fmt.Errorf("cannot inspect downloaded Stealth binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Mode()&0111 == 0 {
		return fmt.Errorf("downloaded Stealth binary is not a non-empty executable file")
	}
	runner := a.runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	output, err := runner.Output(ctx, "", binaryPath, "version")
	if err != nil {
		return fmt.Errorf("downloaded Stealth binary failed version validation: %w", err)
	}
	firstLine := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if firstLine != "Stealth "+expectedVersion {
		return fmt.Errorf("downloaded Stealth binary reported %q, expected Stealth %s", firstLine, expectedVersion)
	}
	return nil
}

func (a *App) replaceExecutable(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("update canceled: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".stealth-update-")
	if err != nil {
		return fmt.Errorf("cannot update this installation because %s is not writable; re-run the update with appropriate system permissions: %w", target, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	sourceFile, err := os.Open(source)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("open validated Stealth binary: %w", err)
	}
	_, copyErr := io.Copy(temporary, sourceFile)
	closeSourceErr := sourceFile.Close()
	if copyErr != nil {
		_ = temporary.Close()
		return fmt.Errorf("stage Stealth binary: %w", copyErr)
	}
	if closeSourceErr != nil {
		_ = temporary.Close()
		return fmt.Errorf("close validated Stealth binary: %w", closeSourceErr)
	}
	if err := temporary.Chmod(0755); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set staged Stealth binary permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("persist staged Stealth binary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close staged Stealth binary: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("update canceled: %w", err)
	}
	rename := a.renameFile
	if rename == nil {
		rename = os.Rename
	}
	if err := rename(temporaryPath, target); err != nil {
		return fmt.Errorf("could not replace %s; the current installation was left unchanged: %w", target, err)
	}
	return nil
}

func (a *App) printUpdateHeader() {
	if a.hasInteractiveTerminal() {
		fmt.Fprintln(a.out, titleStyle.Render("Stealth Update"))
		return
	}
	fmt.Fprintln(a.out, "Stealth Update")
}

func (a *App) printUpdateStep(mark, message string, style lipgloss.Style) {
	line := mark + " " + message
	if a.hasInteractiveTerminal() {
		fmt.Fprintln(a.out, style.Render(line))
		return
	}
	fmt.Fprintln(a.out, line)
}

func (a *App) printUpdateSuccess(message string) {
	line := "✓ " + message
	if a.hasInteractiveTerminal() {
		fmt.Fprintln(a.out, successStyle.Render(line))
		return
	}
	fmt.Fprintln(a.out, line)
}

func (a *App) printUpdateFailure(err error) {
	line := "✗ Update failed"
	if a.hasInteractiveTerminal() {
		fmt.Fprintln(a.errOut, errorStyle.Render(line))
	} else {
		fmt.Fprintln(a.errOut, line)
	}
	fmt.Fprintln(a.errOut, err)
	var migrated *platformMigratedUpdateError
	if errors.As(err, &migrated) {
		fmt.Fprintf(a.errOut, "The installed CLI was not replaced, but the platform migration reached %s. Re-run `stealth update` to reconcile the CLI.\n", migrated.targetVersion)
		return
	}
	fmt.Fprintln(a.errOut, "Your existing Stealth CLI was not modified.")
}
