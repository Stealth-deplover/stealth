package ingresscontrol

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxProbeBody = 64 << 10

var requiredBrowserSecurityHeaders = []struct{ name, value string }{
	{"X-Content-Type-Options", "nosniff"},
	{"Referrer-Policy", "strict-origin-when-cross-origin"},
	{"Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()"},
	{"X-Frame-Options", "DENY"},
	{"Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';"},
}

var publicConsolePaths = []string{"/", "/v1/account", "/healthz", "/readyz", "/version", "/__stealth_ingress_acceptance_unknown__"}

var nonPublicAddressRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

type HTTPProbe struct {
	client *http.Client
	lookup func(context.Context, string) ([]net.IP, error)
}

type pinnedPublicDestination struct {
	host string
	ips  []net.IP
}

type publicDestinationContextKey struct{}

func NewHTTPProbe() *HTTPProbe {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	baseDial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		pinned, ok := ctx.Value(publicDestinationContextKey{}).(pinnedPublicDestination)
		host, port, splitErr := net.SplitHostPort(address)
		if !ok || splitErr != nil || !strings.EqualFold(host, pinned.host) {
			return baseDial(ctx, network, address)
		}
		for _, ip := range pinned.ips {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return connection, nil
			}
		}
		return nil, errors.New("public HTTPS connection failed for resolved address")
	}
	return &HTTPProbe{
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		lookup: func(ctx context.Context, hostname string) ([]net.IP, error) {
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
			if err != nil {
				return nil, err
			}
			ips := make([]net.IP, 0, len(addresses))
			for _, address := range addresses {
				ips = append(ips, address.IP)
			}
			return ips, nil
		},
	}
}

func (p *HTTPProbe) LocalTraefik(ctx context.Context, hostname string) error {
	if p == nil || p.client == nil {
		return errors.New("HTTP probe is unavailable")
	}
	for _, item := range []struct {
		target string
		host   string
		path   string
		want   int
	}{{target: "http://traefik:8080", host: hostname, path: "/", want: http.StatusOK},
		{target: "http://traefik:8080", host: hostname, path: "/v1/account", want: http.StatusUnauthorized}} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.target+item.path, nil)
		if err != nil {
			return errors.New("could not construct Traefik preflight request")
		}
		if item.host != "" {
			request.Host = item.host
		}
		response, err := p.client.Do(request)
		if err != nil {
			return fmt.Errorf("local route %s%s is unreachable", item.target, item.path)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeBody))
		_ = response.Body.Close()
		if response.StatusCode != item.want {
			return fmt.Errorf("local route %s%s returned HTTP %d, expected %d", item.target, item.path, response.StatusCode, item.want)
		}
	}
	return nil
}

func (p *HTTPProbe) CapturePublicConsole(ctx context.Context, publicURL string) (PublicEvidence, error) {
	return p.VerifyPublicConsole(ctx, publicURL, nil)
}

func (p *HTTPProbe) VerifyPublicConsole(ctx context.Context, publicURL string, baseline *PublicEvidence) (PublicEvidence, error) {
	base, err := parsePublicURL(publicURL)
	if err != nil {
		return PublicEvidence{}, err
	}
	addresses, err := p.publicDNS(ctx, base.Hostname())
	if err != nil {
		return PublicEvidence{}, err
	}
	evidence := PublicEvidence{Routes: make(map[string]ResponseEvidence, len(publicConsolePaths))}
	for _, path := range publicConsolePaths {
		response, err := p.request(ctx, base, path, addresses)
		if err != nil {
			return PublicEvidence{}, err
		}
		if response.StatusCode >= http.StatusInternalServerError {
			return PublicEvidence{}, fmt.Errorf("public HTTPS route %s returned HTTP %d", path, response.StatusCode)
		}
		if err := verifyNoDowngrade(response, base); err != nil {
			return PublicEvidence{}, err
		}
		if path == "/" && (response.StatusCode < 200 || response.StatusCode >= 300) {
			return PublicEvidence{}, fmt.Errorf("public Console root returned HTTP %d", response.StatusCode)
		}
		if path == "/v1/account" && response.StatusCode != http.StatusUnauthorized {
			return PublicEvidence{}, fmt.Errorf("public API route /v1/account returned HTTP %d, expected 401 without credentials", response.StatusCode)
		}
		if path == "/" || path == "/v1/account" {
			if err := requireBrowserSecurityHeaders(response.Header); err != nil {
				return PublicEvidence{}, fmt.Errorf("public HTTPS security header check failed on %s: %w", path, err)
			}
			if err := requireHSTS(response.Header.Get("Strict-Transport-Security")); err != nil {
				return PublicEvidence{}, fmt.Errorf("public HTTPS HSTS check failed on %s: %w", path, err)
			}
		}
		routeEvidence := evidenceFromResponse(response)
		evidence.Routes[path] = routeEvidence
		if baseline != nil {
			if err := compareResponse(path, baseline.Routes[path], routeEvidence); err != nil {
				return PublicEvidence{}, err
			}
		}
	}
	return evidence, nil
}

