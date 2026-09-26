package config

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/secretkey"
)

type Config struct {
	DatabaseURL             string
	DatabaseMaxConns        int32
	DatabaseMinConns        int32
	DatabaseMaxConnLifetime time.Duration
	DatabaseMaxConnIdleTime time.Duration
	RedisURL                string
	HTTPAddress             string
	// PlatformSiteAddress is a private listener containing only the public
	// static Site-serving surface. Traefik platform routers never target the
	// control-plane HTTP listener.
	PlatformSiteAddress string
	MetricsToken        string
	// TrustedProxyCIDRs is empty by default. Forwarded client-IP headers are
	// only accepted when the direct peer belongs to one of these networks.
	TrustedProxyCIDRs []*net.IPNet
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
	ConsoleCORSOrigins         []string
	EmailDeliveryMode          string
	SMTPHost                   string
	SMTPPort                   int
	SMTPUsername               string
	SMTPPassword               string
	SMTPFrom                   string
	CookieSecure               bool
	AuthRateLimit              int
	AuthRateWindow             time.Duration
	ProjectOperationRateLimit  int
	ProjectOperationRateWindow time.Duration
	StorageRoot                string
	StorageMaxFileSize         int64
	StorageDefaultQuotaBytes   int64
	StorageDriver              string
	StorageS3Endpoint          string
	StorageS3Region            string
	StorageS3Bucket            string
	StorageS3AccessKey         string
	StorageS3SecretKey         string
	StorageS3UseSSL            bool
	StorageS3PathStyle         bool
	StorageS3Prefix            string
	StorageS3StagingRoot       string
	// Functions source archives use a separate child store under StorageRoot.
	// The global storage values are used as fallbacks for older deployments.
	FunctionsMaxArtifactSize   int64
	FunctionsDefaultQuotaBytes int64
	FunctionsSecretKey         []byte
	// AppsSecretKey is a dedicated operator key for App environment values.
	AppsSecretKey []byte
	// BootstrapCLIKey authenticates the local CLI when it asks the API to mint
	// a first-run setup session and encrypts short-lived GitHub authorization state.
	// It is a separate security domain from FunctionsSecretKey.
	BootstrapCLIKey                  []byte
	GitHubAppClientID                string
	FunctionsRunnerEnabled           bool
	FunctionsWorkerID                string
	FunctionsRunnerPoll              time.Duration
	FunctionsRunnerLeaseAge          time.Duration
	FunctionsRunnerBuildTimeout      time.Duration
	FunctionsRunnerStagingRoot       string
	FunctionsRunnerStagingVolume     string
	FunctionsRunnerMetricsAddress    string
	FunctionsRunnerHelperImage       string
	FunctionsRunnerNodeImage         string
	FunctionsRunnerPythonImage       string
	FunctionsRunnerGoImage           string
	AppsMaxSourceArchiveBytes        int64
	AppsMaxExpandedSourceBytes       int64
	AppsMaxSourceFiles               int
	AppsMaxImageArchiveBytes         int64
	AppsDefaultArtifactQuotaBytes    int64
	AppsBuildkitAddress              string
	AppsBuildkitCACert               string
	AppsBuildkitClientCert           string
	AppsBuildkitClientKey            string
	AppsBuildTimeout                 time.Duration
	AppsBuildLeaseAge                time.Duration
	AppsBuildPollInterval            time.Duration
	AppsBuildStagingRoot             string
	AppsBuildStagingVolume           string
	AppsBuildkitStateVolume          string
	AppsRuntimeNetworkName           string
	AppsRuntimePollInterval          time.Duration
	AppsRuntimeLeaseAge              time.Duration
	AppsRuntimeActionTimeout         time.Duration
	AppsRuntimeImageImportTimeout    time.Duration
	AppsRuntimeImageCacheMaxBytes    int64
	AppsRuntimeImageCacheTargetBytes int64
	AppsRuntimeImageGCSweepInterval  time.Duration
	// Agent runner settings control the trusted queue lifecycle. Provider
	// adapters remain a separate capability and an empty registry never claims
	// queued runs.
	AgentRunnerEnabled          bool
	AgentRunnerExecutionTimeout time.Duration
	// Sites accept pre-built static archives. The compressed upload limit is
	// separate from the expanded publication limit because quota accounting is
	// based on bytes that are actually served from the immutable directory.
	SitesMaxArtifactSize           int64
	SitesDefaultQuotaBytes         int64
	SitesMaxExpandedBytes          int64
	SitesMaxFiles                  int
	SitesGitFetchConcurrency       int
	TraefikGeneratedDir            string
	TraefikReloadFile              string
	PlatformRouteReconcileInterval time.Duration
	CloudflareReconcileInterval    time.Duration
	// OpenTelemetry tracing is disabled when the OTLP endpoint is empty. The
	// API and worker still create no-op spans in that mode, so instrumentation
	// does not need feature flags or test-only branches.
	TelemetryOTLPEndpoint string
	TelemetryServiceName  string
	TelemetrySampleRatio  float64
	// ClickHouse is an optional control-plane dependency. An empty address
	// keeps local and legacy installations fully functional while the admin
	// telemetry surface reports the backend as unavailable. When configured,
	// all query limits are enforced by the API and the ClickHouse session.
	TelemetryClickHouseAddr     string
	TelemetryClickHouseDatabase string
	TelemetryClickHouseUser     string
	TelemetryClickHousePassword string
	TelemetryCollectorHealthURL string
	TelemetryMaxQueryDuration   time.Duration
	TelemetryMaxQueryRange      time.Duration
	TelemetryMaxQueryRows       int
	TelemetryRetention          time.Duration
	// AgentProviderCatalog contains non-secret provider/model metadata for the
	// Console. Agent execution remains queue-only until a trusted provider
	// worker is deployed.
	AgentProviderCatalog []AgentProviderCatalogItem
	// SetupMode exposes only the short-lived browser installer routes. The host
	// CLI owns Docker and production installation; setup-mode API containers do
	// not receive Docker authority.
	SetupMode                   bool
	InstallRoot                 string
	SetupStateFile              string
	CloudflareImportFile        string
	SetupHandoffFile            string
	ProductionComposeFile       string
	SetupComposeFile            string
	CloudflareOAuthClientID     string
	CloudflareOAuthClientSecret string
	CloudflareAPIBaseURL        string
}

