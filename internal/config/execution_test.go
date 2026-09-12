package config

import (
	"testing"
	"time"
)

func TestLoadExecutionSettingsOwnsRuntimeEnvironment(t *testing.T) {
	t.Setenv("FUNCTIONS_MAX_ARTIFACT_SIZE", "8MiB")
	t.Setenv("FUNCTIONS_DEFAULT_QUOTA_BYTES", "64MiB")
	t.Setenv("FUNCTIONS_RUNNER_ENABLED", "false")
	t.Setenv("FUNCTIONS_WORKER_ID", "worker.test")
	t.Setenv("FUNCTIONS_RUNNER_POLL", "2s")
	t.Setenv("FUNCTIONS_RUNNER_LEASE_AGE", "5m")
	t.Setenv("FUNCTIONS_RUNNER_BUILD_TIMEOUT", "3m")
	t.Setenv("FUNCTIONS_RUNNER_STAGING_ROOT", "/tmp/stealth-runner")
	t.Setenv("FUNCTIONS_RUNNER_STAGING_VOLUME", "runner-volume")
	t.Setenv("FUNCTIONS_RUNNER_METRICS_ADDR", "127.0.0.1:9191")
	t.Setenv("AGENT_RUNNER_ENABLED", "true")
	t.Setenv("AGENT_RUNNER_EXECUTION_TIMEOUT", "4m")

	settings, err := loadExecutionSettings()
	if err != nil {
		t.Fatalf("loadExecutionSettings returned error: %v", err)
	}
	if settings.functionsMaxArtifactSize != 8<<20 || settings.functionsDefaultQuotaBytes != 64<<20 {
		t.Fatalf("unexpected function limits: %+v", settings)
	}
	if settings.functionsRunnerEnabled || settings.functionsWorkerID != "worker.test" {
		t.Fatalf("unexpected worker identity settings: %+v", settings)
	}
	if settings.functionsRunnerPoll != 2*time.Second || settings.functionsRunnerBuildTimeout != 3*time.Minute {
		t.Fatalf("unexpected worker timing settings: %+v", settings)
	}
	if !settings.agentRunnerEnabled || settings.agentRunnerExecutionTimeout != 4*time.Minute {
		t.Fatalf("unexpected agent settings: %+v", settings)
	}
}

func TestLoadExecutionSettingsRejectsInvalidRunnerImage(t *testing.T) {
	t.Setenv("FUNCTIONS_RUNNER_NODE_IMAGE", "node image")

	if _, err := loadExecutionSettings(); err == nil {
		t.Fatal("loadExecutionSettings accepted an invalid runner image")
	}
}

func TestExecutionDefaultsPreserveStorageDerivedLimits(t *testing.T) {
	config := Config{StorageMaxFileSize: 8 << 20, StorageDefaultQuotaBytes: 64 << 20}
	config.applyExecutionDefaults()

	if config.FunctionsMaxArtifactSize != 8<<20 || config.FunctionsDefaultQuotaBytes != 64<<20 {
		t.Fatalf("execution defaults = %+v, want storage-derived limits", config)
	}
}
