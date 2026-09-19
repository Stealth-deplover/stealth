package cli

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/installengine"
)

type InstallPlan = installengine.Plan

type localPorts struct {
	API     string
	Console string
	Proxy   string
}

func newInstallPlan(layout InstallLayout, version, publicURL string, dockerGID uint32) InstallPlan {
	return InstallPlan{
		Layout:    layout,
		Version:   version,
		PublicURL: publicURL,
		DockerGID: dockerGID,
	}
}

func validatePublicURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", fmt.Errorf("URL contains invalid control characters")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func generateConfig(plan InstallPlan) (string, error) {
	return installengine.GenerateConfig(installengine.ConfigOptions{
		Version:           plan.Version,
		PublicURL:         plan.PublicURL,
		GitHubAppClientID: plan.GitHubAppClientID,
		DockerGID:         plan.DockerGID,
		Setup:             plan.Setup,
		InstallRoot:       plan.Layout.Root,
	})
}

func validateGitHubAppClientID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 160 {
		return "", fmt.Errorf("GitHub App Client ID is required")
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '.' || character == '-' || character == '_' {
			continue
		}
		return "", fmt.Errorf("GitHub App Client ID contains unsupported characters")
	}
	return value, nil
}

func imageName(name, version string) string {
	return installengine.ImageName(name, version)
}

func formatEnvFile(values map[string]string) string {
	return installengine.FormatEnvFile(values)
}

func readEnvFile(path string) (map[string]string, error) {
	return installengine.ReadEnvFile(path)
}

func validEnvKey(value string) bool {
	return installengine.ValidEnvKey(value)
}

func hasRequiredConfig(values map[string]string) bool {
	for _, key := range []string{
		"STEALTH_API_IMAGE",
		"STEALTH_WORKER_IMAGE",
		"STEALTH_MIGRATE_IMAGE",
		"STEALTH_CONSOLE_IMAGE",
		"STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE",
		"POSTGRES_DB",
		"POSTGRES_USER",
		"POSTGRES_PASSWORD",
		"REDIS_PASSWORD",
		"FUNCTIONS_SECRET_KEY",
		"BOOTSTRAP_CLI_KEY",
		"PUBLIC_APP_URL",
		"DOCKER_GID",
	} {
		if strings.TrimSpace(values[key]) == "" {
			return false
		}
	}
	if !strings.EqualFold(strings.TrimSpace(values["SETUP_MODE"]), "true") && strings.TrimSpace(values["GITHUB_APP_CLIENT_ID"]) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(values["SETUP_MODE"]), "true") && strings.TrimSpace(values["STEALTH_SETUP_IMAGE"]) == "" {
		return false
	}
	return true
}

func configCheckDetail(path string, private bool) string {
	if private {
		return path + " (mode 0600)"
	}
	return path + " (expected mode 0600)"
}

func validLogService(service string) bool {
	switch service {
	case "api", "worker", "console", "proxy", "postgres", "redis", "clickhouse", "otel-collector", "telemetry-host", "telemetry-docker-logs", "telemetry-docker-proxy", "telemetry-docker", "migrate", "setup", "setup-console", "setup-proxy", "cloudflared":
		return true
	default:
		return false
	}
}

func portsFromConfig(values map[string]string) localPorts {
	return localPorts{
		API:     portOrDefault(values["API_HOST_PORT"], "18080"),
		Console: portOrDefault(values["CONSOLE_HOST_PORT"], "13000"),
		Proxy:   portOrDefault(values["PROXY_HTTP_PORT"], "8080"),
	}
}

func setupPortsFromConfig(values map[string]string) localPorts {
	return localPorts{
		API:     portOrDefault(values["SETUP_API_HOST_PORT"], "18081"),
		Console: portOrDefault(values["SETUP_CONSOLE_HOST_PORT"], "13001"),
		Proxy:   portOrDefault(values["SETUP_PROXY_HTTP_PORT"], "8081"),
	}
}

func portOrDefault(raw, fallback string) string {
	return installengine.PortOrDefault(raw, fallback)
}

func (a *App) loadExistingPlan(layout InstallLayout) (*InstallPlan, error) {
	values, err := readEnvFile(layout.EnvFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", layout.EnvFile, err)
	}
	publicURL, err := validatePublicURL(values["PUBLIC_APP_URL"])
	if err != nil {
		return nil, fmt.Errorf("existing PUBLIC_APP_URL is invalid: %w", err)
	}
	version, hasVersionFile, err := readInstalledVersion(layout)
	if err != nil {
		return nil, fmt.Errorf("read existing VERSION: %w", err)
	}
	versionSource := "VERSION"
	if !hasVersionFile {
		versionSource = "STEALTH_API_IMAGE"
		version, err = imageVersion(values["STEALTH_API_IMAGE"])
		if err != nil {
			return nil, fmt.Errorf("existing STEALTH_API_IMAGE is invalid: %w", err)
		}
	}
	if err := validateReleaseVersion(version); err != nil {
		return nil, fmt.Errorf("existing release version from %s is invalid: %w", versionSource, err)
	}
	setupMode := strings.EqualFold(strings.TrimSpace(values["SETUP_MODE"]), "true")
	githubAppClientID := strings.TrimSpace(values["GITHUB_APP_CLIENT_ID"])
	if !setupMode {
		githubAppClientID, err = validateGitHubAppClientID(githubAppClientID)
		if err != nil {
			return nil, fmt.Errorf("existing GITHUB_APP_CLIENT_ID is invalid: %w", err)
		}
	} else if githubAppClientID != "" && !githubauth.ValidClientID(githubAppClientID) {
		return nil, fmt.Errorf("existing GITHUB_APP_CLIENT_ID is invalid")
	}
	gid, err := strconv.ParseUint(values["DOCKER_GID"], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("existing DOCKER_GID is invalid")
	}
	return &InstallPlan{Layout: layout, Version: version, PublicURL: publicURL, GitHubAppClientID: githubAppClientID, DockerGID: uint32(gid), Existing: true, Setup: setupMode, ExternalDatabase: strings.EqualFold(values["DATABASE_MODE"], "external"), ExternalRedis: strings.EqualFold(values["REDIS_MODE"], "external")}, nil
}

func imageVersion(image string) (string, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "", fmt.Errorf("image tag is missing")
	}
	if strings.Contains(image, "@") {
		return "", fmt.Errorf("digest-pinned images do not contain a release tag")
	}
	lastSlash := strings.LastIndexByte(image, '/')
	lastColon := strings.LastIndexByte(image, ':')
	if lastColon <= lastSlash || lastColon == len(image)-1 {
		return "", fmt.Errorf("image tag is missing")
	}
	if lastColon == 0 {
		return "", fmt.Errorf("image repository is missing")
	}
	version := image[lastColon+1:]
	return version, nil
}

func writePrivateFile(path, contents string) error {
	return installengine.WritePrivateFile(path, contents)
}

func writeAtomic(path string, contents []byte, mode os.FileMode) error {
	return installengine.WriteAtomic(path, contents, mode)
}
