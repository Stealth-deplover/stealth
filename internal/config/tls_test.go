package config

import (
	"path/filepath"
	"testing"
)

func TestLoadTLSSettings(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "certs")
	t.Setenv("ACME_ENABLED", "true")
	t.Setenv("ACME_EMAIL", "ops@example.com")
	t.Setenv("ACME_DIRECTORY_URL", "https://acme.example.test/directory")
	t.Setenv("ACME_TLS_ADDR", "127.0.0.1:18443")
	t.Setenv("ACME_HTTP_CHALLENGE_ADDR", "127.0.0.1:18080")
	t.Setenv("ACME_CERT_CACHE_DIR", cacheDir)

	settings, err := loadTLSSettings("/var/lib/stealth/storage", "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	settings.apply(&config)
	if !config.ACMEEnabled || config.ACMEEmail != "ops@example.com" || config.ACMEDirectoryURL != "https://acme.example.test/directory" || config.ACMETLSAddress != "127.0.0.1:18443" || config.ACMEHTTPChallengeAddress != "127.0.0.1:18080" || config.ACMECertCacheDir != cacheDir {
		t.Fatalf("unexpected TLS settings: enabled=%v email=%q directory=%q tls=%q challenge=%q cache=%q", config.ACMEEnabled, config.ACMEEmail, config.ACMEDirectoryURL, config.ACMETLSAddress, config.ACMEHTTPChallengeAddress, config.ACMECertCacheDir)
	}
}

func TestLoadTLSSettingsRejectsHTTPListenerCollision(t *testing.T) {
	t.Setenv("ACME_ENABLED", "true")
	t.Setenv("ACME_EMAIL", "ops@example.com")
	t.Setenv("ACME_DIRECTORY_URL", "https://acme.example.test/directory")
	t.Setenv("ACME_TLS_ADDR", ":8443")
	t.Setenv("ACME_HTTP_CHALLENGE_ADDR", ":8081")
	t.Setenv("ACME_CERT_CACHE_DIR", filepath.Join(t.TempDir(), "certs"))
	if _, err := loadTLSSettings("/var/lib/stealth/storage", ":8443"); err == nil {
		t.Fatal("ACME listener collision was accepted")
	}
}
