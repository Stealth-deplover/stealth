package config

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFunctionsSecretKeyIsDecodedAndValidated(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	key := []byte(strings.Repeat("k", 32))
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg.FunctionsSecretKey) != string(key) {
		t.Fatalf("decoded FunctionsSecretKey = %x, want %x", cfg.FunctionsSecretKey, key)
	}
	if err := cfg.ValidateFunctions(); err != nil {
		t.Fatalf("ValidateFunctions() = %v", err)
	}

	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("too short")))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FUNCTIONS_SECRET_KEY") {
		t.Fatalf("Load with short function key returned %v", err)
	}
}

func TestValidateFunctionsFailsClosedWithoutSecretKey(t *testing.T) {
	cfg := Config{FunctionsMaxArtifactSize: 10, FunctionsDefaultQuotaBytes: 20}
	if err := cfg.ValidateFunctions(); err == nil || !strings.Contains(err.Error(), "FUNCTIONS_SECRET_KEY") {
		t.Fatalf("ValidateFunctions without key returned %v", err)
	}
}

func TestLoadAppSessionTTLIsSeparateAndBounded(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("APP_SESSION_TTL", "30m")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.AppSessionTTL != 30*time.Minute {
		t.Fatalf("AppSessionTTL = %s, want 30m", config.AppSessionTTL)
	}

	t.Setenv("APP_SESSION_TTL", "721h")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "APP_SESSION_TTL") {
		t.Fatalf("Load with overlong APP_SESSION_TTL returned %v", err)
	}
}

func TestLoadAuthDeliveryConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32))))
	t.Setenv("AUTH_VERIFICATION_TTL", "48h")
	t.Setenv("AUTH_PASSWORD_RESET_TTL", "90m")
	t.Setenv("PUBLIC_APP_URL", "https://console.example.test/auth")
	t.Setenv("EMAIL_DELIVERY_MODE", "smtp")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_FROM", "no-reply@example.test")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "secret")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthVerificationTTL != 48*time.Hour || cfg.AuthPasswordResetTTL != 90*time.Minute || cfg.PublicAppURL != "https://console.example.test/auth" || cfg.EmailDeliveryMode != "smtp" || cfg.SMTPHost != "smtp.example.test" || cfg.SMTPPort != 2525 || cfg.SMTPFrom != "no-reply@example.test" {
		t.Fatalf("unexpected auth delivery config: %+v", cfg)
	}
	t.Setenv("EMAIL_DELIVERY_MODE", "unknown")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "EMAIL_DELIVERY_MODE") {
		t.Fatalf("invalid email mode returned %v", err)
	}
}

func TestLoadConsoleCORSOrigins(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("CONSOLE_CORS_ORIGINS", "http://localhost:5173, https://console.example.test:443")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ConsoleCORSOrigins) != 2 || cfg.ConsoleCORSOrigins[0] != "http://localhost:5173" || cfg.ConsoleCORSOrigins[1] != "https://console.example.test" {
		t.Fatalf("ConsoleCORSOrigins = %#v", cfg.ConsoleCORSOrigins)
	}

	t.Setenv("CONSOLE_CORS_ORIGINS", "https://console.example.test/path")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CONSOLE_CORS_ORIGINS") {
		t.Fatalf("path-bearing console origin returned %v", err)
	}
	t.Setenv("CONSOLE_CORS_ORIGINS", "https://console.example.test,https://CONSOLE.example.test")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CONSOLE_CORS_ORIGINS") {
		t.Fatalf("duplicate console origin returned %v", err)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("Load without DATABASE_URL returned %v", err)
	}
}

func TestLoadS3StorageConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("STORAGE_DRIVER", "s3")
	t.Setenv("STORAGE_S3_ENDPOINT", "http://minio.example.test:9000")
	t.Setenv("STORAGE_S3_REGION", "us-east-1")
	t.Setenv("STORAGE_S3_BUCKET", "stealth-files")
	t.Setenv("STORAGE_S3_ACCESS_KEY", "access")
	t.Setenv("STORAGE_S3_SECRET_KEY", "secret")
	t.Setenv("STORAGE_S3_USE_SSL", "false")
	t.Setenv("STORAGE_S3_PATH_STYLE", "true")
	t.Setenv("STORAGE_S3_PREFIX", "tenant-blobs")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StorageDriver != "s3" || cfg.StorageS3Endpoint != "http://minio.example.test:9000" || cfg.StorageS3Bucket != "stealth-files" || cfg.StorageS3Prefix != "tenant-blobs" || cfg.StorageS3UseSSL || !cfg.StorageS3PathStyle {
		t.Fatalf("unexpected S3 storage config: %+v", cfg)
	}
	if err := cfg.ValidateStorage(); err != nil {
		t.Fatalf("ValidateStorage() = %v", err)
	}

	t.Setenv("STORAGE_S3_SECRET_KEY", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STORAGE_S3_ENDPOINT") {
		t.Fatalf("missing S3 secret returned %v", err)
	}
}