func Load() (Config, error) {
	databaseSettings, err := loadDatabaseSettings()
	if err != nil {
		return Config{}, err
	}
	transportSettings, err := loadTransportSettings()
	if err != nil {
		return Config{}, err
	}
	authSettings, err := loadAuthSettings()
	if err != nil {
		return Config{}, err
	}
	siteSettings, err := loadSiteSettings()
	if err != nil {
		return Config{}, err
	}
	ingressSettings, err := loadIngressSettings()
	if err != nil {
		return Config{}, err
	}
	executionSettings, err := loadExecutionSettings()
	if err != nil {
		return Config{}, err
	}
	appBuildSettings, err := loadAppBuildSettings()
	if err != nil {
		return Config{}, err
	}
	telemetrySettings, err := loadTelemetrySettings()
	if err != nil {
		return Config{}, err
	}
	telemetryStoreSettings, err := loadTelemetryStoreSettings()
	if err != nil {
		return Config{}, err
	}
	agentSettings, err := loadAgentSettings()
	if err != nil {
		return Config{}, err
	}
	secretSettings, err := loadSecretSettings()
	if err != nil {
		return Config{}, err
	}
	storageSettings, err := loadStorageSettings()
	if err != nil {
		return Config{}, err
	}
	tlsSettings, err := loadTLSSettings(storageSettings.root, transportSettings.httpAddress)
	if err != nil {
		return Config{}, err
	}
	setupSettings, err := loadSetupSettings()
	if err != nil {
		return Config{}, err
	}
	config := Config{}
	databaseSettings.apply(&config)
	authSettings.apply(&config)
	siteSettings.apply(&config)
	ingressSettings.apply(&config)
	executionSettings.apply(&config)
	appBuildSettings.apply(&config)
	telemetrySettings.apply(&config)
	telemetryStoreSettings.apply(&config)
	agentSettings.apply(&config)
	secretSettings.apply(&config)
	transportSettings.apply(&config)
	storageSettings.apply(&config)
	tlsSettings.apply(&config)
	setupSettings.apply(&config)
	config.FunctionsRunnerStagingRoot, err = filepath.Abs(config.FunctionsRunnerStagingRoot)
	if err != nil || strings.TrimSpace(config.FunctionsRunnerStagingRoot) == "" {
		return Config{}, fmt.Errorf("FUNCTIONS_RUNNER_STAGING_ROOT must be a valid filesystem path")
	}
	config.AppsBuildStagingRoot, err = filepath.Abs(config.AppsBuildStagingRoot)
	if err != nil || strings.TrimSpace(config.AppsBuildStagingRoot) == "" {
		return Config{}, fmt.Errorf("APPS_BUILD_STAGING_ROOT must be a valid filesystem path")
	}
	return config, nil
}

// boundedInt32 parses an operator configuration value with the exact width
// used by the downstream database driver. Parsing at 32 bits and checking the
// bounds before returning makes the narrowing conversion explicit and safe.
func boundedInt32(name, fallback string, minimum, maximum int32) (int32, error) {
	if minimum > maximum {
		return 0, fmt.Errorf("%s has invalid bounds", name)
	}
	parsed, err := strconv.ParseInt(value(name, fallback), 10, 32)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return int32(parsed), nil
}

func decodeSecretKey(raw, name string) ([]byte, error) {
	key, err := secretkey.Decode32ByteKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64-encoded 32 bytes", name)
	}
	return key, nil
}

