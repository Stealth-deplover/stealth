package config

import "time"

// WithDefaults returns a copy of the configuration with every unset or
// non-positive field replaced by the value the HTTP API would otherwise
// assume. Environment loading via Load already validates its own env
// fallbacks; these defaults cover struct-literal configurations such as
// tests and embedded API setups. Derived limits are applied in dependency
// order, so the storage defaults feed the Functions/Sites limits.
func (c Config) WithDefaults() Config {
	c.applyDatabaseDefaults()
	c.applyAuthDefaults()
	c.applyStorageDefaults()
	c.applyExecutionDefaults()
	c.applySiteDefaults()
	c.applyAppBuildDefaults()
	c.applyIngressDefaults()
	c.applyTelemetryStoreDefaults()
	return c
}

func (c *Config) applyAppBuildDefaults() {
	if c.AppsMaxSourceArchiveBytes <= 0 {
		c.AppsMaxSourceArchiveBytes = 128 << 20
	}
	if c.AppsMaxExpandedSourceBytes <= 0 {
		c.AppsMaxExpandedSourceBytes = 1 << 30
	}
	if c.AppsMaxSourceFiles <= 0 {
		c.AppsMaxSourceFiles = 8192
	}
	if c.AppsMaxImageArchiveBytes <= 0 {
		c.AppsMaxImageArchiveBytes = 2 << 30
	}
	if c.AppsDefaultArtifactQuotaBytes <= 0 {
		c.AppsDefaultArtifactQuotaBytes = 5 << 30
	}
	if c.AppsBuildkitAddress == "" {
		c.AppsBuildkitAddress = "tcp://buildkit:1234"
	}
	if c.AppsBuildkitCACert == "" {
		c.AppsBuildkitCACert = "/run/secrets/stealth-buildkit/ca.pem"
	}
	if c.AppsBuildkitClientCert == "" {
		c.AppsBuildkitClientCert = "/run/secrets/stealth-buildkit/client-cert.pem"
	}
	if c.AppsBuildkitClientKey == "" {
		c.AppsBuildkitClientKey = "/run/secrets/stealth-buildkit/client-key.pem"
	}
	if c.AppsBuildTimeout <= 0 {
		c.AppsBuildTimeout = 20 * time.Minute
	}
	if c.AppsBuildLeaseAge <= 0 {
		c.AppsBuildLeaseAge = 25 * time.Minute
	}
	if c.AppsBuildPollInterval <= 0 {
		c.AppsBuildPollInterval = 500 * time.Millisecond
	}
	if c.AppsBuildStagingRoot == "" {
		c.AppsBuildStagingRoot = "/var/lib/stealth/app-build-staging"
	}
	if c.AppsBuildStagingVolume == "" {
		c.AppsBuildStagingVolume = "stealth_app_build_staging"
	}
	if c.AppsBuildkitStateVolume == "" {
		c.AppsBuildkitStateVolume = "stealth_app_buildkit_state"
	}
	if c.AppsRuntimeNetworkName == "" {
		c.AppsRuntimeNetworkName = "stealth_app_runtime"
	}
	if c.AppsRuntimePollInterval <= 0 {
		c.AppsRuntimePollInterval = time.Second
	}
	if c.AppsRuntimeLeaseAge <= 0 {
		c.AppsRuntimeLeaseAge = 2 * time.Minute
	}
	if c.AppsRuntimeActionTimeout <= 0 {
		c.AppsRuntimeActionTimeout = 30 * time.Second
	}
	if c.AppsRuntimeImageImportTimeout <= 0 {
		c.AppsRuntimeImageImportTimeout = 10 * time.Minute
	}
}
