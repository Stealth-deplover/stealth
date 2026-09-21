package installengine

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	releaseVersionPattern       = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?$`)
	stableReleaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
)

// Traefik is a release-managed third-party runtime dependency. Keep the
// version and multi-architecture manifest digest in one place so fresh
// installs and upgrades converge on the same immutable image.
const defaultTraefikImage = "traefik:v3.7.13@sha256:1c32e7c368204fd72812152ebdd2ac0425993df6fd982317deb02e48f2d5423c"

const traefikTrustedProxyCIDR = "172.31.0.2/32"

// ConfigOptions describes the non-secret choices made before the production
// stack is started. The engine creates all initial credentials in one place so
// a retry never needs to invent a second secret set.
type ConfigOptions struct {
	Version           string
	PublicURL         string
	GitHubAppClientID string
	DockerGID         uint32
	Setup             bool
	InstallRoot       string
	ComposeProject    string
	NetworkSubnet     string
	TrustedProxyCIDRs string
	APIImage          string
	SetupImage        string
	StorageDriver     string
	DatabaseURL       string
	RedisURL          string
}

func GenerateConfig(options ConfigOptions) (string, error) {
	if err := validateVersion(options.Version); err != nil {
		return "", err
	}
	publicURL, err := validatePublicURL(options.PublicURL)
	if err != nil {
		return "", err
	}
	if !options.Setup && !validClientID(options.GitHubAppClientID) {
		return "", errorsf("GitHub App Client ID is required for production configuration")
	}
	if options.Setup && strings.TrimSpace(options.InstallRoot) == "" {
		return "", errorsf("installation root is required for setup configuration")
	}
	if strings.ContainsAny(options.InstallRoot, "\x00\r\n") {
		return "", errorsf("installation root is invalid")
	}
	project := firstNonEmpty(options.ComposeProject, "stealth")
	subnet := firstNonEmpty(options.NetworkSubnet, "172.30.0.0/24")
	trustedProxy := ensureTraefikTrustedProxyCIDR(options.TrustedProxyCIDRs, subnet)
	storageDriver := strings.ToLower(firstNonEmpty(options.StorageDriver, "local"))
	if storageDriver != "local" && storageDriver != "s3" {
		return "", errorsf("storage driver must be local or s3")
	}
	postgresPassword, err := randomHex(24)
	if err != nil {
		return "", err
	}
	redisPassword, err := randomHex(24)
	if err != nil {
		return "", err
	}
	functionsKey, err := randomBase64(32)
	if err != nil {
		return "", err
	}
	bootstrapKey, err := randomBase64(32)
	if err != nil {
		return "", err
	}
	metricsToken, err := randomHex(32)
	if err != nil {
		return "", err
	}
	clickhousePassword, err := randomHex(32)
	if err != nil {
		return "", err
	}
	apiImage := options.APIImage
	if apiImage == "" {
		apiImage = ImageName("stealth-api", options.Version)
	}
	setupImage := options.SetupImage
	if setupImage == "" {
		setupImage = ImageName("stealth-setup", options.Version)
	}
	collectorImage := ImageName("stealth-otel-collector", options.Version)
	databaseURL := options.DatabaseURL
	if databaseURL == "" {
		databaseURL = "postgres://stealth:" + postgresPassword + "@postgres:5432/stealth?sslmode=disable"
	}
	redisURL := options.RedisURL
	if redisURL == "" {
		redisURL = "redis://:" + redisPassword + "@redis:6379/0"
	}
	cookieSecure := "false"
	if strings.EqualFold(mustURLScheme(publicURL), "https") {
		cookieSecure = "true"
	}
	values := map[string]string{
		"COMPOSE_PROJECT_NAME":                  project,
		"STEALTH_API_IMAGE":                     apiImage,
		"STEALTH_SETUP_IMAGE":                   setupImage,
		"STEALTH_WORKER_IMAGE":                  ImageName("stealth-worker", options.Version),
		"STEALTH_MIGRATE_IMAGE":                 ImageName("stealth-migrate", options.Version),
		"STEALTH_CONSOLE_IMAGE":                 ImageName("stealth-console", options.Version),
		"STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE":  ImageName("stealth-telemetry-docker-proxy", options.Version),
		"TRAEFIK_IMAGE":                         defaultTraefikImage,
		"OTEL_COLLECTOR_IMAGE":                  collectorImage,
		"OTEL_HOST_COLLECTOR_IMAGE":             collectorImage,
		"OTEL_DOCKER_COLLECTOR_IMAGE":           collectorImage,
		"OTEL_DOCKER_LOGS_COLLECTOR_IMAGE":      ImageName("stealth-otel-docker-logs", options.Version),
		"POSTGRES_DB":                           "stealth",
		"POSTGRES_USER":                         "stealth",
		"POSTGRES_PASSWORD":                     postgresPassword,
		"DATABASE_URL":                          databaseURL,
		"REDIS_PASSWORD":                        redisPassword,
		"REDIS_URL":                             redisURL,
		"FUNCTIONS_SECRET_KEY":                  functionsKey,
		"BOOTSTRAP_CLI_KEY":                     bootstrapKey,
		"GITHUB_APP_CLIENT_ID":                  strings.TrimSpace(options.GitHubAppClientID),
		"PUBLIC_APP_URL":                        publicURL,
		"COOKIE_SECURE":                         cookieSecure,
		"TRUSTED_PROXY_CIDRS":                   trustedProxy,
		"STEALTH_NETWORK_SUBNET":                subnet,
		"STEALTH_NETWORK_NAME":                  "stealth_network",
		"DOCKER_GID":                            strconv.FormatUint(uint64(options.DockerGID), 10),
		"METRICS_TOKEN":                         metricsToken,
		"CLICKHOUSE_IMAGE":                      "clickhouse/clickhouse-server:26.8.6.5",
		"CLICKHOUSE_DATABASE":                   "stealth_telemetry",
		"CLICKHOUSE_USER":                       "stealth",
		"CLICKHOUSE_PASSWORD":                   clickhousePassword,
		"CLICKHOUSE_VOLUME_NAME":                "stealth_clickhouse_data",
		"OTEL_COLLECTOR_HEALTH_URL":             "http://otel-collector:13133",
		"OTELCOL_VOLUME_NAME":                   "stealth_otelcol_state",
		"OTEL_DOCKER_LOGS_VOLUME_NAME":          "stealth_otel_docker_logs_state",
		"STEALTH_TELEMETRY_STORE_NETWORK_NAME":  "stealth_telemetry_store",
		"STEALTH_TELEMETRY_DOCKER_NETWORK_NAME": "stealth_telemetry_docker",
		"STEALTH_INGRESS_NETWORK_NAME":          "stealth_ingress",
		"FUNCTIONS_RUNNER_ENABLED":              "true",
		"FUNCTIONS_WORKER_ID":                   "stealth-worker",
		"FUNCTIONS_RUNNER_STAGING_VOLUME":       "stealth_function_runner_staging",
		"STORAGE_DRIVER":                        storageDriver,
		"STORAGE_MAX_FILE_SIZE":                 "50MiB",
		"STORAGE_DEFAULT_QUOTA_BYTES":           "1GiB",
		"PROJECT_OPERATION_RATE_LIMIT":          "120",
		"PROJECT_OPERATION_RATE_WINDOW":         "1m",
		"AUTH_RATE_LIMIT":                       "10",
		"AUTH_RATE_WINDOW":                      "1m",
		"PROXY_HTTP_BIND":                       "127.0.0.1",
		"PROXY_HTTP_PORT":                       "8080",
		"API_HOST_PORT":                         "18080",
		"CONSOLE_HOST_PORT":                     "13000",
		"SETUP_API_HOST_PORT":                   "18081",
		"SETUP_CONSOLE_HOST_PORT":               "13001",
		"SETUP_PROXY_HTTP_PORT":                 "8081",
		"SETUP_MODE":                            strconv.FormatBool(options.Setup),
	}
	values["STEALTH_TELEMETRY_INGEST_NETWORK_NAME"] = "stealth_telemetry_ingest"
	if options.Setup {
		values["STEALTH_INSTALL_ROOT"] = options.InstallRoot
		root := strings.TrimRight(options.InstallRoot, "/")
		values["STEALTH_SETUP_STATE_FILE"] = root + "/state/setup-state.enc"
		values["STEALTH_PRODUCTION_COMPOSE_FILE"] = root + "/compose.production.yaml"
		values["STEALTH_SETUP_COMPOSE_FILE"] = root + "/compose.setup.yaml"
	}
	if err := validateConfigValues(values); err != nil {
		return "", err
	}
	return FormatEnvFile(values), nil
}

func ImageName(name, version string) string {
	return "ghcr.io/stealth-deplover/" + name + ":" + strings.TrimSpace(version)
}

var releaseManagedImageNames = map[string]string{
	"STEALTH_API_IMAGE":                    "stealth-api",
	"STEALTH_SETUP_IMAGE":                  "stealth-setup",
	"STEALTH_WORKER_IMAGE":                 "stealth-worker",
	"STEALTH_MIGRATE_IMAGE":                "stealth-migrate",
	"STEALTH_CONSOLE_IMAGE":                "stealth-console",
	"STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE": "stealth-telemetry-docker-proxy",
	"OTEL_COLLECTOR_IMAGE":                 "stealth-otel-collector",
	"OTEL_HOST_COLLECTOR_IMAGE":            "stealth-otel-collector",
	"OTEL_DOCKER_COLLECTOR_IMAGE":          "stealth-otel-collector",
	"OTEL_DOCKER_LOGS_COLLECTOR_IMAGE":     "stealth-otel-docker-logs",
}

// MigrateReleaseConfig advances release-owned defaults while preserving
// operator-selected values. Image values are updated only when they still
// equal the canonical image for the installed release; custom registries,
// digests, and custom tags are treated as operator overrides. Secret values
// are never regenerated unless a required secret key is absent.
func MigrateReleaseConfig(values map[string]string, targetVersion, installedVersion string) (string, error) {
	if err := ValidateReleaseVersion(targetVersion); err != nil {
		return "", err
	}
	result := make(map[string]string, len(values)+len(releaseManagedImageNames)+16)
	for key, value := range values {
		result[key] = value
	}
	updates := make(map[string]string)
	for key, imageName := range releaseManagedImageNames {
		current := strings.TrimSpace(result[key])
		target := ImageName(imageName, targetVersion)
		switch {
		case current == "":
			updates[key] = target
		case strings.TrimSpace(installedVersion) != "" && current == ImageName(imageName, installedVersion):
			updates[key] = target
		}
	}
	for key, value := range map[string]string{
		"CLICKHOUSE_IMAGE":                      "clickhouse/clickhouse-server:26.8.6.5",
		"CLICKHOUSE_DATABASE":                   "stealth_telemetry",
		"CLICKHOUSE_USER":                       "stealth",
		"CLICKHOUSE_VOLUME_NAME":                "stealth_clickhouse_data",
		"OTEL_COLLECTOR_HEALTH_URL":             "http://otel-collector:13133",
		"OTELCOL_VOLUME_NAME":                   "stealth_otelcol_state",
		"OTEL_DOCKER_LOGS_VOLUME_NAME":          "stealth_otel_docker_logs_state",
		"STEALTH_TELEMETRY_STORE_NETWORK_NAME":  "stealth_telemetry_store",
		"STEALTH_TELEMETRY_INGEST_NETWORK_NAME": "stealth_telemetry_ingest",
		"STEALTH_TELEMETRY_DOCKER_NETWORK_NAME": "stealth_telemetry_docker",
		"TRAEFIK_IMAGE":                         defaultTraefikImage,
		"STEALTH_INGRESS_NETWORK_NAME":          "stealth_ingress",
	} {
		if strings.TrimSpace(result[key]) == "" {
			updates[key] = value
		}
	}
	trustedProxy := ensureTraefikTrustedProxyCIDR(result["TRUSTED_PROXY_CIDRS"], result["STEALTH_NETWORK_SUBNET"])
	if trustedProxy != strings.TrimSpace(result["TRUSTED_PROXY_CIDRS"]) {
		updates["TRUSTED_PROXY_CIDRS"] = trustedProxy
	}
	if strings.TrimSpace(result["CLICKHOUSE_PASSWORD"]) == "" {
		password, err := randomHex(32)
		if err != nil {
			return "", fmt.Errorf("generate ClickHouse password: %w", err)
		}
		updates["CLICKHOUSE_PASSWORD"] = password
	}
	return MergeEnv(result, updates)
}

func ensureTraefikTrustedProxyCIDR(value, fallbackSubnet string) string {
	trusted := strings.TrimSpace(value)
	if trusted == "" {
		trusted = firstNonEmpty(strings.TrimSpace(fallbackSubnet), "172.30.0.0/24")
	}
	for _, entry := range strings.Split(trusted, ",") {
		if strings.TrimSpace(entry) == traefikTrustedProxyCIDR {
			return trusted
		}
	}
	return trusted + "," + traefikTrustedProxyCIDR
}

func validateConfigValues(values map[string]string) error {
	for key, value := range values {
		if !ValidEnvKey(key) || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("invalid generated configuration value for %s", key)
		}
		if strings.TrimSpace(value) == "" && key != "GITHUB_APP_CLIENT_ID" {
			return fmt.Errorf("generated configuration value for %s is empty", key)
		}
	}
	for _, key := range []string{"STEALTH_API_IMAGE", "STEALTH_SETUP_IMAGE", "STEALTH_WORKER_IMAGE", "STEALTH_MIGRATE_IMAGE", "STEALTH_CONSOLE_IMAGE", "STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE", "OTEL_COLLECTOR_IMAGE", "OTEL_HOST_COLLECTOR_IMAGE", "OTEL_DOCKER_COLLECTOR_IMAGE", "OTEL_DOCKER_LOGS_COLLECTOR_IMAGE", "TRAEFIK_IMAGE"} {
		if !validImageReference(values[key]) {
			return fmt.Errorf("generated image reference for %s is invalid", key)
		}
	}
	return nil
}

func validImageReference(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || strings.ContainsRune("/._:@-", character) {
			continue
		}
		return false
	}
	return true
}

// ValidateReleaseVersion accepts stable releases and explicitly numbered RCs.
// The release workflow and explicit installer version pin use the same format.
func ValidateReleaseVersion(value string) error {
	if !releaseVersionPattern.MatchString(strings.TrimSpace(value)) {
		return errorsf("release version %q must match vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N", value)
	}
	return nil
}

// ValidateStableReleaseVersion deliberately excludes prereleases. It is used
// by the automatic update path so a normal installation never follows an RC.
func ValidateStableReleaseVersion(value string) error {
	if !stableReleaseVersionPattern.MatchString(strings.TrimSpace(value)) {
		return errorsf("stable release version %q must match vMAJOR.MINOR.PATCH", value)
	}
	return nil
}

func validateVersion(value string) error {
	return ValidateReleaseVersion(value)
}

func validatePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errorsf("URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errorsf("URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validClientID(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 160 {
		return false
	}
	for _, character := range raw {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune(".-_", character) {
			continue
		}
		return false
	}
	return true
}

func randomHex(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func randomBase64(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

func mustURLScheme(raw string) string {
	parsed, _ := url.Parse(raw)
	if parsed == nil {
		return ""
	}
	return parsed.Scheme
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func errorsf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
