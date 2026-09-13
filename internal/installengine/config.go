package installengine

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

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
	trustedProxy := firstNonEmpty(options.TrustedProxyCIDRs, subnet)
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
	apiImage := options.APIImage
	if apiImage == "" {
		apiImage = ImageName("stealth-api", options.Version)
	}
	setupImage := options.SetupImage
	if setupImage == "" {
		setupImage = ImageName("stealth-setup", options.Version)
	}
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
		"COMPOSE_PROJECT_NAME":            project,
		"STEALTH_API_IMAGE":               apiImage,
		"STEALTH_SETUP_IMAGE":             setupImage,
		"STEALTH_WORKER_IMAGE":            ImageName("stealth-worker", options.Version),
		"STEALTH_MIGRATE_IMAGE":           ImageName("stealth-migrate", options.Version),
		"STEALTH_CONSOLE_IMAGE":           ImageName("stealth-console", options.Version),
		"POSTGRES_DB":                     "stealth",
		"POSTGRES_USER":                   "stealth",
		"POSTGRES_PASSWORD":               postgresPassword,
		"DATABASE_URL":                    databaseURL,
		"REDIS_PASSWORD":                  redisPassword,
		"REDIS_URL":                       redisURL,
		"FUNCTIONS_SECRET_KEY":            functionsKey,
		"BOOTSTRAP_CLI_KEY":               bootstrapKey,
		"GITHUB_APP_CLIENT_ID":            strings.TrimSpace(options.GitHubAppClientID),
		"PUBLIC_APP_URL":                  publicURL,
		"COOKIE_SECURE":                   cookieSecure,
		"TRUSTED_PROXY_CIDRS":             trustedProxy,
		"STEALTH_NETWORK_SUBNET":          subnet,
		"STEALTH_NETWORK_NAME":            "stealth_network",
		"DOCKER_GID":                      strconv.FormatUint(uint64(options.DockerGID), 10),
		"METRICS_TOKEN":                   metricsToken,
		"FUNCTIONS_RUNNER_ENABLED":        "true",
		"FUNCTIONS_WORKER_ID":             "stealth-worker",
		"FUNCTIONS_RUNNER_STAGING_VOLUME": "stealth_function_runner_staging",
		"STORAGE_DRIVER":                  storageDriver,
		"STORAGE_MAX_FILE_SIZE":           "50MiB",
		"STORAGE_DEFAULT_QUOTA_BYTES":     "1GiB",
		"PROJECT_OPERATION_RATE_LIMIT":    "120",
		"PROJECT_OPERATION_RATE_WINDOW":   "1m",
		"AUTH_RATE_LIMIT":                 "10",
		"AUTH_RATE_WINDOW":                "1m",
		"PROXY_HTTP_BIND":                 "127.0.0.1",
		"PROXY_HTTP_PORT":                 "8080",
		"API_HOST_PORT":                   "18080",
		"CONSOLE_HOST_PORT":               "13000",
		"SETUP_API_HOST_PORT":             "18081",
		"SETUP_CONSOLE_HOST_PORT":         "13001",
		"SETUP_PROXY_HTTP_PORT":           "8081",
		"SETUP_MODE":                      strconv.FormatBool(options.Setup),
	}
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

func validateConfigValues(values map[string]string) error {
	for key, value := range values {
		if !ValidEnvKey(key) || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("invalid generated configuration value for %s", key)
		}
		if strings.TrimSpace(value) == "" && key != "GITHUB_APP_CLIENT_ID" {
			return fmt.Errorf("generated configuration value for %s is empty", key)
		}
	}
	for _, key := range []string{"STEALTH_API_IMAGE", "STEALTH_SETUP_IMAGE", "STEALTH_WORKER_IMAGE", "STEALTH_MIGRATE_IMAGE", "STEALTH_CONSOLE_IMAGE"} {
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

func validateVersion(value string) error {
	value = strings.TrimSpace(value)
	if len(value) < 6 || value[0] != 'v' {
		return errorsf("release version must match vMAJOR.MINOR.PATCH")
	}
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) != 3 {
		return errorsf("release version must match vMAJOR.MINOR.PATCH")
	}
	for _, part := range parts {
		if part == "" {
			return errorsf("release version must match vMAJOR.MINOR.PATCH")
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return errorsf("release version must match vMAJOR.MINOR.PATCH")
			}
		}
	}
	return nil
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
