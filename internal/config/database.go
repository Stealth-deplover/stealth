package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// databaseSettings owns the database connection environment contract. The
// application still receives one Config snapshot, but pool sizing and
// connection lifetime rules stay together instead of being interleaved with
// unrelated HTTP, auth, and storage settings.
type databaseSettings struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

func loadDatabaseSettings() (databaseSettings, error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return databaseSettings{}, fmt.Errorf("DATABASE_URL is required")
	}
	maxConns, err := boundedInt32("DATABASE_MAX_CONNS", "16", 1, 256)
	if err != nil {
		return databaseSettings{}, err
	}
	minConns, err := boundedInt32("DATABASE_MIN_CONNS", "2", 0, maxConns)
	if err != nil {
		return databaseSettings{}, err
	}
	maxConnLifetime, err := time.ParseDuration(value("DATABASE_MAX_CONN_LIFETIME", "1h"))
	if err != nil || maxConnLifetime <= 0 || maxConnLifetime > 7*24*time.Hour {
		return databaseSettings{}, fmt.Errorf("DATABASE_MAX_CONN_LIFETIME must be a positive duration no longer than 168h")
	}
	maxConnIdleTime, err := time.ParseDuration(value("DATABASE_MAX_CONN_IDLE_TIME", "30m"))
	if err != nil || maxConnIdleTime <= 0 || maxConnIdleTime > 7*24*time.Hour {
		return databaseSettings{}, fmt.Errorf("DATABASE_MAX_CONN_IDLE_TIME must be a positive duration no longer than 168h")
	}
	return databaseSettings{
		URL:             databaseURL,
		MaxConns:        maxConns,
		MinConns:        minConns,
		MaxConnLifetime: maxConnLifetime,
		MaxConnIdleTime: maxConnIdleTime,
	}, nil
}

func (s databaseSettings) apply(c *Config) {
	c.DatabaseURL = s.URL
	c.DatabaseMaxConns = s.MaxConns
	c.DatabaseMinConns = s.MinConns
	c.DatabaseMaxConnLifetime = s.MaxConnLifetime
	c.DatabaseMaxConnIdleTime = s.MaxConnIdleTime
}

func (c *Config) applyDatabaseDefaults() {
	if c.DatabaseMaxConns <= 0 {
		c.DatabaseMaxConns = 16
	}
	if c.DatabaseMaxConns > 256 {
		c.DatabaseMaxConns = 256
	}
	if c.DatabaseMinConns < 0 {
		c.DatabaseMinConns = 0
	}
	if c.DatabaseMinConns > c.DatabaseMaxConns {
		c.DatabaseMinConns = c.DatabaseMaxConns
	}
	if c.DatabaseMaxConnLifetime <= 0 {
		c.DatabaseMaxConnLifetime = time.Hour
	}
	if c.DatabaseMaxConnIdleTime <= 0 {
		c.DatabaseMaxConnIdleTime = 30 * time.Minute
	}
}
