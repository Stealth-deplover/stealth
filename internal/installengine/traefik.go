package installengine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const (
	traefikPublicHostPlaceholder             = "__STEALTH_PUBLIC_HOST__"
	traefikCloudflaredTrustedCIDRPlaceholder = "__STEALTH_CLOUDFLARED_TRUSTED_CIDR__"
	traefikSecurityHeadersMiddleware         = "stealth-security-headers"
	traefikAdminRealtimeRouter               = "stealth-admin-realtime"
	traefikProjectRealtimeRouter             = "stealth-project-realtime"
	// These IDs are part of the bind-mount contract between the production
	// Compose state initializer and worker image. They are deliberately fixed
	// rather than operator-configurable.
	StealthRuntimeUID = 10001
	StealthRuntimeGID = 10001
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
	root := filepath.Clean(strings.TrimSpace(layout.Root))
	if root == "" || root == string(filepath.Separator) {
		return errors.New("refusing filesystem root as Traefik installation root")
	}
	expected := map[string]string{
		"TraefikDir":          filepath.Join(root, "traefik"),
		"TraefikDynamic":      filepath.Join(root, "traefik", "dynamic"),
		"TraefikStatic":       filepath.Join(root, "traefik", "traefik.yaml"),
		"TraefikCore":         filepath.Join(root, "traefik", "dynamic", "core.yaml"),
		"TraefikGenerated":    filepath.Join(root, "traefik", "dynamic", "generated"),
		"TraefikReloadMarker": filepath.Join(root, "traefik", "dynamic", ".reload.yaml"),
	}
	for name, actual := range map[string]string{
		"TraefikDir":          layout.TraefikDir,
		"TraefikDynamic":      layout.TraefikDynamic,
		"TraefikStatic":       layout.TraefikStatic,
		"TraefikCore":         layout.TraefikCore,
		"TraefikGenerated":    layout.TraefikGenerated,
		"TraefikReloadMarker": layout.TraefikReloadMarker,
	} {
		if filepath.Clean(strings.TrimSpace(actual)) != expected[name] || !pathWithinInstallRoot(root, actual) {
			return fmt.Errorf("Traefik %s path escapes the installation root", name)
		}
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect installation root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installation root %q is not a normal directory", root)
	}
	for _, path := range []string{layout.TraefikDir, layout.TraefikDynamic, layout.TraefikGenerated} {
		if err := ensureTraefikDirectory(root, path); err != nil {
			return err
		}
	}
	markerInfo, err := os.Lstat(layout.TraefikReloadMarker)
	if err == nil {
		if !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Traefik reload marker %q is not a normal file", layout.TraefikReloadMarker)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Traefik reload marker: %w", err)
	}

	for _, path := range []string{layout.TraefikStatic, layout.TraefikCore} {
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("inspect release-managed Traefik file %q: %w", path, statErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release-managed Traefik file %q is not a normal file", path)
		}
	}
	return nil
}

func ensureTraefikDirectory(root, target string) error {
	if !pathWithinInstallRoot(root, target) {
		return fmt.Errorf("Traefik directory %q escapes the installation root", target)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(target, 0o755); err != nil {
			return fmt.Errorf("create Traefik directory %q: %w", target, err)
		}
		info, err = os.Lstat(target)
	}
	if err != nil {
		return fmt.Errorf("inspect Traefik directory %q: %w", target, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Traefik path %q is not a normal directory", target)
	}
	return nil
}

func pathWithinInstallRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return target != root
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
		traefikAdminRealtimeRouter + ":",
		traefikProjectRealtimeRouter + ":",
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
	if strings.Contains(text, "buffering:") || strings.Contains(text, "maxRequestBodyBytes") || strings.Contains(text, "stealth-request-body-limit") {
		return errors.New("Traefik core routes must not buffer complete request bodies")
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
