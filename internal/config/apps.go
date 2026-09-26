package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type appBuildSettings struct {
	maxSourceArchiveBytes        int64
	maxExpandedSourceBytes       int64
	maxSourceFiles               int
	maxImageArchiveBytes         int64
	defaultArtifactQuotaBytes    int64
	buildkitAddress              string
	buildkitCACert               string
	buildkitClientCert           string
	buildkitClientKey            string
	buildTimeout                 time.Duration
	buildLeaseAge                time.Duration
	buildPollInterval            time.Duration
	buildStagingRoot             string
	buildStagingVolume           string
	buildkitStateVolume          string
	runtimeNetworkName           string
	runtimePollInterval          time.Duration
	runtimeLeaseAge              time.Duration
	runtimeActionTimeout         time.Duration
	runtimeImageImportTimeout    time.Duration
	runtimeImageCacheMaxBytes    int64
	runtimeImageCacheTargetBytes int64
	runtimeImageGCSweepInterval  time.Duration
}

func loadAppBuildSettings() (appBuildSettings, error) {
	settings := appBuildSettings{}
	var err error
	if settings.maxSourceArchiveBytes, err = boundedAppBytes("APPS_MAX_SOURCE_ARCHIVE_BYTES", "128MiB", 1, 2<<30); err != nil {
		return appBuildSettings{}, err
	}
	if settings.maxExpandedSourceBytes, err = boundedAppBytes("APPS_MAX_EXPANDED_SOURCE_BYTES", "1GiB", 1, 16<<30); err != nil {
		return appBuildSettings{}, err
	}
	if settings.maxImageArchiveBytes, err = boundedAppBytes("APPS_MAX_IMAGE_ARCHIVE_BYTES", "2GiB", 1, 16<<30); err != nil {
		return appBuildSettings{}, err
	}
	if settings.defaultArtifactQuotaBytes, err = boundedAppBytes("APPS_DEFAULT_ARTIFACT_QUOTA_BYTES", "5GiB", 1, 1<<40); err != nil {
		return appBuildSettings{}, err
	}
	if settings.defaultArtifactQuotaBytes < settings.maxSourceArchiveBytes {
		return appBuildSettings{}, fmt.Errorf("APPS_DEFAULT_ARTIFACT_QUOTA_BYTES must be at least APPS_MAX_SOURCE_ARCHIVE_BYTES")
	}
	settings.maxSourceFiles, err = strconv.Atoi(value("APPS_MAX_SOURCE_FILES", "8192"))
	if err != nil || settings.maxSourceFiles < 1 || settings.maxSourceFiles > 1000000 {
		return appBuildSettings{}, fmt.Errorf("APPS_MAX_SOURCE_FILES must be an integer between 1 and 1000000")
	}
	settings.buildTimeout, err = time.ParseDuration(value("APPS_BUILD_TIMEOUT", "20m"))
	if err != nil || settings.buildTimeout < time.Minute || settings.buildTimeout > 24*time.Hour {
		return appBuildSettings{}, fmt.Errorf("APPS_BUILD_TIMEOUT must be between 1m and 24h")
	}
	settings.buildLeaseAge, err = time.ParseDuration(value("APPS_BUILD_LEASE_AGE", "25m"))
	if err != nil || settings.buildLeaseAge < settings.buildTimeout || settings.buildLeaseAge > 48*time.Hour {
		return appBuildSettings{}, fmt.Errorf("APPS_BUILD_LEASE_AGE must be at least APPS_BUILD_TIMEOUT and no longer than 48h")
	}
	settings.buildPollInterval, err = time.ParseDuration(value("APPS_BUILD_POLL_INTERVAL", "500ms"))
	if err != nil || settings.buildPollInterval < 100*time.Millisecond || settings.buildPollInterval > time.Minute {
		return appBuildSettings{}, fmt.Errorf("APPS_BUILD_POLL_INTERVAL must be between 100ms and 1m")
	}
	settings.buildkitAddress = strings.TrimSpace(value("APPS_BUILDKIT_ADDRESS", "tcp://buildkit:1234"))
	if !validBuildkitAddress(settings.buildkitAddress) {
		return appBuildSettings{}, fmt.Errorf("APPS_BUILDKIT_ADDRESS must be a private TCP host:port address")
	}
	for key, target := range map[string]*string{
		"APPS_BUILDKIT_CA_CERT":     &settings.buildkitCACert,
		"APPS_BUILDKIT_CLIENT_CERT": &settings.buildkitClientCert,
		"APPS_BUILDKIT_CLIENT_KEY":  &settings.buildkitClientKey,
	} {
		path := value(key, defaultBuildKitPath(key))
		if !validBuildKitPath(path) {
			return appBuildSettings{}, fmt.Errorf("%s must be an absolute, clean, non-root path", key)
		}
		*target = path
	}
	settings.buildStagingRoot = filepath.Clean(value("APPS_BUILD_STAGING_ROOT", "/var/lib/stealth/app-build-staging"))
	if !filepath.IsAbs(settings.buildStagingRoot) || settings.buildStagingRoot == string(filepath.Separator) {
		return appBuildSettings{}, fmt.Errorf("APPS_BUILD_STAGING_ROOT must be an absolute non-root path")
	}
	settings.buildStagingVolume = value("APPS_BUILD_STAGING_VOLUME", "stealth-app-build-staging")
	if len(settings.buildStagingVolume) > 255 || !isDockerName(settings.buildStagingVolume) {
		return appBuildSettings{}, fmt.Errorf("APPS_BUILD_STAGING_VOLUME must be a valid Docker volume name")
	}
	settings.buildkitStateVolume = value("APPS_BUILDKIT_STATE_VOLUME", "stealth_app_buildkit_state")
	if len(settings.buildkitStateVolume) > 255 || !isDockerName(settings.buildkitStateVolume) {
		return appBuildSettings{}, fmt.Errorf("APPS_BUILDKIT_STATE_VOLUME must be a valid Docker volume name")
	}
	settings.runtimeNetworkName = value("APPS_RUNTIME_NETWORK_NAME", "stealth_app_runtime")
	if len(settings.runtimeNetworkName) > 63 || !isDockerName(settings.runtimeNetworkName) {
		return appBuildSettings{}, fmt.Errorf("APPS_RUNTIME_NETWORK_NAME must be a valid Docker network name")
	}
	settings.runtimePollInterval, err = time.ParseDuration(value("APPS_RUNTIME_POLL_INTERVAL", "1s"))
	if err != nil || settings.runtimePollInterval < 100*time.Millisecond || settings.runtimePollInterval > time.Minute {
		return appBuildSettings{}, fmt.Errorf("APPS_RUNTIME_POLL_INTERVAL must be between 100ms and 1m")
	}
	settings.runtimeLeaseAge, err = time.ParseDuration(value("APPS_RUNTIME_LEASE_AGE", "2m"))
	if err != nil || settings.runtimeLeaseAge < 30*time.Second || settings.runtimeLeaseAge > 10*time.Minute {
		return appBuildSettings{}, fmt.Errorf("APPS_RUNTIME_LEASE_AGE must be between 30s and 10m")
	}
	settings.runtimeActionTimeout, err = time.ParseDuration(value("APPS_RUNTIME_ACTION_TIMEOUT", "30s"))
	if err != nil || settings.runtimeActionTimeout < 5*time.Second || settings.runtimeActionTimeout > 2*time.Minute {
		return appBuildSettings{}, fmt.Errorf("APPS_RUNTIME_ACTION_TIMEOUT must be between 5s and 2m")
	}
	settings.runtimeImageImportTimeout, err = time.ParseDuration(value("APPS_RUNTIME_IMAGE_IMPORT_TIMEOUT", "10m"))
	if err != nil || settings.runtimeImageImportTimeout < time.Minute || settings.runtimeImageImportTimeout > 30*time.Minute {
		return appBuildSettings{}, fmt.Errorf("APPS_RUNTIME_IMAGE_IMPORT_TIMEOUT must be between 1m and 30m")
	}
	if settings.runtimeImageCacheMaxBytes, err = boundedAppBytes("APPS_RUNTIME_IMAGE_CACHE_MAX_BYTES", "20GiB", 1<<20, 1<<40); err != nil {
		return appBuildSettings{}, err
	}
	if settings.runtimeImageCacheTargetBytes, err = boundedAppBytes("APPS_RUNTIME_IMAGE_CACHE_TARGET_BYTES", "16GiB", 1<<20, 1<<40); err != nil {
		return appBuildSettings{}, err
	}
	if settings.runtimeImageCacheTargetBytes >= settings.runtimeImageCacheMaxBytes {
		return appBuildSettings{}, fmt.Errorf("APPS_RUNTIME_IMAGE_CACHE_TARGET_BYTES must be less than APPS_RUNTIME_IMAGE_CACHE_MAX_BYTES")
	}
	settings.runtimeImageGCSweepInterval, err = time.ParseDuration(value("APPS_RUNTIME_IMAGE_GC_INTERVAL", "15m"))
	if err != nil || settings.runtimeImageGCSweepInterval < time.Minute || settings.runtimeImageGCSweepInterval > 24*time.Hour {
		return appBuildSettings{}, fmt.Errorf("APPS_RUNTIME_IMAGE_GC_INTERVAL must be between 1m and 24h")
	}
	return settings, nil
}

