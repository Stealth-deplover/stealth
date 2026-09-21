package installengine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const traefikPublicHostPlaceholder = "__STEALTH_PUBLIC_HOST__"

func renderTraefikCoreAsset(contents []byte, plan Plan) ([]byte, error) {
	host, err := traefikPublicHost(plan)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(contents), traefikPublicHostPlaceholder) {
		return nil, errors.New("core route template is missing the public host placeholder")
	}
	return []byte(strings.ReplaceAll(string(contents), traefikPublicHostPlaceholder, host)), nil
}

func traefikPublicHost(plan Plan) (string, error) {
	raw := strings.TrimSpace(plan.PublicURL)
	if raw == "" && plan.Layout.EnvFile != "" {
		values, err := ReadEnvFile(plan.Layout.EnvFile)
		if err == nil {
			raw = strings.TrimSpace(values["PUBLIC_APP_URL"])
		}
	}
	if raw == "" {
		return "", errors.New("PUBLIC_APP_URL is required to render Traefik core routes")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("PUBLIC_APP_URL is invalid for Traefik core routes")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", errors.New("PUBLIC_APP_URL has no hostname for Traefik core routes")
	}
	if ip := net.ParseIP(host); ip != nil && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	for _, character := range host {
		if unicode.IsControl(character) || strings.ContainsRune("`\\\"'", character) {
			return "", errors.New("PUBLIC_APP_URL hostname contains unsupported characters")
		}
	}
	return host, nil
}

