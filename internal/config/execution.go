package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// executionSettings owns environment parsing for the function and agent
// execution runtimes. Config remains the validated application snapshot, but
// the composition root no longer needs to know how each execution setting is
// parsed or constrained.
type executionSettings struct {
	functionsMaxArtifactSize      int64
	functionsDefaultQuotaBytes    int64
	functionsRunnerEnabled        bool
	functionsWorkerID             string
	functionsRunnerPoll           time.Duration
	functionsRunnerLeaseAge       time.Duration
	functionsRunnerBuildTimeout   time.Duration
	functionsRunnerStagingRoot    string
	functionsRunnerStagingVolume  string
	functionsRunnerMetricsAddress string
	functionsRunnerHelperImage    string
	functionsRunnerNodeImage      string
	functionsRunnerPythonImage    string
	functionsRunnerGoImage        string
	agentRunnerEnabled            bool
	agentRunnerExecutionTimeout   time.Duration
}

func loadExecutionSettings() (executionSettings, error) {
	functionsMaxArtifactSize, err := parseBytes(value("FUNCTIONS_MAX_ARTIFACT_SIZE", "50MiB"))
	if err != nil || functionsMaxArtifactSize < 1 {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_MAX_ARTIFACT_SIZE must be a positive byte quantity")
	}
	functionsDefaultQuotaBytes, err := parseBytes(value("FUNCTIONS_DEFAULT_QUOTA_BYTES", "1GiB"))
	if err != nil || functionsDefaultQuotaBytes < 1 {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_DEFAULT_QUOTA_BYTES must be a positive byte quantity")
	}
	functionsRunnerEnabled, err := strconv.ParseBool(value("FUNCTIONS_RUNNER_ENABLED", "true"))
	if err != nil {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_RUNNER_ENABLED must be true or false")
	}
	functionsRunnerPoll, err := time.ParseDuration(value("FUNCTIONS_RUNNER_POLL", "500ms"))
	if err != nil || functionsRunnerPoll <= 0 || functionsRunnerPoll > time.Minute {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_RUNNER_POLL must be a positive duration no longer than 1m")
	}
	functionsRunnerLeaseAge, err := time.ParseDuration(value("FUNCTIONS_RUNNER_LEASE_AGE", "20m"))
	if err != nil || functionsRunnerLeaseAge < time.Minute || functionsRunnerLeaseAge > 24*time.Hour {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_RUNNER_LEASE_AGE must be between 1m and 24h")
	}
	functionsRunnerBuildTimeout, err := time.ParseDuration(value("FUNCTIONS_RUNNER_BUILD_TIMEOUT", "15m"))
	if err != nil || functionsRunnerBuildTimeout < time.Minute || functionsRunnerBuildTimeout > 24*time.Hour {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_RUNNER_BUILD_TIMEOUT must be between 1m and 24h")
	}
	agentRunnerEnabled, err := strconv.ParseBool(value("AGENT_RUNNER_ENABLED", "false"))
	if err != nil {
		return executionSettings{}, fmt.Errorf("AGENT_RUNNER_ENABLED must be true or false")
	}
	agentRunnerExecutionTimeout, err := time.ParseDuration(value("AGENT_RUNNER_EXECUTION_TIMEOUT", "15m"))
	if err != nil || agentRunnerExecutionTimeout < time.Minute || agentRunnerExecutionTimeout > 24*time.Hour {
		return executionSettings{}, fmt.Errorf("AGENT_RUNNER_EXECUTION_TIMEOUT must be between 1m and 24h")
	}
	workerID := value("FUNCTIONS_WORKER_ID", "")
	if workerID == "" {
		workerID, _ = os.Hostname()
	}
	if workerID == "" {
		workerID = "stealth-worker"
	}
	if len(workerID) > 128 || !isWorkerID(workerID) {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_WORKER_ID must contain only letters, numbers, dots, underscores, or hyphens")
	}
	stagingVolume := value("FUNCTIONS_RUNNER_STAGING_VOLUME", "stealth-function-runner-staging")
	if len(stagingVolume) > 255 || !isDockerName(stagingVolume) {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_RUNNER_STAGING_VOLUME must be a valid Docker volume name")
	}
	metricsAddress := value("FUNCTIONS_RUNNER_METRICS_ADDR", "127.0.0.1:9091")
	if !isListenAddress(metricsAddress) {
		return executionSettings{}, fmt.Errorf("FUNCTIONS_RUNNER_METRICS_ADDR must be a TCP host:port with a port between 1 and 65535")
	}
	runnerImages := struct {
		helper string
		node   string
		python string
		golang string
	}{
		helper: value("FUNCTIONS_RUNNER_HELPER_IMAGE", "alpine:3.22"),
		node:   value("FUNCTIONS_RUNNER_NODE_IMAGE", "node:22-alpine"),
		python: value("FUNCTIONS_RUNNER_PYTHON_IMAGE", "python:3.13-alpine"),
		golang: value("FUNCTIONS_RUNNER_GO_IMAGE", "golang:1.24-alpine"),
	}
	images := []struct {
		name  string
		value string
	}{
		{name: "FUNCTIONS_RUNNER_HELPER_IMAGE", value: runnerImages.helper},
		{name: "FUNCTIONS_RUNNER_NODE_IMAGE", value: runnerImages.node},
		{name: "FUNCTIONS_RUNNER_PYTHON_IMAGE", value: runnerImages.python},
		{name: "FUNCTIONS_RUNNER_GO_IMAGE", value: runnerImages.golang},
	}
	for _, image := range images {
		if !isImageReference(image.value) {
			return executionSettings{}, fmt.Errorf("%s must be a valid Docker image reference", image.name)
		}
	}

	return executionSettings{
		functionsMaxArtifactSize:      functionsMaxArtifactSize,
		functionsDefaultQuotaBytes:    functionsDefaultQuotaBytes,
		functionsRunnerEnabled:        functionsRunnerEnabled,
		functionsWorkerID:             workerID,
		functionsRunnerPoll:           functionsRunnerPoll,
		functionsRunnerLeaseAge:       functionsRunnerLeaseAge,
		functionsRunnerBuildTimeout:   functionsRunnerBuildTimeout,
		functionsRunnerStagingRoot:    value("FUNCTIONS_RUNNER_STAGING_ROOT", "/var/lib/stealth/runner-staging"),
		functionsRunnerStagingVolume:  stagingVolume,
		functionsRunnerMetricsAddress: metricsAddress,
		functionsRunnerHelperImage:    runnerImages.helper,
		functionsRunnerNodeImage:      runnerImages.node,
		functionsRunnerPythonImage:    runnerImages.python,
		functionsRunnerGoImage:        runnerImages.golang,
		agentRunnerEnabled:            agentRunnerEnabled,
		agentRunnerExecutionTimeout:   agentRunnerExecutionTimeout,
	}, nil
}

