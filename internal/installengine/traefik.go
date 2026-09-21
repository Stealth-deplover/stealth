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

const (
	traefikPublicHostPlaceholder                   = "__STEALTH_PUBLIC_HOST__"
	traefikCloudflaredTrustedCIDRPlaceholder       = "__STEALTH_CLOUDFLARED_TRUSTED_CIDR__"
	traefikSecurityHeadersMiddleware               = "stealth-security-headers"
	traefikRequestBodyLimitMiddleware              = "stealth-request-body-limit"
	traefikAdminRealtimeRouter                     = "stealth-admin-realtime"
	traefikProjectRealtimeRouter                   = "stealth-project-realtime"
	traefikRequestBodyLimitBytes             int64 = 104857600
)

var expectedTraefikSecurityHeaders = map[string]string{
	"X-Content-Type-Options":  "nosniff",
	"Referrer-Policy":         "strict-origin-when-cross-origin",
	"Permissions-Policy":      "camera=(), microphone=(), geolocation=(), payment=()",
	"X-Frame-Options":         "DENY",
	"Content-Security-Policy": "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';",
}

func renderTraefikStaticAsset(contents []byte, plan Plan) ([]byte, error) {
	config, err := traefikIngressNetworkForPlan(plan)
	if err != nil {
		return nil, err
	}
	text := string(contents)
	if !strings.Contains(text, traefikCloudflaredTrustedCIDRPlaceholder) {
		return nil, errors.New("Traefik static configuration is missing the Cloudflared trusted-peer placeholder")
	}
	return []byte(strings.ReplaceAll(text, traefikCloudflaredTrustedCIDRPlaceholder, config.CloudflaredIP+"/32")), nil
}

func traefikIngressNetworkForPlan(plan Plan) (IngressNetworkConfig, error) {
	values, err := traefikConfigValuesForPlan(plan)
	if err != nil {
		return IngressNetworkConfig{}, err
	}
	return ingressNetworkConfigFromValues(values)
}

func traefikConfigValuesForPlan(plan Plan) (map[string]string, error) {
	contents := strings.TrimSpace(plan.ConfigContents)
	if contents == "" && plan.Layout.EnvFile != "" {
		values, err := ReadEnvFile(plan.Layout.EnvFile)
		if err == nil {
			return values, nil
		}
	}
	if contents == "" {
		return nil, errors.New("installation configuration is required to render Traefik assets")
	}
	values, err := ParseEnvContents(contents)
	if err != nil {
		return nil, fmt.Errorf("parse installation configuration: %w", err)
	}
	return values, nil
}

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
	if raw == "" {
		if values, err := traefikConfigValuesForPlan(plan); err == nil {
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
	if strings.Contains(text, traefikCloudflaredTrustedCIDRPlaceholder) {
		return errors.New("Traefik static configuration was not rendered with the configured Cloudflared peer")
	}
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
		"trustedIPs:",
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
	trustedIPs, ok := forwardedHeaders["trustedIPs"].([]any)
	if !ok || len(trustedIPs) != 1 {
		return errors.New("Traefik web entrypoint must trust exactly one rendered peer")
	}
	trustedCIDR, ok := trustedIPs[0].(string)
	if !ok {
		return errors.New("Traefik trusted peer is not a string CIDR")
	}
	trustedIP, trustedNetwork, err := net.ParseCIDR(trustedCIDR)
	if err != nil || trustedIP.To4() == nil {
		return errors.New("Traefik trusted peer is not a valid IPv4 CIDR")
	}
	ones, bits := trustedNetwork.Mask.Size()
	if bits != 32 || ones != 32 || !trustedNetwork.Contains(trustedIP) {
		return errors.New("Traefik trusted peer must be an exact IPv4 /32")
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
		"middlewares:",
		traefikSecurityHeadersMiddleware + ":",
		traefikRequestBodyLimitMiddleware + ":",
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
			Middlewares map[string]struct {
				Headers struct {
					CustomResponseHeaders map[string]string `yaml:"customResponseHeaders"`
				} `yaml:"headers"`
				Buffering struct {
					MaxRequestBodyBytes int64 `yaml:"maxRequestBodyBytes"`
				} `yaml:"buffering"`
			} `yaml:"middlewares"`
			Routers map[string]struct {
				EntryPoints []string `yaml:"entryPoints"`
				Rule        string   `yaml:"rule"`
				Service     string   `yaml:"service"`
				Middlewares []string `yaml:"middlewares"`
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
	securityHeaders, ok := config.HTTP.Middlewares[traefikSecurityHeadersMiddleware]
	if !ok {
		return fmt.Errorf("Traefik core middleware %q is missing", traefikSecurityHeadersMiddleware)
	}
	if len(securityHeaders.Headers.CustomResponseHeaders) != len(expectedTraefikSecurityHeaders) {
		return errors.New("Traefik security-header middleware must contain exactly the approved response headers")
	}
	for name, expected := range expectedTraefikSecurityHeaders {
		if got := securityHeaders.Headers.CustomResponseHeaders[name]; got != expected {
			return fmt.Errorf("Traefik security header %q = %q, want %q", name, got, expected)
		}
	}
	if _, hasHSTS := securityHeaders.Headers.CustomResponseHeaders["Strict-Transport-Security"]; hasHSTS {
		return errors.New("Traefik internal HTTP core routes must not emit unconditional HSTS")
	}
	bodyLimit, ok := config.HTTP.Middlewares[traefikRequestBodyLimitMiddleware]
	if !ok || bodyLimit.Buffering.MaxRequestBodyBytes != traefikRequestBodyLimitBytes {
		return fmt.Errorf("Traefik request body limit must be %d bytes", traefikRequestBodyLimitBytes)
	}
	for routerName, router := range config.HTTP.Routers {
		if strings.TrimSpace(router.Rule) == "" || !containsString(router.EntryPoints, "web") {
			return fmt.Errorf("Traefik core router %q is incomplete", routerName)
		}
		if !strings.Contains(router.Rule, "Host(`") {
			return fmt.Errorf("Traefik core router %q has no Host matcher", routerName)
		}
		if !containsString(router.Middlewares, traefikSecurityHeadersMiddleware) {
			return fmt.Errorf("Traefik core router %q is missing the release-managed security-header middleware", routerName)
		}
		streamingRouter := routerName == traefikAdminRealtimeRouter || routerName == traefikProjectRealtimeRouter
		if streamingRouter {
			if containsString(router.Middlewares, traefikRequestBodyLimitMiddleware) {
				return fmt.Errorf("Traefik streaming router %q must not use the buffering body-limit middleware", routerName)
			}
		} else if !containsString(router.Middlewares, traefikRequestBodyLimitMiddleware) {
			return fmt.Errorf("Traefik core router %q is missing the release-managed request-body middleware", routerName)
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
	apiRouter := config.HTTP.Routers["stealth-api"]
	if !strings.Contains(apiRouter.Rule, "PathPrefix(`/v1/`)") {
		return errors.New("Traefik public API router must be limited to the /v1/ prefix")
	}
	for _, path := range []string{"/healthz", "/readyz", "/version"} {
		if strings.Contains(apiRouter.Rule, path) {
			return errors.New("Traefik public API router exposes an internal health or version endpoint")
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
