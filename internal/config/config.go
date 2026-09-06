package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	RedisURL    string
	HTTPAddress string
	// ACME terminates HTTPS for verified Site custom domains when enabled. The
	// default keeps certificate issuance off for local development; production
	// deployments must opt in explicitly and persist ACMECertCacheDir.
	ACMEEnabled              bool
	ACMEEmail                string
	ACMEDirectoryURL         string
	ACMETLSAddress           string
	ACMEHTTPChallengeAddress string
	ACMECertCacheDir         string
	SessionCookieName        string
	SessionTTL               time.Duration
	AppSessionTTL            time.Duration
	AuthVerificationTTL      time.Duration
	AuthPasswordResetTTL     time.Duration
	PublicAppURL             string
	// ConsoleCORSOrigins is the explicit allowlist for the browser-hosted
	// management console. Project application origins remain tenant-scoped and
	// are handled by httpapi's project CORS policy.
	ConsoleCORSOrigins       []string
	EmailDeliveryMode        string
	SMTPHost                 string
	SMTPPort                 int
	SMTPUsername             string
	SMTPPassword             string
	SMTPFrom                 string
	CookieSecure             bool
	AuthRateLimit            int
	AuthRateWindow           time.Duration
	StorageRoot              string
	StorageMaxFileSize       int64
	StorageDefaultQuotaBytes int64
	StorageDriver            string
	StorageS3Endpoint        string
	StorageS3Region          string
	StorageS3Bucket          string
	StorageS3AccessKey       string
	StorageS3SecretKey       string
	StorageS3UseSSL          bool
	StorageS3PathStyle       bool
	StorageS3Prefix          string
	StorageS3StagingRoot     string
	// Functions source archives use a separate child store under StorageRoot.
	// The global storage values are used as fallbacks for older deployments.
	FunctionsMaxArtifactSize      int64
	FunctionsDefaultQuotaBytes    int64
	FunctionsSecretKey            []byte
	FunctionsRunnerEnabled        bool
	FunctionsWorkerID             string
	FunctionsRunnerPoll           time.Duration
	FunctionsRunnerLeaseAge       time.Duration
	FunctionsRunnerBuildTimeout   time.Duration
	FunctionsRunnerStagingRoot    string
	FunctionsRunnerStagingVolume  string
	FunctionsRunnerMetricsAddress string
	FunctionsRunnerHelperImage    string
	FunctionsRunnerNodeImage      string
	FunctionsRunnerPythonImage    string
	FunctionsRunnerGoImage        string
	// Agent runner settings control the trusted queue lifecycle. Provider
	// adapters remain a separate capability and an empty registry never claims
	// queued runs.
	AgentRunnerEnabled          bool
	AgentRunnerExecutionTimeout time.Duration
	// Sites accept pre-built static archives. The compressed upload limit is
	// separate from the expanded publication limit because quota accounting is
	// based on bytes that are actually served from the immutable directory.
	SitesMaxArtifactSize     int64
	SitesDefaultQuotaBytes   int64
	SitesMaxExpandedBytes    int64
	SitesMaxFiles            int
	SitesGitFetchConcurrency int
	// OpenTelemetry tracing is disabled when the OTLP endpoint is empty. The
	// API and worker still create no-op spans in that mode, so instrumentation
	// does not need feature flags or test-only branches.
	TelemetryOTLPEndpoint string
	TelemetryServiceName  string
	TelemetrySampleRatio  float64
	// AgentProviderCatalog contains non-secret provider/model metadata for the
	// Console. Agent execution remains queue-only until a trusted provider
	// worker is deployed.
	AgentProviderCatalog []AgentProviderCatalogItem
}

