// Package setupinstall owns the setup-specific input assembly for the shared
// installengine. It turns durable browser setup state into an idempotent plan
// and keeps environment-file mutation at that seam.
package setupinstall

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

// BuildPlan prepares the reviewed production environment and returns the
// shared install plan. Existing environment values are retained unless the
// setup state explicitly owns the corresponding value.
func BuildPlan(state setupstate.State, installRoot string) (installengine.Plan, error) {
	layout, err := installengine.NewLayout(installRoot)
	if err != nil {
		return installengine.Plan{}, err
	}
	base, err := installengine.ReadEnvFile(layout.EnvFile)
	if err != nil {
		return installengine.Plan{}, err
	}

	credentials := state.SetupCredentials()
	databaseURL := credentials.DatabaseURL
	if databaseURL == "" {
		databaseURL = base["DATABASE_URL"]
	}
	redisURL := credentials.RedisURL
	if redisURL == "" {
		redisURL = base["REDIS_URL"]
	}
	updates := map[string]string{
		"SETUP_MODE":           "false",
		"GITHUB_APP_CLIENT_ID": state.GitHub.ClientID,
		"PUBLIC_APP_URL":       state.Draft.PublicURL,
		"DATABASE_URL":         databaseURL,
		"REDIS_URL":            redisURL,
		"COOKIE_SECURE":        strconv.FormatBool(strings.HasPrefix(state.Draft.PublicURL, "https://")),
	}
	if strings.TrimSpace(base["STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE"]) == "" {
		updates["STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE"] = installengine.ImageName("stealth-telemetry-docker-proxy", BaseVersion(base))
	}
	if state.Draft.NetworkMode == "public_ip" {
		updates["PROXY_HTTP_BIND"] = "0.0.0.0"
	} else {
		updates["PROXY_HTTP_BIND"] = "127.0.0.1"
	}
	if state.Draft.StorageMode != "s3" {
		updates["STORAGE_DRIVER"] = "local"
	} else {
		updates["STORAGE_DRIVER"] = "s3"
		updates["STORAGE_S3_ENDPOINT"] = state.Draft.StorageS3Endpoint
		updates["STORAGE_S3_REGION"] = state.Draft.StorageS3Region
		updates["STORAGE_S3_BUCKET"] = state.Draft.StorageS3Bucket
		updates["STORAGE_S3_ACCESS_KEY"] = credentials.StorageS3AccessKey
		updates["STORAGE_S3_SECRET_KEY"] = credentials.StorageS3SecretKey
		updates["STORAGE_S3_USE_SSL"] = strconv.FormatBool(state.Draft.StorageS3UseSSL)
		updates["STORAGE_S3_PATH_STYLE"] = strconv.FormatBool(state.Draft.StorageS3PathStyle)
		updates["STORAGE_S3_PREFIX"] = state.Draft.StorageS3Prefix
	}
	cloudflareEnabled := state.Draft.NetworkMode == "cloudflare_tunnel"
	if cloudflareEnabled {
		if err := installengine.WritePrivateFile(filepath.Join(layout.Root, "state", "cloudflare-tunnel-token"), state.Secret("cloudflare_tunnel_token")+"\n"); err != nil {
			return installengine.Plan{}, err
		}
		updates["CLOUDFLARE_TUNNEL_TOKEN_FILE"] = "./state/cloudflare-tunnel-token"
	}
	contents, err := installengine.MergeEnv(base, updates)
	if err != nil {
		return installengine.Plan{}, err
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, contents); err != nil {
		return installengine.Plan{}, err
	}
	apiPort := installengine.PortOrDefault(base["API_HOST_PORT"], "18080")
	consolePort := installengine.PortOrDefault(base["CONSOLE_HOST_PORT"], "13000")
	proxyPort := installengine.PortOrDefault(base["PROXY_HTTP_PORT"], "8080")
	return installengine.Plan{
		Layout:            layout,
		Version:           BaseVersion(base),
		PublicURL:         state.Draft.PublicURL,
		GitHubAppClientID: state.GitHub.ClientID,
		Existing:          true,
		ExternalDatabase:  state.Draft.DatabaseMode == "external",
		ExternalRedis:     state.Draft.RedisMode == "external",
		Cloudflare:        cloudflareEnabled,
		VerifyPublicURL:   cloudflareEnabled || state.Draft.NetworkMode == "public_ip",
		// The installer now runs in the host CLI, not on the setup Compose
		// network. Health checks must therefore use the host-published ports.
		InternalAPIURL:     "http://127.0.0.1:" + apiPort,
		InternalConsoleURL: "http://127.0.0.1:" + consolePort,
		InternalProxyURL:   "http://127.0.0.1:" + proxyPort,
	}, nil
}

// BaseVersion extracts the release version from the existing API image.
func BaseVersion(values map[string]string) string {
	if value := strings.TrimSpace(values["STEALTH_API_IMAGE"]); value != "" {
		if index := strings.LastIndexByte(value, ':'); index >= 0 && index+1 < len(value) {
			return value[index+1:]
		}
	}
	return "v0.0.0"
}