func (s executionSettings) apply(c *Config) {
	c.FunctionsMaxArtifactSize = s.functionsMaxArtifactSize
	c.FunctionsDefaultQuotaBytes = s.functionsDefaultQuotaBytes
	c.FunctionsRunnerEnabled = s.functionsRunnerEnabled
	c.FunctionsWorkerID = s.functionsWorkerID
	c.FunctionsRunnerPoll = s.functionsRunnerPoll
	c.FunctionsRunnerLeaseAge = s.functionsRunnerLeaseAge
	c.FunctionsRunnerBuildTimeout = s.functionsRunnerBuildTimeout
	c.FunctionsRunnerStagingRoot = filepath.Clean(s.functionsRunnerStagingRoot)
	c.FunctionsRunnerStagingVolume = s.functionsRunnerStagingVolume
	c.FunctionsRunnerMetricsAddress = s.functionsRunnerMetricsAddress
	c.FunctionsRunnerHelperImage = s.functionsRunnerHelperImage
	c.FunctionsRunnerNodeImage = s.functionsRunnerNodeImage
	c.FunctionsRunnerPythonImage = s.functionsRunnerPythonImage
	c.FunctionsRunnerGoImage = s.functionsRunnerGoImage
	c.AgentRunnerEnabled = s.agentRunnerEnabled
	c.AgentRunnerExecutionTimeout = s.agentRunnerExecutionTimeout
}

func (c *Config) applyExecutionDefaults() {
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
}

// ValidateFunctions enforces the production credential gate. Load keeps the
// key optional for tests and older hand-built Config values; the production
// entrypoint calls this method before serving traffic. In all cases the HTTP
// readiness endpoint also fails closed when the key is absent.
func (c Config) ValidateFunctions() error {
	if len(c.FunctionsSecretKey) != 32 {
		return fmt.Errorf("FUNCTIONS_SECRET_KEY must be configured as base64-encoded 32 bytes")
	}
	if c.FunctionsMaxArtifactSize <= 0 || c.FunctionsDefaultQuotaBytes <= 0 || c.FunctionsMaxArtifactSize > c.FunctionsDefaultQuotaBytes {
		return fmt.Errorf("function artifact size and quota settings are invalid")
	}
	return nil
}