func Load() (Config, error) {
	ttl, err := time.ParseDuration(value("SESSION_TTL", "720h"))
	if err != nil || ttl <= 0 {
		return Config{}, fmt.Errorf("SESSION_TTL must be a positive duration")
	}
	appSessionTTL, err := time.ParseDuration(value("APP_SESSION_TTL", "720h"))
	if err != nil || appSessionTTL <= 0 || appSessionTTL > 720*time.Hour {
		return Config{}, fmt.Errorf("APP_SESSION_TTL must be a positive duration no longer than 720h")
	}
	verificationTTL, err := time.ParseDuration(value("AUTH_VERIFICATION_TTL", "24h"))
	if err != nil || verificationTTL <= 0 || verificationTTL > 7*24*time.Hour {
		return Config{}, fmt.Errorf("AUTH_VERIFICATION_TTL must be a positive duration no longer than 168h")
	}
	passwordResetTTL, err := time.ParseDuration(value("AUTH_PASSWORD_RESET_TTL", "1h"))
	if err != nil || passwordResetTTL <= 0 || passwordResetTTL > 24*time.Hour {
		return Config{}, fmt.Errorf("AUTH_PASSWORD_RESET_TTL must be a positive duration no longer than 24h")
	}
	publicAppURL := value("PUBLIC_APP_URL", "http://localhost:4173")
	if !isPublicAppURL(publicAppURL) {
		return Config{}, fmt.Errorf("PUBLIC_APP_URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	consoleCORSOrigins, err := parseConsoleCORSOrigins(os.Getenv("CONSOLE_CORS_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	emailDeliveryMode := strings.ToLower(value("EMAIL_DELIVERY_MODE", "disabled"))
	if emailDeliveryMode != "disabled" && emailDeliveryMode != "log" && emailDeliveryMode != "smtp" {
		return Config{}, fmt.Errorf("EMAIL_DELIVERY_MODE must be disabled, log, or smtp")
	}
	smtpHost := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	smtpPort, err := strconv.Atoi(value("SMTP_PORT", "587"))
	if err != nil || smtpPort < 1 || smtpPort > 65535 {
		return Config{}, fmt.Errorf("SMTP_PORT must be an integer between 1 and 65535")
	}
	smtpFrom := strings.TrimSpace(os.Getenv("SMTP_FROM"))
	if emailDeliveryMode == "smtp" && (smtpHost == "" || !isACMEEmail(smtpFrom)) {
		return Config{}, fmt.Errorf("SMTP_HOST and SMTP_FROM must be configured when EMAIL_DELIVERY_MODE is smtp")
	}
	secure, err := strconv.ParseBool(value("COOKIE_SECURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("COOKIE_SECURE must be true or false")
	}
	rateLimit, err := strconv.Atoi(value("AUTH_RATE_LIMIT", "10"))
	if err != nil || rateLimit < 1 || rateLimit > 1000 {
		return Config{}, fmt.Errorf("AUTH_RATE_LIMIT must be an integer between 1 and 1000")
	}
	rateWindow, err := time.ParseDuration(value("AUTH_RATE_WINDOW", "1m"))
	if err != nil || rateWindow <= 0 || rateWindow > time.Hour {
		return Config{}, fmt.Errorf("AUTH_RATE_WINDOW must be a positive duration no longer than 1h")
	}
	storageMaxFileSize, err := parseBytes(value("STORAGE_MAX_FILE_SIZE", "50MiB"))
	if err != nil || storageMaxFileSize < 1 {
		return Config{}, fmt.Errorf("STORAGE_MAX_FILE_SIZE must be a positive byte quantity")
	}
	storageDefaultQuota, err := parseBytes(value("STORAGE_DEFAULT_QUOTA_BYTES", "1GiB"))
	if err != nil || storageDefaultQuota < 1 {
		return Config{}, fmt.Errorf("STORAGE_DEFAULT_QUOTA_BYTES must be a positive byte quantity")
	}
	functionsMaxArtifactSize, err := parseBytes(value("FUNCTIONS_MAX_ARTIFACT_SIZE", "50MiB"))
	if err != nil || functionsMaxArtifactSize < 1 {
		return Config{}, fmt.Errorf("FUNCTIONS_MAX_ARTIFACT_SIZE must be a positive byte quantity")
	}
	functionsDefaultQuota, err := parseBytes(value("FUNCTIONS_DEFAULT_QUOTA_BYTES", "1GiB"))
	if err != nil || functionsDefaultQuota < 1 {
		return Config{}, fmt.Errorf("FUNCTIONS_DEFAULT_QUOTA_BYTES must be a positive byte quantity")
	}
	sitesMaxArtifactSize, err := parseBytes(value("SITES_MAX_ARTIFACT_SIZE", "50MiB"))
	if err != nil || sitesMaxArtifactSize < 1 {
		return Config{}, fmt.Errorf("SITES_MAX_ARTIFACT_SIZE must be a positive byte quantity")
	}
	sitesDefaultQuota, err := parseBytes(value("SITES_DEFAULT_QUOTA_BYTES", "1GiB"))
	if err != nil || sitesDefaultQuota < 1 {
		return Config{}, fmt.Errorf("SITES_DEFAULT_QUOTA_BYTES must be a positive byte quantity")
	}
	sitesMaxExpanded, err := parseBytes(value("SITES_MAX_EXPANDED_BYTES", "256MiB"))
	if err != nil || sitesMaxExpanded < 1 {
		return Config{}, fmt.Errorf("SITES_MAX_EXPANDED_BYTES must be a positive byte quantity")
	}
	sitesMaxFiles, err := strconv.Atoi(value("SITES_MAX_FILES", "4096"))
	if err != nil || sitesMaxFiles < 1 || sitesMaxFiles > 100000 {
		return Config{}, fmt.Errorf("SITES_MAX_FILES must be an integer between 1 and 100000")
	}
	sitesGitFetchConcurrency, err := strconv.Atoi(value("SITES_GIT_FETCH_CONCURRENCY", "4"))
	if err != nil || sitesGitFetchConcurrency < 1 || sitesGitFetchConcurrency > 32 {
		return Config{}, fmt.Errorf("SITES_GIT_FETCH_CONCURRENCY must be an integer between 1 and 32")
	}
	runnerEnabled, err := strconv.ParseBool(value("FUNCTIONS_RUNNER_ENABLED", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_ENABLED must be true or false")
	}
	runnerPoll, err := time.ParseDuration(value("FUNCTIONS_RUNNER_POLL", "500ms"))
	if err != nil || runnerPoll <= 0 || runnerPoll > time.Minute {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_POLL must be a positive duration no longer than 1m")
	}
	runnerLeaseAge, err := time.ParseDuration(value("FUNCTIONS_RUNNER_LEASE_AGE", "20m"))
	if err != nil || runnerLeaseAge < time.Minute || runnerLeaseAge > 24*time.Hour {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_LEASE_AGE must be between 1m and 24h")
	}
	runnerBuildTimeout, err := time.ParseDuration(value("FUNCTIONS_RUNNER_BUILD_TIMEOUT", "15m"))
	if err != nil || runnerBuildTimeout < time.Minute || runnerBuildTimeout > 24*time.Hour {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_BUILD_TIMEOUT must be between 1m and 24h")
	}
	agentRunnerEnabled, err := strconv.ParseBool(value("AGENT_RUNNER_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("AGENT_RUNNER_ENABLED must be true or false")
	}
	agentRunnerExecutionTimeout, err := time.ParseDuration(value("AGENT_RUNNER_EXECUTION_TIMEOUT", "15m"))
	if err != nil || agentRunnerExecutionTimeout < time.Minute || agentRunnerExecutionTimeout > 24*time.Hour {
		return Config{}, fmt.Errorf("AGENT_RUNNER_EXECUTION_TIMEOUT must be between 1m and 24h")
	}
	workerID := value("FUNCTIONS_WORKER_ID", "")
	if workerID == "" {
		workerID, _ = os.Hostname()
	}
	if workerID == "" {
		workerID = "stealth-worker"
	}
	if len(workerID) > 128 || !isWorkerID(workerID) {
		return Config{}, fmt.Errorf("FUNCTIONS_WORKER_ID must contain only letters, numbers, dots, underscores, or hyphens")
	}
	stagingVolume := value("FUNCTIONS_RUNNER_STAGING_VOLUME", "stealth-function-runner-staging")
	if len(stagingVolume) > 255 || !isDockerName(stagingVolume) {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_STAGING_VOLUME must be a valid Docker volume name")
	}
	workerMetricsAddress := value("FUNCTIONS_RUNNER_METRICS_ADDR", ":9091")
	if !isListenAddress(workerMetricsAddress) {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_METRICS_ADDR must be a TCP host:port with a port between 1 and 65535")
	}
	telemetryEndpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if telemetryEndpoint != "" {
		parsed, parseErr := url.Parse(telemetryEndpoint)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Config{}, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must be an absolute HTTP(S) URL without query or fragment")
		}
	}
	telemetryServiceName := value("OTEL_SERVICE_NAME", "")
	telemetrySampleRatio, err := strconv.ParseFloat(value("OTEL_TRACES_SAMPLER_ARG", "0.1"), 64)
	if err != nil || telemetrySampleRatio < 0 || telemetrySampleRatio > 1 {
		return Config{}, fmt.Errorf("OTEL_TRACES_SAMPLER_ARG must be a number between 0 and 1")
	}
	agentProviderCatalog, err := parseAgentProviderCatalog(os.Getenv("AGENT_PROVIDER_CATALOG"))
	if err != nil {
		return Config{}, err
	}
	runnerImages := map[string]string{
		"FUNCTIONS_RUNNER_HELPER_IMAGE": value("FUNCTIONS_RUNNER_HELPER_IMAGE", "alpine:3.22"),
		"FUNCTIONS_RUNNER_NODE_IMAGE":   value("FUNCTIONS_RUNNER_NODE_IMAGE", "node:22-alpine"),
		"FUNCTIONS_RUNNER_PYTHON_IMAGE": value("FUNCTIONS_RUNNER_PYTHON_IMAGE", "python:3.13-alpine"),
		"FUNCTIONS_RUNNER_GO_IMAGE":     value("FUNCTIONS_RUNNER_GO_IMAGE", "golang:1.24-alpine"),
	}
	for name, image := range runnerImages {
		if !isImageReference(image) {
			return Config{}, fmt.Errorf("%s must be a valid Docker image reference", name)
		}
	}
	var functionsSecretKey []byte
	if raw := strings.TrimSpace(os.Getenv("FUNCTIONS_SECRET_KEY")); raw != "" {
		functionsSecretKey, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			// Raw URL encoding is convenient for env files that avoid '='.
			functionsSecretKey, err = base64.RawURLEncoding.DecodeString(raw)
		}
		if err != nil || len(functionsSecretKey) != 32 {
			return Config{}, fmt.Errorf("FUNCTIONS_SECRET_KEY must be base64-encoded 32 bytes")
		}
	}
	storageRoot := value("STORAGE_ROOT", "/var/lib/stealth/storage")
	storageRoot, err = filepath.Abs(storageRoot)
	if err != nil || strings.TrimSpace(storageRoot) == "" {
		return Config{}, fmt.Errorf("STORAGE_ROOT must be a valid filesystem path")
	}
	storageDriver := strings.ToLower(value("STORAGE_DRIVER", "local"))
	if storageDriver != "local" && storageDriver != "s3" {
		return Config{}, fmt.Errorf("STORAGE_DRIVER must be local or s3")
	}
	storageS3Endpoint := strings.TrimSpace(os.Getenv("STORAGE_S3_ENDPOINT"))
	storageS3Region := value("STORAGE_S3_REGION", "us-east-1")
	storageS3Bucket := strings.TrimSpace(os.Getenv("STORAGE_S3_BUCKET"))
	storageS3AccessKey := strings.TrimSpace(os.Getenv("STORAGE_S3_ACCESS_KEY"))
	storageS3SecretKey := os.Getenv("STORAGE_S3_SECRET_KEY")
	storageS3UseSSL, err := strconv.ParseBool(value("STORAGE_S3_USE_SSL", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("STORAGE_S3_USE_SSL must be true or false")
	}
	storageS3PathStyle, err := strconv.ParseBool(value("STORAGE_S3_PATH_STYLE", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("STORAGE_S3_PATH_STYLE must be true or false")
	}
	storageS3Prefix := strings.Trim(strings.TrimSpace(os.Getenv("STORAGE_S3_PREFIX")), "/")
	if len(storageS3Prefix) > 512 || strings.ContainsAny(storageS3Prefix, "\\\r\n\x00") {
		return Config{}, fmt.Errorf("STORAGE_S3_PREFIX must be a safe object prefix")
	}
	if storageDriver == "s3" {
		if storageS3Endpoint == "" || storageS3Bucket == "" || storageS3AccessKey == "" || storageS3SecretKey == "" {
			return Config{}, fmt.Errorf("STORAGE_S3_ENDPOINT, STORAGE_S3_BUCKET, STORAGE_S3_ACCESS_KEY, and STORAGE_S3_SECRET_KEY are required when STORAGE_DRIVER is s3")
		}
		if !isStorageS3Endpoint(storageS3Endpoint) {
			return Config{}, fmt.Errorf("STORAGE_S3_ENDPOINT must be an HTTP(S) endpoint without path or credentials")
		}
	}
	storageS3StagingRoot, err := filepath.Abs(value("STORAGE_S3_STAGING_ROOT", filepath.Join(storageRoot, "s3-staging")))
	if err != nil || strings.TrimSpace(storageS3StagingRoot) == "" || storageS3StagingRoot == string(filepath.Separator) {
		return Config{}, fmt.Errorf("STORAGE_S3_STAGING_ROOT must be a valid non-root filesystem path")
	}
	acmeEnabled, err := strconv.ParseBool(value("ACME_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("ACME_ENABLED must be true or false")
	}
	acmeDirectoryURL := value("ACME_DIRECTORY_URL", "https://acme-v02.api.letsencrypt.org/directory")
	if !isACMEDirectoryURL(acmeDirectoryURL) {
		return Config{}, fmt.Errorf("ACME_DIRECTORY_URL must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	acmeTLSAddress := value("ACME_TLS_ADDR", ":8443")
	if !isListenAddress(acmeTLSAddress) {
		return Config{}, fmt.Errorf("ACME_TLS_ADDR must be a TCP host:port with a port between 1 and 65535")
	}
	acmeHTTPChallengeAddress := value("ACME_HTTP_CHALLENGE_ADDR", ":8081")
	if !isListenAddress(acmeHTTPChallengeAddress) {
		return Config{}, fmt.Errorf("ACME_HTTP_CHALLENGE_ADDR must be a TCP host:port with a port between 1 and 65535")
	}
	acmeEmail := strings.TrimSpace(os.Getenv("ACME_EMAIL"))
	acmeCertCacheDir := value("ACME_CERT_CACHE_DIR", filepath.Join(storageRoot, "acme"))
	acmeCertCacheDir, err = filepath.Abs(acmeCertCacheDir)
	if err != nil || strings.TrimSpace(acmeCertCacheDir) == "" || acmeCertCacheDir == string(filepath.Separator) {
		return Config{}, fmt.Errorf("ACME_CERT_CACHE_DIR must be a valid non-root filesystem path")
	}
	if acmeEnabled && !isACMEEmail(acmeEmail) {
		return Config{}, fmt.Errorf("ACME_EMAIL must be a valid email address when ACME_ENABLED is true")
	}
	if acmeEnabled && acmeTLSAddress == acmeHTTPChallengeAddress {
		return Config{}, fmt.Errorf("ACME_TLS_ADDR and ACME_HTTP_CHALLENGE_ADDR must be different listeners")
	}
	if acmeEnabled {
		httpAddress := value("HTTP_ADDR", ":8080")
		if sameListenPort(acmeTLSAddress, httpAddress) || sameListenPort(acmeHTTPChallengeAddress, httpAddress) {
			return Config{}, fmt.Errorf("ACME listeners must not reuse the HTTP_ADDR port")
		}
	}
	config := Config{
		DatabaseURL:                   strings.TrimSpace(os.Getenv("DATABASE_URL")),
		RedisURL:                      value("REDIS_URL", "redis://127.0.0.1:6379/0"),
		HTTPAddress:                   value("HTTP_ADDR", ":8080"),
		ACMEEnabled:                   acmeEnabled,
		ACMEEmail:                     acmeEmail,
		ACMEDirectoryURL:              acmeDirectoryURL,
		ACMETLSAddress:                acmeTLSAddress,
		ACMEHTTPChallengeAddress:      acmeHTTPChallengeAddress,
		ACMECertCacheDir:              filepath.Clean(acmeCertCacheDir),
		SessionCookieName:             value("SESSION_COOKIE_NAME", "stealth_session"),
		SessionTTL:                    ttl,
		AppSessionTTL:                 appSessionTTL,
		AuthVerificationTTL:           verificationTTL,
		AuthPasswordResetTTL:          passwordResetTTL,
		PublicAppURL:                  strings.TrimRight(publicAppURL, "/"),
		ConsoleCORSOrigins:            consoleCORSOrigins,
		EmailDeliveryMode:             emailDeliveryMode,
		SMTPHost:                      smtpHost,
		SMTPPort:                      smtpPort,
		SMTPUsername:                  strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword:                  os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:                      smtpFrom,
		CookieSecure:                  secure,
		AuthRateLimit:                 rateLimit,
		AuthRateWindow:                rateWindow,
		StorageRoot:                   filepath.Clean(storageRoot),
		StorageMaxFileSize:            storageMaxFileSize,
		StorageDefaultQuotaBytes:      storageDefaultQuota,
		StorageDriver:                 storageDriver,
		StorageS3Endpoint:             storageS3Endpoint,
		StorageS3Region:               storageS3Region,
		StorageS3Bucket:               storageS3Bucket,
		StorageS3AccessKey:            storageS3AccessKey,
		StorageS3SecretKey:            storageS3SecretKey,
		StorageS3UseSSL:               storageS3UseSSL,
		StorageS3PathStyle:            storageS3PathStyle,
		StorageS3Prefix:               storageS3Prefix,
		StorageS3StagingRoot:          filepath.Clean(storageS3StagingRoot),
		FunctionsMaxArtifactSize:      functionsMaxArtifactSize,
		FunctionsDefaultQuotaBytes:    functionsDefaultQuota,
		FunctionsSecretKey:            functionsSecretKey,
		FunctionsRunnerEnabled:        runnerEnabled,
		FunctionsWorkerID:             workerID,
		FunctionsRunnerPoll:           runnerPoll,
		FunctionsRunnerLeaseAge:       runnerLeaseAge,
		FunctionsRunnerBuildTimeout:   runnerBuildTimeout,
		FunctionsRunnerStagingRoot:    filepath.Clean(value("FUNCTIONS_RUNNER_STAGING_ROOT", "/var/lib/stealth/runner-staging")),
		FunctionsRunnerStagingVolume:  stagingVolume,
		FunctionsRunnerMetricsAddress: workerMetricsAddress,
		FunctionsRunnerHelperImage:    runnerImages["FUNCTIONS_RUNNER_HELPER_IMAGE"],
		FunctionsRunnerNodeImage:      runnerImages["FUNCTIONS_RUNNER_NODE_IMAGE"],
		FunctionsRunnerPythonImage:    runnerImages["FUNCTIONS_RUNNER_PYTHON_IMAGE"],
		FunctionsRunnerGoImage:        runnerImages["FUNCTIONS_RUNNER_GO_IMAGE"],
		AgentRunnerEnabled:            agentRunnerEnabled,
		AgentRunnerExecutionTimeout:   agentRunnerExecutionTimeout,
		SitesMaxArtifactSize:          sitesMaxArtifactSize,
		SitesDefaultQuotaBytes:        sitesDefaultQuota,
		SitesMaxExpandedBytes:         sitesMaxExpanded,
		SitesMaxFiles:                 sitesMaxFiles,
		SitesGitFetchConcurrency:      sitesGitFetchConcurrency,
		TelemetryOTLPEndpoint:         telemetryEndpoint,
		TelemetryServiceName:          telemetryServiceName,
		TelemetrySampleRatio:          telemetrySampleRatio,
		AgentProviderCatalog:          agentProviderCatalog,
	}
	config.FunctionsRunnerStagingRoot, err = filepath.Abs(config.FunctionsRunnerStagingRoot)
	if err != nil || strings.TrimSpace(config.FunctionsRunnerStagingRoot) == "" {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_STAGING_ROOT must be a valid filesystem path")
	}
	if config.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return config, nil
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

func (c Config) ValidateStorage() error {
	if c.StorageDriver != "local" && c.StorageDriver != "s3" {
		return fmt.Errorf("storage driver must be local or s3")
	}
	if c.StorageMaxFileSize <= 0 || c.StorageDefaultQuotaBytes <= 0 {
		return fmt.Errorf("storage size and quota settings are invalid")
	}
	if c.StorageDriver == "s3" {
		if strings.TrimSpace(c.StorageS3Endpoint) == "" || strings.TrimSpace(c.StorageS3Bucket) == "" || strings.TrimSpace(c.StorageS3AccessKey) == "" || c.StorageS3SecretKey == "" {
			return fmt.Errorf("S3 storage credentials and bucket are required")
		}
		if !isStorageS3Endpoint(c.StorageS3Endpoint) {
			return fmt.Errorf("S3 storage endpoint is invalid")
		}
		if strings.TrimSpace(c.StorageS3StagingRoot) == "" || !filepath.IsAbs(c.StorageS3StagingRoot) || filepath.Clean(c.StorageS3StagingRoot) == string(filepath.Separator) {
			return fmt.Errorf("S3 storage staging root is invalid")
		}
	}
	return nil
}

// ValidateSites keeps production deployments from silently accepting a
// malformed static publication configuration. NewWithLimiter still supplies
// safe defaults for hand-built test Config values.
func (c Config) ValidateSites() error {
	if c.SitesMaxArtifactSize <= 0 || c.SitesMaxExpandedBytes <= 0 || c.SitesDefaultQuotaBytes <= 0 || c.SitesMaxFiles < 1 || c.SitesMaxFiles > 100000 || c.SitesGitFetchConcurrency < 0 || c.SitesGitFetchConcurrency > 32 {
		return fmt.Errorf("site artifact, expanded-size, file-count, and quota settings are invalid")
	}
	if c.SitesMaxExpandedBytes > c.SitesDefaultQuotaBytes {
		return fmt.Errorf("SITES_MAX_EXPANDED_BYTES cannot exceed SITES_DEFAULT_QUOTA_BYTES")
	}
	if c.ACMEEnabled {
		if !isACMEEmail(strings.TrimSpace(c.ACMEEmail)) {
			return fmt.Errorf("ACME_EMAIL must be a valid email address when ACME_ENABLED is true")
		}
		if !isACMEDirectoryURL(c.ACMEDirectoryURL) {
			return fmt.Errorf("ACME_DIRECTORY_URL must be an absolute HTTPS URL without credentials, query, or fragment")
		}
		if !isListenAddress(c.ACMETLSAddress) || !isListenAddress(c.ACMEHTTPChallengeAddress) {
			return fmt.Errorf("ACME listener addresses are invalid")
		}
		if strings.TrimSpace(c.ACMETLSAddress) == strings.TrimSpace(c.ACMEHTTPChallengeAddress) {
			return fmt.Errorf("ACME_TLS_ADDR and ACME_HTTP_CHALLENGE_ADDR must be different listeners")
		}
		if sameListenPort(c.ACMETLSAddress, c.HTTPAddress) || sameListenPort(c.ACMEHTTPChallengeAddress, c.HTTPAddress) {
			return fmt.Errorf("ACME listeners must not reuse the HTTP_ADDR port")
		}
		if strings.TrimSpace(c.ACMECertCacheDir) == "" || !filepath.IsAbs(c.ACMECertCacheDir) || filepath.Clean(c.ACMECertCacheDir) == string(filepath.Separator) {
			return fmt.Errorf("ACME_CERT_CACHE_DIR must be a valid non-root filesystem path")
		}
	}
	return nil
}

func value(key, fallback string) string {
	if got := strings.TrimSpace(os.Getenv(key)); got != "" {
		return got
	}
	return fallback
}

func isWorkerID(value string) bool {
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return value != ""
}

func isStorageS3Endpoint(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return false
	}
	endpointURL := raw
	if !strings.Contains(raw, "://") {
		endpointURL = "http://" + raw
	}
	parsed, err := url.Parse(endpointURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return false
	}
	return true
}

func isDockerName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			if index == 0 && (character == '.' || character == '_' || character == '-') {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func isImageReference(value string) bool {
	if len(value) < 1 || len(value) > 255 || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || strings.ContainsRune("/._:@-", character) {
			continue
		}
		return false
	}
	return true
}

func isListenAddress(value string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || strings.ContainsAny(host, "\r\n") {
		return false
	}
	parsed, err := strconv.Atoi(port)
	return err == nil && parsed >= 1 && parsed <= 65535
}

func sameListenPort(first, second string) bool {
	_, firstPort, firstErr := net.SplitHostPort(strings.TrimSpace(first))
	_, secondPort, secondErr := net.SplitHostPort(strings.TrimSpace(second))
	return firstErr == nil && secondErr == nil && firstPort == secondPort
}

func isACMEDirectoryURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\x00\r\n \t") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return false
		}
	}
	return true
}

func isACMEEmail(value string) bool {
	if value == "" || len([]byte(value)) > 320 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && strings.Contains(value, "@")
}

func isPublicAppURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\x00\r\n \t") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return false
		}
	}
	return true
}

// parseConsoleCORSOrigins parses a comma-separated, exact-origin allowlist.
// It deliberately does not fall back to PUBLIC_APP_URL: that value may point
// at an email route (and a missing allowlist must fail closed for browsers).
func parseConsoleCORSOrigins(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 32 {
		return nil, fmt.Errorf("CONSOLE_CORS_ORIGINS must contain at most 32 origins")
	}
	seen := make(map[string]struct{}, len(parts))
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		origin, err := normalizeConsoleOrigin(part)
		if err != nil {
			return nil, fmt.Errorf("CONSOLE_CORS_ORIGINS contains an invalid origin")
		}
		if _, exists := seen[origin]; exists {
			return nil, fmt.Errorf("CONSOLE_CORS_ORIGINS contains a duplicate origin")
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins, nil
}

func normalizeConsoleOrigin(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", fmt.Errorf("invalid origin")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid origin")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid origin")
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.ContainsAny(hostname, "*%/") {
		return "", fmt.Errorf("invalid origin")
	}
	if port := parsed.Port(); port != "" {
		parsedPort, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", fmt.Errorf("invalid origin")
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Host)
	if (scheme == "http" && parsed.Port() == "80") || (scheme == "https" && parsed.Port() == "443") {
		host = strings.TrimSuffix(host, ":"+parsed.Port())
	}
	return scheme + "://" + host, nil
}

// parseBytes accepts plain bytes and binary IEC suffixes. Keeping this parser
// in config makes deployment values explicit while retaining an integer value
// for quota accounting in PostgreSQL.
func parseBytes(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty byte quantity")
	}
	units := []struct {
		suffix string
		value  int64
	}{{"tib", 1 << 40}, {"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10}, {"b", 1}}
	lower := strings.ToLower(raw)
	multiplier := int64(1)
	number := lower
	for _, unit := range units {
		if strings.HasSuffix(lower, unit.suffix) {
			multiplier = unit.value
			number = strings.TrimSpace(lower[:len(lower)-len(unit.suffix)])
			break
		}
	}
	parsed, err := strconv.ParseInt(number, 10, 64)
	if err != nil || parsed < 1 || parsed > (1<<63-1)/multiplier {
		return 0, fmt.Errorf("invalid byte quantity")
	}
	return parsed * multiplier, nil
}
