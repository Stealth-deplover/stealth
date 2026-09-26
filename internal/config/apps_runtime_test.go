package config

import (
	"strings"
	"testing"
	"time"
)

func clearAppRuntimeEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"APPS_RUNTIME_NETWORK_NAME", "APPS_RUNTIME_POLL_INTERVAL", "APPS_RUNTIME_LEASE_AGE",
		"APPS_RUNTIME_ACTION_TIMEOUT", "APPS_RUNTIME_IMAGE_IMPORT_TIMEOUT",
		"APPS_RUNTIME_IMAGE_CACHE_MAX_BYTES", "APPS_RUNTIME_IMAGE_CACHE_TARGET_BYTES", "APPS_RUNTIME_IMAGE_GC_INTERVAL",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadAppBuildSettingsRuntimeDefaults(t *testing.T) {
	clearAppRuntimeEnv(t)
	settings, err := loadAppBuildSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.runtimeNetworkName != "stealth_app_runtime" || settings.runtimePollInterval != time.Second ||
		settings.runtimeLeaseAge != 2*time.Minute || settings.runtimeActionTimeout != 30*time.Second ||
		settings.runtimeImageImportTimeout != 10*time.Minute || settings.runtimeImageCacheMaxBytes != 20<<30 ||
		settings.runtimeImageCacheTargetBytes != 16<<30 || settings.runtimeImageGCSweepInterval != 15*time.Minute {
		t.Fatalf("unexpected runtime defaults: %#v", settings)
	}
}

func TestLoadAppBuildSettingsRejectsUnsafeRuntimeBounds(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "network name", key: "APPS_RUNTIME_NETWORK_NAME", value: "Bad Network"},
		{name: "poll interval too short", key: "APPS_RUNTIME_POLL_INTERVAL", value: "1ms"},
		{name: "lease too short", key: "APPS_RUNTIME_LEASE_AGE", value: "1s"},
		{name: "action timeout too long", key: "APPS_RUNTIME_ACTION_TIMEOUT", value: "5m"},
		{name: "image import timeout too short", key: "APPS_RUNTIME_IMAGE_IMPORT_TIMEOUT", value: "30s"},
		{name: "image cache max too small", key: "APPS_RUNTIME_IMAGE_CACHE_MAX_BYTES", value: "512KiB"},
		{name: "image cache max too large", key: "APPS_RUNTIME_IMAGE_CACHE_MAX_BYTES", value: "2TiB"},
		{name: "image cache sweep too short", key: "APPS_RUNTIME_IMAGE_GC_INTERVAL", value: "10s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearAppRuntimeEnv(t)
			t.Setenv(test.key, test.value)
			if _, err := loadAppBuildSettings(); err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("invalid %s=%q returned %v", test.key, test.value, err)
			}
		})
	}
}

func TestLoadAppBuildSettingsRequiresCacheTargetBelowMaximum(t *testing.T) {
	clearAppRuntimeEnv(t)
	t.Setenv("APPS_RUNTIME_IMAGE_CACHE_MAX_BYTES", "4GiB")
	t.Setenv("APPS_RUNTIME_IMAGE_CACHE_TARGET_BYTES", "4GiB")
	if _, err := loadAppBuildSettings(); err == nil || !strings.Contains(err.Error(), "APPS_RUNTIME_IMAGE_CACHE_TARGET_BYTES") {
		t.Fatalf("invalid equal cache target/max returned %v", err)
	}
}
