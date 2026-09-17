package config

import (
	"net"
	"testing"
)

func TestLoadTransportSettings(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://redis.example.test:6379/2")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("METRICS_TOKEN", "scrape-token")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.0.2.10")

	settings, err := loadTransportSettings()
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	settings.apply(&config)
	if config.RedisURL != "redis://redis.example.test:6379/2" || config.HTTPAddress != "127.0.0.1:9090" || config.MetricsToken != "scrape-token" {
		t.Fatalf("unexpected transport settings: redis=%q http=%q metrics=%q", config.RedisURL, config.HTTPAddress, config.MetricsToken)
	}
	if len(config.TrustedProxyCIDRs) != 2 || !config.TrustedProxyCIDRs[0].Contains(net.ParseIP("10.1.2.3")) || !config.TrustedProxyCIDRs[1].Contains(net.ParseIP("192.0.2.10")) {
		t.Fatalf("unexpected trusted proxy networks: %+v", config.TrustedProxyCIDRs)
	}
	config.TrustedProxyCIDRs[0].IP[0] = 203
	if settings.trustedProxyCIDRs[0].IP[0] == 203 {
		t.Fatal("applying transport settings did not clone trusted proxy networks")
	}
}

func TestLoadTransportSettingsRejectsInvalidMetricsToken(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "bad token")
	if _, err := loadTransportSettings(); err == nil {
		t.Fatal("invalid metrics token was accepted")
	}
}