func TestLoadRunnerImageReferences(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32))))
	t.Setenv("FUNCTIONS_RUNNER_NODE_IMAGE", "registry.example.test/stealth/node@sha256:"+strings.Repeat("a", 64))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FunctionsRunnerNodeImage == "" || !strings.Contains(cfg.FunctionsRunnerNodeImage, "@sha256:") {
		t.Fatalf("runner image was not loaded: %q", cfg.FunctionsRunnerNodeImage)
	}
	t.Setenv("FUNCTIONS_RUNNER_NODE_IMAGE", "node:22 alpine")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FUNCTIONS_RUNNER_NODE_IMAGE") {
		t.Fatalf("invalid runner image returned %v", err)
	}
}

func TestLoadWorkerMetricsAddress(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32))))
	t.Setenv("FUNCTIONS_RUNNER_METRICS_ADDR", "127.0.0.1:19091")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FunctionsRunnerMetricsAddress != "127.0.0.1:19091" {
		t.Fatalf("FunctionsRunnerMetricsAddress = %q", cfg.FunctionsRunnerMetricsAddress)
	}

	t.Setenv("FUNCTIONS_RUNNER_METRICS_ADDR", ":0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FUNCTIONS_RUNNER_METRICS_ADDR") {
		t.Fatalf("invalid metrics address returned %v", err)
	}
}

func TestLoadWorkerBuildTimeout(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32))))
	t.Setenv("FUNCTIONS_RUNNER_BUILD_TIMEOUT", "5m")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FunctionsRunnerBuildTimeout != 5*time.Minute {
		t.Fatalf("FunctionsRunnerBuildTimeout = %s, want 5m", cfg.FunctionsRunnerBuildTimeout)
	}
	t.Setenv("AGENT_RUNNER_ENABLED", "true")
	t.Setenv("AGENT_RUNNER_EXECUTION_TIMEOUT", "7m")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AgentRunnerEnabled || cfg.AgentRunnerExecutionTimeout != 7*time.Minute {
		t.Fatalf("unexpected Agent runner config: enabled=%v timeout=%s", cfg.AgentRunnerEnabled, cfg.AgentRunnerExecutionTimeout)
	}
	t.Setenv("FUNCTIONS_RUNNER_BUILD_TIMEOUT", "30s")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FUNCTIONS_RUNNER_BUILD_TIMEOUT") {
		t.Fatalf("invalid build timeout returned %v", err)
	}
	t.Setenv("FUNCTIONS_RUNNER_BUILD_TIMEOUT", "15m")
	t.Setenv("AGENT_RUNNER_EXECUTION_TIMEOUT", "30s")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AGENT_RUNNER_EXECUTION_TIMEOUT") {
		t.Fatalf("invalid Agent execution timeout returned %v", err)
	}
}

func TestLoadTelemetryConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("t", 32))))
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://tempo:4318")
	t.Setenv("OTEL_SERVICE_NAME", "stealth-api")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TelemetryOTLPEndpoint != "http://tempo:4318" || cfg.TelemetryServiceName != "stealth-api" || cfg.TelemetrySampleRatio != 0.25 {
		t.Fatalf("unexpected telemetry config: endpoint=%q service=%q ratio=%v", cfg.TelemetryOTLPEndpoint, cfg.TelemetryServiceName, cfg.TelemetrySampleRatio)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "tempo:4318")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Fatalf("invalid OTLP endpoint returned %v", err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://tempo:4318")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OTEL_TRACES_SAMPLER_ARG") {
		t.Fatalf("invalid OTLP sample ratio returned %v", err)
	}
}

