package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDatabaseSettings(t *testing.T) {
	t.Setenv("DATABASE_URL", " postgres://example.invalid/stealth ")
	t.Setenv("DATABASE_MAX_CONNS", "32")
	t.Setenv("DATABASE_MIN_CONNS", "4")
	t.Setenv("DATABASE_MAX_CONN_LIFETIME", "2h")
	t.Setenv("DATABASE_MAX_CONN_IDLE_TIME", "15m")

	settings, err := loadDatabaseSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.URL != "postgres://example.invalid/stealth" || settings.MaxConns != 32 || settings.MinConns != 4 || settings.MaxConnLifetime != 2*time.Hour || settings.MaxConnIdleTime != 15*time.Minute {
		t.Fatalf("unexpected database settings: %+v", settings)
	}
	var config Config
	settings.apply(&config)
	if config.DatabaseURL != settings.URL || config.DatabaseMaxConns != settings.MaxConns {
		t.Fatalf("settings were not applied: %+v", config)
	}
}

func TestLoadDatabaseSettingsRequiresURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := loadDatabaseSettings(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("missing database URL returned %v", err)
	}
}

func TestApplyDatabaseDefaults(t *testing.T) {
	config := Config{
		DatabaseMaxConns:        999,
		DatabaseMinConns:        1000,
		DatabaseMaxConnLifetime: -time.Second,
		DatabaseMaxConnIdleTime: 0,
	}
	config.applyDatabaseDefaults()
	if config.DatabaseMaxConns != 256 || config.DatabaseMinConns != 256 || config.DatabaseMaxConnLifetime != time.Hour || config.DatabaseMaxConnIdleTime != 30*time.Minute {
		t.Fatalf("unexpected database defaults: %+v", config)
	}
}
