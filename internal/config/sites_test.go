package config

import "testing"

func TestLoadSiteSettings(t *testing.T) {
	t.Setenv("SITES_MAX_ARTIFACT_SIZE", "8MiB")
	t.Setenv("SITES_DEFAULT_QUOTA_BYTES", "64MiB")
	t.Setenv("SITES_MAX_EXPANDED_BYTES", "32MiB")
	t.Setenv("SITES_MAX_FILES", "128")
	t.Setenv("SITES_GIT_FETCH_CONCURRENCY", "6")

	settings, err := loadSiteSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.maxArtifactSize != 8<<20 || settings.defaultQuotaBytes != 64<<20 || settings.maxExpandedBytes != 32<<20 || settings.maxFiles != 128 || settings.gitFetchConcurrency != 6 {
		t.Fatalf("unexpected site settings: %+v", settings)
	}
	var config Config
	settings.apply(&config)
	if config.SitesMaxArtifactSize != settings.maxArtifactSize || config.SitesMaxFiles != settings.maxFiles {
		t.Fatalf("settings were not applied: %+v", config)
	}
}

func TestApplySiteDefaultsUsesStorageLimits(t *testing.T) {
	config := Config{StorageMaxFileSize: 8 << 20, StorageDefaultQuotaBytes: 64 << 20}
	config.applySiteDefaults()
	if config.SitesMaxArtifactSize != 8<<20 || config.SitesDefaultQuotaBytes != 64<<20 || config.SitesMaxExpandedBytes != 64<<20 || config.SitesMaxFiles != 4096 || config.SitesGitFetchConcurrency != 4 {
		t.Fatalf("unexpected site defaults: %+v", config)
	}
}