func TestLoadAgentProviderCatalog(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	t.Setenv("AGENT_PROVIDER_CATALOG", `[{"id":"local","name":"Local gateway","models":["model-a"," model-b "]}]`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AgentProviderCatalog) != 1 || cfg.AgentProviderCatalog[0].ID != "local" || cfg.AgentProviderCatalog[0].Name != "Local gateway" || len(cfg.AgentProviderCatalog[0].Models) != 2 || cfg.AgentProviderCatalog[0].Models[1] != "model-b" {
		t.Fatalf("unexpected AgentProviderCatalog: %#v", cfg.AgentProviderCatalog)
	}

	t.Setenv("AGENT_PROVIDER_CATALOG", `[{"id":"local","name":"Local","models":[]}]`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AGENT_PROVIDER_CATALOG") {
		t.Fatalf("empty model catalog returned %v", err)
	}
	t.Setenv("AGENT_PROVIDER_CATALOG", `[{"id":"local","name":"Local","models":["model"]},{"id":"local","name":"Duplicate","models":["model"]}]`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AGENT_PROVIDER_CATALOG") {
		t.Fatalf("duplicate provider catalog returned %v", err)
	}
}

func TestLoadSitesConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	t.Setenv("SITES_MAX_ARTIFACT_SIZE", "8MiB")
	t.Setenv("SITES_DEFAULT_QUOTA_BYTES", "64MiB")
	t.Setenv("SITES_MAX_EXPANDED_BYTES", "32MiB")
	t.Setenv("SITES_MAX_FILES", "128")
	t.Setenv("SITES_GIT_FETCH_CONCURRENCY", "6")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SitesMaxArtifactSize != 8<<20 || cfg.SitesDefaultQuotaBytes != 64<<20 || cfg.SitesMaxExpandedBytes != 32<<20 || cfg.SitesMaxFiles != 128 || cfg.SitesGitFetchConcurrency != 6 {
		t.Fatalf("unexpected Sites config: %+v", cfg)
	}
	if err := cfg.ValidateSites(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SITES_MAX_FILES", "0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SITES_MAX_FILES") {
		t.Fatalf("invalid site file count returned %v", err)
	}
	t.Setenv("SITES_MAX_FILES", "128")
	t.Setenv("SITES_GIT_FETCH_CONCURRENCY", "33")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SITES_GIT_FETCH_CONCURRENCY") {
		t.Fatalf("invalid Git fetch concurrency returned %v", err)
	}
}

func TestLoadACMEConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/stealth")
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))))
	t.Setenv("ACME_ENABLED", "true")
	t.Setenv("ACME_EMAIL", "ops@example.com")
	t.Setenv("ACME_DIRECTORY_URL", "https://acme.example.test/directory")
	t.Setenv("ACME_TLS_ADDR", "127.0.0.1:18443")
	t.Setenv("ACME_HTTP_CHALLENGE_ADDR", "127.0.0.1:18080")
	t.Setenv("ACME_CERT_CACHE_DIR", filepath.Join(t.TempDir(), "certs"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ACMEEnabled || cfg.ACMEEmail != "ops@example.com" || cfg.ACMEDirectoryURL != "https://acme.example.test/directory" || cfg.ACMETLSAddress != "127.0.0.1:18443" || cfg.ACMEHTTPChallengeAddress != "127.0.0.1:18080" {
		t.Fatalf("unexpected ACME config: %+v", cfg)
	}
	if err := cfg.ValidateSites(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ACME_EMAIL", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ACME_EMAIL") {
		t.Fatalf("missing ACME email returned %v", err)
	}
}

func TestValidateSitesACMERequiresSafeConfiguration(t *testing.T) {
	base := Config{SitesMaxArtifactSize: 1, SitesMaxExpandedBytes: 1, SitesDefaultQuotaBytes: 1, SitesMaxFiles: 1, SitesGitFetchConcurrency: 4, ACMEEnabled: true, ACMEEmail: "ops@example.com", ACMEDirectoryURL: "https://acme.example.test/directory", ACMETLSAddress: ":8443", ACMEHTTPChallengeAddress: ":8081", ACMECertCacheDir: "/var/lib/stealth/storage/acme"}
	if err := base.ValidateSites(); err != nil {
		t.Fatal(err)
	}
	base.ACMEEmail = "not-an-email"
	if err := base.ValidateSites(); err == nil || !strings.Contains(err.Error(), "ACME_EMAIL") {
		t.Fatalf("invalid ACME email returned %v", err)
	}
	base.ACMEEmail = "ops@example.com"
	base.ACMEDirectoryURL = "https://acme.example.test:bad/directory"
	if err := base.ValidateSites(); err == nil || !strings.Contains(err.Error(), "ACME_DIRECTORY_URL") {
		t.Fatalf("invalid ACME directory port returned %v", err)
	}
	base.ACMEDirectoryURL = "https://acme.example.test/directory"
	base.HTTPAddress = ":8443"
	if err := base.ValidateSites(); err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
		t.Fatalf("ACME listener collision returned %v", err)
	}
}
