package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// telemetryStoreSettings owns the ClickHouse query/storage contract. The
// address is deliberately optional so an unavailable telemetry backend never
// prevents the core control plane from starting.
type telemetryStoreSettings struct {
	addr               string
	database           string
	user               string
	password           string
	collectorHealthURL string
	maxDuration        time.Duration
	maxRange           time.Duration
	maxRows            int
	retention          time.Duration
}

func loadTelemetryStoreSettings() (telemetryStoreSettings, error) {
	addr := strings.TrimSpace(os.Getenv("CLICKHOUSE_ADDR"))
	if addr != "" {
		if err := validateClickHouseAddresses(addr); err != nil {
			return telemetryStoreSettings{}, err
		}
	}
	database := strings.TrimSpace(value("CLICKHOUSE_DATABASE", "stealth_telemetry"))
	if !isSQLIdentifier(database) {
		return telemetryStoreSettings{}, fmt.Errorf("CLICKHOUSE_DATABASE must be a valid identifier")
	}
	user := strings.TrimSpace(value("CLICKHOUSE_USER", "stealth"))
	if user == "" || len(user) > 128 || strings.ContainsAny(user, "\r\n\x00") {
		return telemetryStoreSettings{}, fmt.Errorf("CLICKHOUSE_USER must be a non-empty safe value")
	}
	collectorHealthURL := strings.TrimSpace(os.Getenv("OTEL_COLLECTOR_HEALTH_URL"))
	if collectorHealthURL != "" {
		parsed, err := url.Parse(collectorHealthURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return telemetryStoreSettings{}, fmt.Errorf("OTEL_COLLECTOR_HEALTH_URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
		}
	}

	maxDuration, err := boundedDuration("TELEMETRY_MAX_QUERY_DURATION", "10s", time.Second, time.Minute)
	if err != nil {
		return telemetryStoreSettings{}, err
	}
	maxRange, err := boundedDuration("TELEMETRY_MAX_QUERY_RANGE", "720h", 15*time.Minute, 365*24*time.Hour)
	if err != nil {
		return telemetryStoreSettings{}, err
	}
	maxRows, err := boundedInt("TELEMETRY_MAX_QUERY_ROWS", "1000", 1, 10000)
	if err != nil {
		return telemetryStoreSettings{}, err
	}
	retention, err := boundedDuration("TELEMETRY_RETENTION", "720h", 24*time.Hour, 10*365*24*time.Hour)
	if err != nil {
		return telemetryStoreSettings{}, err
	}

	return telemetryStoreSettings{
		addr:               addr,
		database:           database,
		user:               user,
		password:           os.Getenv("CLICKHOUSE_PASSWORD"),
		collectorHealthURL: collectorHealthURL,
		maxDuration:        maxDuration,
		maxRange:           maxRange,
		maxRows:            maxRows,
		retention:          retention,
	}, nil
}

func (s telemetryStoreSettings) apply(c *Config) {
	c.TelemetryClickHouseAddr = s.addr
	c.TelemetryClickHouseDatabase = s.database
	c.TelemetryClickHouseUser = s.user
	c.TelemetryClickHousePassword = s.password
	c.TelemetryCollectorHealthURL = s.collectorHealthURL
	c.TelemetryMaxQueryDuration = s.maxDuration
	c.TelemetryMaxQueryRange = s.maxRange
	c.TelemetryMaxQueryRows = s.maxRows
	c.TelemetryRetention = s.retention
}

func (c *Config) applyTelemetryStoreDefaults() {
	if c.TelemetryClickHouseDatabase == "" {
		c.TelemetryClickHouseDatabase = "stealth_telemetry"
	}
	if c.TelemetryClickHouseUser == "" {
		c.TelemetryClickHouseUser = "stealth"
	}
	if c.TelemetryMaxQueryDuration <= 0 {
		c.TelemetryMaxQueryDuration = 10 * time.Second
	}
	if c.TelemetryMaxQueryRange <= 0 {
		c.TelemetryMaxQueryRange = 30 * 24 * time.Hour
	}
	if c.TelemetryMaxQueryRows <= 0 {
		c.TelemetryMaxQueryRows = 1000
	}
	if c.TelemetryRetention <= 0 {
		c.TelemetryRetention = 30 * 24 * time.Hour
	}
}

func boundedDuration(name, fallback string, minimum, maximum time.Duration) (time.Duration, error) {
	parsed, err := time.ParseDuration(value(name, fallback))
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %s and %s", name, minimum, maximum)
	}
	return parsed, nil
}

func boundedInt(name, fallback string, minimum, maximum int) (int, error) {
	parsed, err := strconv.Atoi(value(name, fallback))
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}

func validateClickHouseAddresses(raw string) error {
	parts := strings.Split(raw, ",")
	if len(parts) > 8 {
		return fmt.Errorf("CLICKHOUSE_ADDR must contain at most 8 hosts")
	}
	for _, part := range parts {
		address := strings.TrimSpace(part)
		host, port, err := net.SplitHostPort(address)
		if err != nil || strings.TrimSpace(host) == "" {
			return fmt.Errorf("CLICKHOUSE_ADDR must contain host:port values")
		}
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return fmt.Errorf("CLICKHOUSE_ADDR contains an invalid port")
		}
		if strings.ContainsAny(host, "\r\n\x00") {
			return fmt.Errorf("CLICKHOUSE_ADDR contains an unsafe host")
		}
	}
	return nil
}

func isSQLIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}