func ensureTraefikDirectories(layout Layout) error {
	for _, path := range []string{layout.TraefikDir, layout.TraefikDynamic, layout.TraefikGenerated} {
		if strings.TrimSpace(path) == "" {
			return errors.New("Traefik installation directories are incomplete")
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create Traefik directory %q: %w", path, err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect Traefik directory %q: %w", path, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Traefik path %q is not a normal directory", path)
		}
		if err := os.Chmod(path, 0o755); err != nil {
			return fmt.Errorf("protect Traefik directory %q: %w", path, err)
		}
	}
	if strings.TrimSpace(layout.TraefikReloadMarker) == "" {
		return errors.New("Traefik reload marker path is incomplete")
	}
	markerInfo, err := os.Lstat(layout.TraefikReloadMarker)
	if errors.Is(err, os.ErrNotExist) {
		if err := WriteAtomic(layout.TraefikReloadMarker, []byte("# Top-level file-provider reload sentinel.\n"), 0o644); err != nil {
			return fmt.Errorf("create Traefik reload marker: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Traefik reload marker: %w", err)
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Traefik reload marker %q is not a normal file", layout.TraefikReloadMarker)
	}
	if err := os.Chmod(layout.TraefikReloadMarker, 0o644); err != nil {
		return fmt.Errorf("protect Traefik reload marker: %w", err)
	}
	return nil
}

func validateTraefikStaticAsset(contents []byte) error {
	text := string(contents)
	var config map[string]any
	if err := decodeTraefikYAML(contents, &config); err != nil {
		return fmt.Errorf("Traefik static configuration is not valid YAML: %w", err)
	}
	for _, marker := range []string{
		"providers:",
		"    directory: /etc/traefik/dynamic",
		"dashboard: false",
		"insecure: false",
		"ping:",
		"entryPoint: health",
		"format: json",
		"Authorization: drop",
		"Cookie: drop",
	} {
		if !strings.Contains(text, marker) {
			return fmt.Errorf("missing Traefik static configuration marker %q", marker)
		}
	}
	if strings.Contains(text, "providers:\n  docker:") || strings.Contains(text, "forwardedHeaders:\n      insecure: true") {
		return errors.New("Traefik static configuration enables a forbidden provider or forwarded-header mode")
	}
	providers, ok := config["providers"].(map[string]any)
	if !ok {
		return errors.New("Traefik static configuration has no provider map")
	}
	if _, ok := providers["docker"]; ok {
		return errors.New("Traefik static configuration enables the Docker provider")
	}
	fileProvider, ok := providers["file"].(map[string]any)
	if !ok || fileProvider["directory"] != "/etc/traefik/dynamic" {
		return errors.New("Traefik static configuration has an invalid file provider")
	}
	api, ok := config["api"].(map[string]any)
	if !ok || api["dashboard"] != false || api["insecure"] != false {
		return errors.New("Traefik dashboard/API exposure is not explicitly disabled")
	}
	entryPoints, ok := config["entryPoints"].(map[string]any)
	if !ok {
		return errors.New("Traefik static configuration has no entrypoint map")
	}
	web, ok := entryPoints["web"].(map[string]any)
	if !ok {
		return errors.New("Traefik web entrypoint is missing")
	}
	forwardedHeaders, ok := web["forwardedHeaders"].(map[string]any)
	if ok && forwardedHeaders["insecure"] == true {
		return errors.New("Traefik web entrypoint trusts arbitrary forwarded headers")
	}
	health, ok := entryPoints["health"].(map[string]any)
	if !ok || health["address"] != ":8081" {
		return errors.New("Traefik health entrypoint is not private and explicit")
	}
	return nil
}

func validateTraefikCoreAsset(contents []byte) error {
	text := string(contents)
	if strings.Contains(text, traefikPublicHostPlaceholder) {
		return errors.New("Traefik core routes were not rendered with a public hostname")
	}
	for _, marker := range []string{
		"routers:",
		"stealth-api:",
		"stealth-console:",
		"PathPrefix(`/v1/`)",
		"url: http://api:8080",
		"url: http://console:3000",
	} {
		if !strings.Contains(text, marker) {
			return fmt.Errorf("missing Traefik core route marker %q", marker)
		}
	}
	var config struct {
		HTTP struct {
			Routers map[string]struct {
				EntryPoints []string `yaml:"entryPoints"`
				Rule        string   `yaml:"rule"`
				Service     string   `yaml:"service"`
			} `yaml:"routers"`
			Services map[string]struct {
				LoadBalancer struct {
					PassHostHeader bool `yaml:"passHostHeader"`
					Servers        []struct {
						URL string `yaml:"url"`
					} `yaml:"servers"`
				} `yaml:"loadBalancer"`
			} `yaml:"services"`
		} `yaml:"http"`
	}
	if err := decodeTraefikYAML(contents, &config); err != nil {
		return fmt.Errorf("Traefik core configuration is not valid YAML: %w", err)
	}
	for routerName, router := range config.HTTP.Routers {
		if strings.TrimSpace(router.Rule) == "" || !containsString(router.EntryPoints, "web") {
			return fmt.Errorf("Traefik core router %q is incomplete", routerName)
		}
		if !strings.Contains(router.Rule, "Host(`") {
			return fmt.Errorf("Traefik core router %q has no Host matcher", routerName)
		}
		service, ok := config.HTTP.Services[router.Service]
		if !ok || !service.LoadBalancer.PassHostHeader || len(service.LoadBalancer.Servers) != 1 {
			return fmt.Errorf("Traefik core router %q references an invalid service", routerName)
		}
		backend, err := url.Parse(service.LoadBalancer.Servers[0].URL)
		if err != nil || backend.Scheme != "http" || backend.Host == "" || backend.RawQuery != "" || backend.Fragment != "" {
			return fmt.Errorf("Traefik core router %q references an invalid backend", routerName)
		}
	}
	for _, routerName := range []string{"stealth-api", "stealth-console"} {
		if _, ok := config.HTTP.Routers[routerName]; !ok {
			return fmt.Errorf("Traefik core router %q is missing", routerName)
		}
	}
	if strings.Contains(text, "api@internal") || strings.Contains(text, "docker@internal") {
		return errors.New("Traefik core routes expose an internal dashboard or Docker service")
	}
	return nil
}

func decodeTraefikYAML(contents []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple YAML documents are not supported")
		}
		return err
	}
	return nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
