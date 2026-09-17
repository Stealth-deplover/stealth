package config

import (
	"fmt"
	"strconv"
)

// siteSettings owns the bounded static-publication limits. The ACME listener
// settings remain in the transport/TLS domain because they also coordinate
// with the main HTTP listener.
type siteSettings struct {
	maxArtifactSize     int64
	defaultQuotaBytes   int64
	maxExpandedBytes    int64
	maxFiles            int
	gitFetchConcurrency int
}

func loadSiteSettings() (siteSettings, error) {
	maxArtifactSize, err := parseBytes(value("SITES_MAX_ARTIFACT_SIZE", "50MiB"))
	if err != nil || maxArtifactSize < 1 {
		return siteSettings{}, fmt.Errorf("SITES_MAX_ARTIFACT_SIZE must be a positive byte quantity")
	}
	defaultQuotaBytes, err := parseBytes(value("SITES_DEFAULT_QUOTA_BYTES", "1GiB"))
	if err != nil || defaultQuotaBytes < 1 {
		return siteSettings{}, fmt.Errorf("SITES_DEFAULT_QUOTA_BYTES must be a positive byte quantity")
	}
	maxExpandedBytes, err := parseBytes(value("SITES_MAX_EXPANDED_BYTES", "256MiB"))
	if err != nil || maxExpandedBytes < 1 {
		return siteSettings{}, fmt.Errorf("SITES_MAX_EXPANDED_BYTES must be a positive byte quantity")
	}
	maxFiles, err := strconv.Atoi(value("SITES_MAX_FILES", "4096"))
	if err != nil || maxFiles < 1 || maxFiles > 100000 {
		return siteSettings{}, fmt.Errorf("SITES_MAX_FILES must be an integer between 1 and 100000")
	}
	gitFetchConcurrency, err := strconv.Atoi(value("SITES_GIT_FETCH_CONCURRENCY", "4"))
	if err != nil || gitFetchConcurrency < 1 || gitFetchConcurrency > 32 {
		return siteSettings{}, fmt.Errorf("SITES_GIT_FETCH_CONCURRENCY must be an integer between 1 and 32")
	}
	return siteSettings{
		maxArtifactSize:     maxArtifactSize,
		defaultQuotaBytes:   defaultQuotaBytes,
		maxExpandedBytes:    maxExpandedBytes,
		maxFiles:            maxFiles,
		gitFetchConcurrency: gitFetchConcurrency,
	}, nil
}

func (s siteSettings) apply(c *Config) {
	c.SitesMaxArtifactSize = s.maxArtifactSize
	c.SitesDefaultQuotaBytes = s.defaultQuotaBytes
	c.SitesMaxExpandedBytes = s.maxExpandedBytes
	c.SitesMaxFiles = s.maxFiles
	c.SitesGitFetchConcurrency = s.gitFetchConcurrency
}

func (c *Config) applySiteDefaults() {
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
}
