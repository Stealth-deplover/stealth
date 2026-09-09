package config

import (
	"strings"
	"time"
)

// WithDefaults returns a copy of the configuration with every unset or
// non-positive field replaced by the value the HTTP API would otherwise
// assume. Environment loading via Load already validates its own env
// fallbacks; these defaults cover struct-literal configurations such as
// tests and embedded API setups. Derived limits are applied in dependency
// order, so the storage defaults feed the Functions/Sites limits.
func (c Config) WithDefaults() Config {
	if c.DatabaseMaxConns <= 0 {
		c.DatabaseMaxConns = 16
	}
	if c.DatabaseMaxConns > 256 {
		c.DatabaseMaxConns = 256
	}
	if c.DatabaseMinConns < 0 {
		c.DatabaseMinConns = 0
	}
	if c.DatabaseMinConns > c.DatabaseMaxConns {
		c.DatabaseMinConns = c.DatabaseMaxConns
	}
	if c.DatabaseMaxConnLifetime <= 0 {
		c.DatabaseMaxConnLifetime = time.Hour
	}
	if c.DatabaseMaxConnIdleTime <= 0 {
		c.DatabaseMaxConnIdleTime = 30 * time.Minute
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = 720 * time.Hour
	}
	if strings.TrimSpace(c.SessionCookieName) == "" {
		c.SessionCookieName = "stealth_session"
	}
	if c.AppSessionTTL <= 0 {
		c.AppSessionTTL = c.SessionTTL
	}
	if c.AuthRateLimit <= 0 {
		c.AuthRateLimit = 10
	}
	if c.AuthRateWindow <= 0 {
		c.AuthRateWindow = time.Minute
	}
	if c.ProjectOperationRateLimit <= 0 {
		c.ProjectOperationRateLimit = 120
	}
	if c.ProjectOperationRateWindow <= 0 {
		c.ProjectOperationRateWindow = time.Minute
	}
	if c.AuthVerificationTTL <= 0 {
		c.AuthVerificationTTL = 24 * time.Hour
	}
	if c.AuthPasswordResetTTL <= 0 {
		c.AuthPasswordResetTTL = time.Hour
	}
	if strings.TrimSpace(c.PublicAppURL) == "" {
		c.PublicAppURL = "http://localhost:4173"
	}
	if c.StorageRoot == "" {
		c.StorageRoot = "/var/lib/stealth/storage"
	}
	if c.StorageMaxFileSize <= 0 {
		c.StorageMaxFileSize = 50 << 20
	}
	if c.StorageDefaultQuotaBytes <= 0 {
		c.StorageDefaultQuotaBytes = 1 << 30
	}
	if c.FunctionsMaxArtifactSize <= 0 {
		c.FunctionsMaxArtifactSize = c.StorageMaxFileSize
	}
	if c.FunctionsMaxArtifactSize <= 0 {
		c.FunctionsMaxArtifactSize = 50 << 20
	}
	if c.FunctionsDefaultQuotaBytes <= 0 {
		c.FunctionsDefaultQuotaBytes = c.StorageDefaultQuotaBytes
	}
	if c.FunctionsDefaultQuotaBytes <= 0 {
		c.FunctionsDefaultQuotaBytes = 1 << 30
	}
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
