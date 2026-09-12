package config

import (
	"path/filepath"
	"testing"
)

func TestLoadStorageSettingsOwnsLocalStorageEnvironment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	t.Setenv("STORAGE_ROOT", root)
	t.Setenv("STORAGE_MAX_FILE_SIZE", "8MiB")
	t.Setenv("STORAGE_DEFAULT_QUOTA_BYTES", "64MiB")
	t.Setenv("STORAGE_DRIVER", "local")
	t.Setenv("STORAGE_S3_STAGING_ROOT", filepath.Join(root, "staging"))

	settings, err := loadStorageSettings()
	if err != nil {
		t.Fatalf("loadStorageSettings returned error: %v", err)
	}
	if settings.root != root || settings.maxFileSize != 8<<20 || settings.defaultQuota != 64<<20 {
		t.Fatalf("unexpected local storage settings: %+v", settings)
	}
	if settings.driver != "local" || settings.s3UseSSL != true || !settings.s3PathStyle {
		t.Fatalf("unexpected storage mode settings: %+v", settings)
	}
}

func TestLoadStorageSettingsRequiresCompleteS3Configuration(t *testing.T) {
	t.Setenv("STORAGE_DRIVER", "s3")
	t.Setenv("STORAGE_S3_ENDPOINT", "https://s3.example.test")
	t.Setenv("STORAGE_S3_BUCKET", "stealth")
	t.Setenv("STORAGE_S3_ACCESS_KEY", "access")
	t.Setenv("STORAGE_S3_SECRET_KEY", "secret")

	settings, err := loadStorageSettings()
	if err != nil {
		t.Fatalf("loadStorageSettings returned error: %v", err)
	}
	var config Config
	settings.apply(&config)
	if err := config.ValidateStorage(); err != nil {
		t.Fatalf("applied S3 settings failed validation: %v", err)
	}
}

func TestStorageDefaultsRemainAvailableToStructLiteralConfigs(t *testing.T) {
	config := Config{}
	config.applyStorageDefaults()

	if config.StorageRoot != "/var/lib/stealth/storage" || config.StorageMaxFileSize != 50<<20 || config.StorageDefaultQuotaBytes != 1<<30 {
		t.Fatalf("storage defaults = %+v", config)
	}
}