// ValidateBootstrap enforces the production credential gate for the
// first-owner flow. Tests and embedded handlers may construct a Config with
// zero values, but the API composition root must not serve a GitHub bootstrap
// without both dedicated pieces of configuration.
func (c Config) ValidateBootstrap() error {
	if len(c.BootstrapCLIKey) != 32 {
		return fmt.Errorf("BOOTSTRAP_CLI_KEY must be configured as base64-encoded 32 bytes")
	}
	if !c.SetupMode && !validGitHubAppClientID(c.GitHubAppClientID) {
		return fmt.Errorf("GITHUB_APP_CLIENT_ID must be configured")
	}
	return nil
}

func (c Config) ValidateSetup() error {
	if !c.SetupMode {
		return nil
	}
	if strings.TrimSpace(c.SetupStateFile) == "" || !filepath.IsAbs(c.SetupStateFile) || filepath.Clean(c.SetupStateFile) == string(filepath.Separator) {
		return fmt.Errorf("STEALTH_SETUP_STATE_FILE must be a valid non-root absolute path in setup mode")
	}
	return nil
}

func validGitHubAppClientID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '.' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
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

func (c Config) ValidateApps() error {
	if c.AppsMaxSourceArchiveBytes <= 0 || c.AppsMaxSourceArchiveBytes > 2<<30 ||
		c.AppsMaxExpandedSourceBytes <= 0 || c.AppsMaxExpandedSourceBytes > 16<<30 ||
		c.AppsMaxSourceFiles < 1 || c.AppsMaxSourceFiles > 1000000 ||
		c.AppsMaxImageArchiveBytes <= 0 || c.AppsMaxImageArchiveBytes > 16<<30 ||
		c.AppsDefaultArtifactQuotaBytes < c.AppsMaxSourceArchiveBytes || c.AppsDefaultArtifactQuotaBytes > 1<<40 {
		return fmt.Errorf("App source, image, file-count, and artifact quota settings are invalid")
	}
	if !validBuildkitAddress(c.AppsBuildkitAddress) {
		return fmt.Errorf("APPS_BUILDKIT_ADDRESS must be a private TCP host:port address")
	}
	if !validBuildKitPath(c.AppsBuildkitCACert) || !validBuildKitPath(c.AppsBuildkitClientCert) || !validBuildKitPath(c.AppsBuildkitClientKey) {
		return fmt.Errorf("App BuildKit TLS certificate paths must be absolute, clean, non-root paths")
	}
	if c.AppsBuildTimeout < time.Minute || c.AppsBuildTimeout > 24*time.Hour || c.AppsBuildLeaseAge < c.AppsBuildTimeout || c.AppsBuildLeaseAge > 48*time.Hour || c.AppsBuildPollInterval < 100*time.Millisecond || c.AppsBuildPollInterval > time.Minute {
		return fmt.Errorf("App build timeout, lease, or polling settings are invalid")
	}
	if strings.TrimSpace(c.AppsBuildStagingRoot) == "" || !filepath.IsAbs(c.AppsBuildStagingRoot) || filepath.Clean(c.AppsBuildStagingRoot) == string(filepath.Separator) {
		return fmt.Errorf("APPS_BUILD_STAGING_ROOT must be an absolute non-root path")
	}
	if !isDockerName(c.AppsBuildStagingVolume) || !isDockerName(c.AppsBuildkitStateVolume) {
		return fmt.Errorf("App build volume names are invalid")
	}
	if len(c.AppsRuntimeNetworkName) > 63 || !isDockerName(c.AppsRuntimeNetworkName) ||
		c.AppsRuntimePollInterval < 100*time.Millisecond || c.AppsRuntimePollInterval > time.Minute ||
		c.AppsRuntimeLeaseAge < 30*time.Second || c.AppsRuntimeLeaseAge > 10*time.Minute ||
		c.AppsRuntimeActionTimeout < 5*time.Second || c.AppsRuntimeActionTimeout > 2*time.Minute ||
		c.AppsRuntimeImageImportTimeout < time.Minute || c.AppsRuntimeImageImportTimeout > 30*time.Minute ||
		c.AppsRuntimeImageCacheMaxBytes < 1<<20 || c.AppsRuntimeImageCacheMaxBytes > 1<<40 ||
		c.AppsRuntimeImageCacheTargetBytes < 1<<20 || c.AppsRuntimeImageCacheTargetBytes >= c.AppsRuntimeImageCacheMaxBytes ||
		c.AppsRuntimeImageGCSweepInterval < time.Minute || c.AppsRuntimeImageGCSweepInterval > 24*time.Hour {
		return fmt.Errorf("App runtime network, poll, lease, or Docker timeout settings are invalid")
	}
	return nil
}

// ValidateAppSecrets is a production startup gate for the dedicated key used
// to encrypt and decrypt persisted App environment values.
func (c Config) ValidateAppSecrets() error {
	if len(c.AppsSecretKey) != 32 {
		return fmt.Errorf("APPS_SECRET_KEY must be configured as base64-encoded 32 bytes")
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