func (p *HTTPProbe) VerifyPublicSite(ctx context.Context, hostname, workloadBaseDomain, expectedSHA256 string) error {
	canonical, err := normalizePlatformSiteHostname(hostname, workloadBaseDomain)
	if err != nil || canonical != hostname {
		return errors.New("platform Site hostname is invalid")
	}
	addresses, err := p.publicDNS(ctx, hostname)
	if err != nil {
		return err
	}
	base := &url.URL{Scheme: "https", Host: hostname}
	response, err := p.request(ctx, base, "/", addresses)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("public platform Site returned HTTP %d", response.StatusCode)
	}
	if err := verifyNoDowngrade(response, base); err != nil {
		return err
	}
	if expectedSHA256 != "" {
		actual := evidenceFromResponse(response).BodySHA256
		if len(actual) != len(expectedSHA256) || subtle.ConstantTimeCompare([]byte(actual), []byte(strings.ToLower(expectedSHA256))) != 1 {
			return errors.New("public platform Site body SHA-256 did not match the requested digest")
		}
	}
	return nil
}

func (p *HTTPProbe) request(ctx context.Context, base *url.URL, path string, pinnedAddresses ...[]net.IP) (*http.Response, error) {
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + path
	if len(pinnedAddresses) > 0 && len(pinnedAddresses[0]) > 0 {
		ctx = context.WithValue(ctx, publicDestinationContextKey{}, pinnedPublicDestination{host: base.Hostname(), ips: pinnedAddresses[0]})
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("could not construct public HTTPS request")
	}
	request.Header.Set("Accept", "*/*")
	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("public DNS/TLS/HTTPS request failed for %s", path)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxProbeBody))
	_ = response.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("could not read bounded public HTTPS response for %s", path)
	}
	response.Body = io.NopCloser(strings.NewReader(string(body)))
	response.Header.Set("X-Stealth-Body-SHA256", digestBody(body))
	return response, nil
}

func (p *HTTPProbe) publicDNS(ctx context.Context, hostname string) ([]net.IP, error) {
	if p.lookup == nil {
		return nil, errors.New("public DNS resolver is unavailable")
	}
	ips, err := p.lookup(ctx, hostname)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("public DNS lookup failed for the configured hostname")
	}
	for _, ip := range ips {
		if !publicDestinationIP(ip) {
			return nil, errors.New("public hostname resolves to a non-public address; verification refused")
		}
	}
	return ips, nil
}

func publicDestinationIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicAddressRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func evidenceFromResponse(response *http.Response) ResponseEvidence {
	selected := make(map[string]string)
	for _, key := range []string{
		"Content-Type", "Location", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy",
		"X-Frame-Options", "Content-Security-Policy", "Strict-Transport-Security",
	} {
		if value := strings.TrimSpace(response.Header.Get(key)); value != "" {
			selected[key] = value
		}
	}
	return ResponseEvidence{
		StatusCode: response.StatusCode,
		Headers:    selected,
		BodySHA256: response.Header.Get("X-Stealth-Body-SHA256"),
	}
}

func compareResponse(path string, baseline, after ResponseEvidence) error {
	if baseline.StatusCode == 0 {
		return errors.New("public HTTPS baseline is incomplete; rerun `stealth ingress cutover`")
	}
	if baseline.StatusCode != after.StatusCode {
		return fmt.Errorf("public route %s changed from HTTP %d to HTTP %d after cutover", path, baseline.StatusCode, after.StatusCode)
	}
	for _, header := range []string{"Content-Type", "Location", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy", "X-Frame-Options", "Content-Security-Policy", "Strict-Transport-Security"} {
		if !strings.EqualFold(strings.TrimSpace(baseline.Headers[header]), strings.TrimSpace(after.Headers[header])) {
			return fmt.Errorf("public route %s changed the %s header after cutover", path, header)
		}
	}
	// API responses can contain request-specific metadata. Compare bounded body
	// fingerprints only for the Console document and unknown-route fallback.
	if (path == "/" || strings.HasPrefix(path, "/__stealth_ingress_acceptance_")) && baseline.BodySHA256 != after.BodySHA256 {
		return fmt.Errorf("public route %s body fingerprint changed after cutover", path)
	}
	return nil
}

func verifyNoDowngrade(response *http.Response, base *url.URL) error {
	if response.StatusCode < 300 || response.StatusCode >= 400 {
		return nil
	}
	location := strings.TrimSpace(response.Header.Get("Location"))
	if location == "" {
		return errors.New("public HTTPS response redirected without a Location")
	}
	redirect, err := base.Parse(location)
	if err != nil || redirect.Scheme != "https" || !strings.EqualFold(redirect.Hostname(), base.Hostname()) {
		return errors.New("public HTTPS response redirects away from the configured HTTPS Console hostname")
	}
	return nil
}

func requireBrowserSecurityHeaders(headers http.Header) error {
	for _, required := range requiredBrowserSecurityHeaders {
		if !strings.EqualFold(strings.TrimSpace(headers.Get(required.name)), required.value) {
			return fmt.Errorf("%s does not match the current production browser policy", required.name)
		}
	}
	return nil
}

func requireHSTS(raw string) error {
	maxAge := int64(-1)
	includeSubdomains := false
	for _, directive := range strings.Split(raw, ";") {
		parts := strings.SplitN(strings.TrimSpace(directive), "=", 2)
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		switch name {
		case "max-age":
			if len(parts) != 2 {
				return errors.New("Strict-Transport-Security max-age is invalid")
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
			if err != nil {
				return errors.New("Strict-Transport-Security max-age is invalid")
			}
			maxAge = parsed
		case "includesubdomains":
			includeSubdomains = len(parts) == 1
		}
	}
	if maxAge < 31_536_000 || !includeSubdomains {
		return errors.New("Strict-Transport-Security must include max-age of at least 31536000 and includeSubDomains")
	}
	return nil
}

func digestBody(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
