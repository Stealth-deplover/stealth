package config

// WithDefaults returns a copy of the configuration with every unset or
// non-positive field replaced by the value the HTTP API would otherwise
// assume. Environment loading via Load already validates its own env
// fallbacks; these defaults cover struct-literal configurations such as
// tests and embedded API setups. Derived limits are applied in dependency
// order, so the storage defaults feed the Functions/Sites limits.
func (c Config) WithDefaults() Config {
	c.applyDatabaseDefaults()
	c.applyAuthDefaults()
	if c.StorageRoot == "" {
		c.StorageRoot = "/var/lib/stealth/storage"
	}
	if c.StorageMaxFileSize <= 0 {
		c.StorageMaxFileSize = 50 << 20
	}
	if c.StorageDefaultQuotaBytes <= 0 {
		c.StorageDefaultQuotaBytes = 1 << 30
	}
	c.applyExecutionDefaults()
	if c.SitesMaxArtifactSize <= 0 {
		c.SitesMaxArtifactSize = c.StorageMaxFileSize
	}
	if c.SitesMaxArtifactSize <= 0 {
		c.SitesMaxArtifactSize = 50 << 20
	}
	if c.SitesDefaultQuotaBytes <= 0 {
		c.SitesDefaultQuotaBytes = c.StorageDefaultQuotaBytes
	}
	if c.SitesDefaultQuotaBytes <= 0 {
		c.SitesDefaultQuotaBytes = 1 << 30
	}
	if c.SitesMaxExpandedBytes <= 0 {
		c.SitesMaxExpandedBytes = 256 << 20
	}
	if c.SitesMaxExpandedBytes > c.SitesDefaultQuotaBytes {
		c.SitesMaxExpandedBytes = c.SitesDefaultQuotaBytes
	}
	if c.SitesMaxFiles <= 0 {
		c.SitesMaxFiles = 4096
	}
	if c.SitesGitFetchConcurrency <= 0 {
		c.SitesGitFetchConcurrency = 4
	}
	if c.SitesGitFetchConcurrency > 32 {
		c.SitesGitFetchConcurrency = 32
	}
	return c
}
