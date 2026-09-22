package config

import (
	"fmt"
	"path/filepath"
	"time"
)

type ingressSettings struct {
	generatedDir       string
	reloadFile         string
	interval           time.Duration
	cloudflareInterval time.Duration
}

func loadIngressSettings() (ingressSettings, error) {
	interval, err := time.ParseDuration(value("PLATFORM_ROUTE_RECONCILE_INTERVAL", "5s"))
	if err != nil || interval < time.Second || interval > 5*time.Minute {
		return ingressSettings{}, fmt.Errorf("PLATFORM_ROUTE_RECONCILE_INTERVAL must be between 1s and 5m")
	}
	cloudflareInterval, err := time.ParseDuration(value("CLOUDFLARE_RECONCILE_INTERVAL", "1m"))
	if err != nil || cloudflareInterval < 15*time.Second || cloudflareInterval > 5*time.Minute {
		return ingressSettings{}, fmt.Errorf("CLOUDFLARE_RECONCILE_INTERVAL must be between 15s and 5m")
	}
	generatedDir := filepath.Clean(value("TRAEFIK_GENERATED_DIR", "/var/lib/stealth/traefik/generated"))
	reloadFile := filepath.Clean(value("TRAEFIK_RELOAD_FILE", "/var/lib/stealth/traefik/.reload.yaml"))
	if !filepath.IsAbs(generatedDir) || generatedDir == string(filepath.Separator) || !filepath.IsAbs(reloadFile) || reloadFile == string(filepath.Separator) {
		return ingressSettings{}, fmt.Errorf("TRAEFIK_GENERATED_DIR and TRAEFIK_RELOAD_FILE must be absolute non-root paths")
	}
	return ingressSettings{generatedDir: generatedDir, reloadFile: reloadFile, interval: interval, cloudflareInterval: cloudflareInterval}, nil
}

func (s ingressSettings) apply(c *Config) {
	c.TraefikGeneratedDir = s.generatedDir
	c.TraefikReloadFile = s.reloadFile
	c.PlatformRouteReconcileInterval = s.interval
	c.CloudflareReconcileInterval = s.cloudflareInterval
}

func (c *Config) applyIngressDefaults() {
	if c.TraefikGeneratedDir == "" {
		c.TraefikGeneratedDir = "/var/lib/stealth/traefik/generated"
	}
	if c.TraefikReloadFile == "" {
		c.TraefikReloadFile = "/var/lib/stealth/traefik/.reload.yaml"
	}
	if c.PlatformRouteReconcileInterval <= 0 {
		c.PlatformRouteReconcileInterval = 5 * time.Second
	}
	if c.CloudflareReconcileInterval <= 0 {
		c.CloudflareReconcileInterval = time.Minute
	}
}
