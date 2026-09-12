package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/buildinfo"
)

var releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func validateReleaseVersion(version string) error {
	if !releaseVersionPattern.MatchString(strings.TrimSpace(version)) {
		return fmt.Errorf("release version %q must match vMAJOR.MINOR.PATCH", version)
	}
	return nil
}

func (a *App) resolveReleaseVersion(override string) (string, error) {
	version := strings.TrimSpace(override)
	if version == "" {
		version = strings.TrimSpace(os.Getenv("STEALTH_VERSION"))
	}
	if version == "" {
		version = strings.TrimSpace(buildinfo.Version)
	}
	if version == "" || version == "dev" {
		return "", fmt.Errorf("this development build has no release version; set STEALTH_VERSION or use a release CLI")
	}
	if err := validateReleaseVersion(version); err != nil {
		return "", err
	}
	return version, nil
}

func releaseAsset(goos, goarch string) (string, error) {
	switch {
	case goos == "linux" && goarch == "amd64":
		return "stealth_Linux_x86_64.tar.gz", nil
	case goos == "linux" && goarch == "arm64":
		return "stealth_Linux_arm64.tar.gz", nil
	default:
		return "", fmt.Errorf("unsupported platform %s/%s; release installers support Linux amd64 and arm64", goos, goarch)
	}
}

func (a *App) rawAssetURL(version, path string) string {
	return strings.TrimRight(a.assetBase, "/") + "/" + version + "/" + path
}

func (a *App) fetchAsset(ctx context.Context, assetURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create asset request: %w", err)
	}
	response, err := a.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download asset returned HTTP %d", response.StatusCode)
	}
	const maxAssetSize = 2 << 20
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxAssetSize+1))
	if err != nil {
		return nil, fmt.Errorf("read downloaded asset: %w", err)
	}
	if len(contents) > maxAssetSize {
		return nil, fmt.Errorf("downloaded asset is unexpectedly large")
	}
	return contents, nil
}

func verifySHA256(contents []byte, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if len(expected) != sha256.Size*2 {
		return fmt.Errorf("checksum is not a SHA-256 digest")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return fmt.Errorf("checksum is not a SHA-256 digest")
	}
	actual := sha256.Sum256(contents)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
		return fmt.Errorf("checksum mismatch")
	}
	return nil
}

func checksumForAsset(checksums, asset string) (string, error) {
	var found string
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			if len(fields) != 2 {
				return "", fmt.Errorf("checksum entry for %s is malformed", asset)
			}
			if found != "" {
				return "", fmt.Errorf("checksum for %s is duplicated", asset)
			}
			found = fields[0]
		}
	}
	if found != "" {
		return found, nil
	}
	return "", fmt.Errorf("checksum for %s was not found", asset)
}
