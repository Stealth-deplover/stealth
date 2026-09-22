package config

import (
	"testing"
	"time"
)

func TestLoadIngressSettings(t *testing.T) {
	t.Setenv("PLATFORM_ROUTE_RECONCILE_INTERVAL", "15s")
	t.Setenv("TRAEFIK_GENERATED_DIR", "/var/lib/stealth/traefik/generated")
	t.Setenv("TRAEFIK_RELOAD_FILE", "/var/lib/stealth/traefik/.reload.yaml")

	settings, err := loadIngressSettings()
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	settings.apply(&config)
	if config.PlatformRouteReconcileInterval != 15*time.Second || config.TraefikGeneratedDir != "/var/lib/stealth/traefik/generated" || config.TraefikReloadFile != "/var/lib/stealth/traefik/.reload.yaml" {
		t.Fatalf("unexpected ingress settings: %+v", config)
	}
}

func TestLoadIngressSettingsRejectsUnsafeValues(t *testing.T) {
	t.Setenv("PLATFORM_ROUTE_RECONCILE_INTERVAL", "500ms")
	if _, err := loadIngressSettings(); err == nil {
		t.Fatal("sub-second reconcile interval was accepted")
	}
	t.Setenv("PLATFORM_ROUTE_RECONCILE_INTERVAL", "5s")
	t.Setenv("TRAEFIK_GENERATED_DIR", "relative/generated")
	if _, err := loadIngressSettings(); err == nil {
		t.Fatal("relative generated directory was accepted")
	}
}