func boundedAppBytes(name, fallback string, minimum, maximum int64) (int64, error) {
	parsed, err := parseBytes(value(name, fallback))
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d bytes", name, minimum, maximum)
	}
	return parsed, nil
}

func validBuildkitAddress(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "tcp" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return false
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || strings.TrimSpace(host) == "" {
		return false
	}
	portNumber, err := strconv.Atoi(port)
	return err == nil && portNumber >= 1 && portNumber <= 65535
}

func (s appBuildSettings) apply(config *Config) {
	config.AppsMaxSourceArchiveBytes = s.maxSourceArchiveBytes
	config.AppsMaxExpandedSourceBytes = s.maxExpandedSourceBytes
	config.AppsMaxSourceFiles = s.maxSourceFiles
	config.AppsMaxImageArchiveBytes = s.maxImageArchiveBytes
	config.AppsDefaultArtifactQuotaBytes = s.defaultArtifactQuotaBytes
	config.AppsBuildkitAddress = s.buildkitAddress
	config.AppsBuildkitCACert = s.buildkitCACert
	config.AppsBuildkitClientCert = s.buildkitClientCert
	config.AppsBuildkitClientKey = s.buildkitClientKey
	config.AppsBuildTimeout = s.buildTimeout
	config.AppsBuildLeaseAge = s.buildLeaseAge
	config.AppsBuildPollInterval = s.buildPollInterval
	config.AppsBuildStagingRoot = s.buildStagingRoot
	config.AppsBuildStagingVolume = s.buildStagingVolume
	config.AppsBuildkitStateVolume = s.buildkitStateVolume
	config.AppsRuntimeNetworkName = s.runtimeNetworkName
	config.AppsRuntimePollInterval = s.runtimePollInterval
	config.AppsRuntimeLeaseAge = s.runtimeLeaseAge
	config.AppsRuntimeActionTimeout = s.runtimeActionTimeout
	config.AppsRuntimeImageImportTimeout = s.runtimeImageImportTimeout
	config.AppsRuntimeImageCacheMaxBytes = s.runtimeImageCacheMaxBytes
	config.AppsRuntimeImageCacheTargetBytes = s.runtimeImageCacheTargetBytes
	config.AppsRuntimeImageGCSweepInterval = s.runtimeImageGCSweepInterval
}

func defaultBuildKitPath(key string) string {
	switch key {
	case "APPS_BUILDKIT_CA_CERT":
		return "/run/secrets/stealth-buildkit/ca.pem"
	case "APPS_BUILDKIT_CLIENT_CERT":
		return "/run/secrets/stealth-buildkit/client-cert.pem"
	default:
		return "/run/secrets/stealth-buildkit/client-key.pem"
	}
}

func validBuildKitPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator) && !strings.ContainsAny(path, "\x00\r\n")
}
