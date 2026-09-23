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

	"github.com/Stealth-deplover/stealth/internal/domainname"
)

const maxProbeBody = 64 << 10
const maxPublicRedirects = 5

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
	canonical, err := domainname.NormalizeHostname(hostname)
	if err != nil || net.ParseIP(canonical) != nil {
		return errors.New("Traefik preflight requires the configured Console DNS hostname")
	}
	hostname = canonical
	const target = "http://traefik:8080"
	rootStatus, locations, err := p.localTraefikRequest(ctx, target, hostname, "/")
	if err != nil {
		return err
	}
	if isRedirect(rootStatus) {
		origin := &url.URL{Scheme: "https", Host: hostname, Path: "/"}
		redirect, redirectErr := safeConsoleRedirect(origin, origin, locations)
		if redirectErr != nil || redirect.Path != "/organizations" || redirect.RawQuery != "" {
			return errors.New("local Traefik Console root returned an unexpected redirect")
		}
		rootStatus, _, err = p.localTraefikRequest(ctx, target, hostname, "/organizations")
		if err != nil {
			return err
		}
	}
	if rootStatus < http.StatusOK || rootStatus >= http.StatusMultipleChoices {
		return fmt.Errorf("local route %s/ returned HTTP %d, expected a Console document or safe redirect", target, rootStatus)
	}
	apiStatus, _, err := p.localTraefikRequest(ctx, target, hostname, "/v1/account")
	if err != nil {
		return err
	}
	if apiStatus != http.StatusUnauthorized {
		return fmt.Errorf("local route %s/v1/account returned HTTP %d, expected %d", target, apiStatus, http.StatusUnauthorized)
	}
	return nil
}

func (p *HTTPProbe) localTraefikRequest(ctx context.Context, target, hostname, path string) (int, []string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target+path, nil)
	if err != nil {
		return 0, nil, errors.New("could not construct Traefik preflight request")
	}
	request.Host = hostname
	response, err := p.client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("local route %s%s is unreachable", target, path)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeBody))
	_ = response.Body.Close()
	return response.StatusCode, response.Header.Values("Location"), nil
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
		routeEvidence, finalResponse, err := p.requestPublicRoute(ctx, base, path, addresses, path == "/" || path == "/v1/account")
		if err != nil {
			return PublicEvidence{}, err
		}
		if finalResponse.StatusCode >= http.StatusInternalServerError {
			return PublicEvidence{}, fmt.Errorf("public HTTPS route %s returned HTTP %d", path, finalResponse.StatusCode)
		}
		if path == "/" && (finalResponse.StatusCode < 200 || finalResponse.StatusCode >= 300) {
			return PublicEvidence{}, fmt.Errorf("public Console root ended at HTTP %d", finalResponse.StatusCode)
		}
		if path == "/v1/account" && finalResponse.StatusCode != http.StatusUnauthorized {
			return PublicEvidence{}, fmt.Errorf("public API route /v1/account ended at HTTP %d, expected 401 without credentials", finalResponse.StatusCode)
		}
		evidence.Routes[path] = routeEvidence
		if baseline != nil {
			if err := compareResponse(path, baseline.Routes[path], routeEvidence); err != nil {
				return PublicEvidence{}, err
			}
		}
	}
	return evidence, nil
}

// requestPublicRoute follows a small, explicit redirect chain. The initial
// public DNS answers stay pinned for every hop, and redirect destinations are
// resolved and validated against the configured origin before another request
// is issued. The default http.Client redirect policy remains disabled.
func (p *HTTPProbe) requestPublicRoute(ctx context.Context, base *url.URL, path string, addresses []net.IP, requirePolicy bool) (ResponseEvidence, *http.Response, error) {
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + path
	seen := map[string]struct{}{redirectLoopKey(&target): {}}
	evidence := ResponseEvidence{StatusCode: 0}
	for redirects := 0; ; {
		response, err := p.requestURL(ctx, &target, base.Hostname(), addresses, path)
		if err != nil {
			return ResponseEvidence{}, nil, err
		}
		if response.StatusCode >= http.StatusInternalServerError {
			return ResponseEvidence{}, nil, fmt.Errorf("public HTTPS route %s returned HTTP %d", path, response.StatusCode)
		}
		if requirePolicy {
			if err := requireBrowserSecurityHeaders(response.Header); err != nil {
				return ResponseEvidence{}, nil, fmt.Errorf("public HTTPS security header check failed on %s: %w", path, err)
			}
			if err := requireHSTS(response.Header.Get("Strict-Transport-Security")); err != nil {
				return ResponseEvidence{}, nil, fmt.Errorf("public HTTPS HSTS check failed on %s: %w", path, err)
			}
		}
		if evidence.StatusCode == 0 {
			evidence.StatusCode = response.StatusCode
		}
		if !isRedirect(response.StatusCode) {
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				return ResponseEvidence{}, nil, fmt.Errorf("public HTTPS route %s returned unsupported redirect status %d", path, response.StatusCode)
			}
			route := evidenceFromResponse(response)
			route.StatusCode = evidence.StatusCode
			route.RedirectChain = evidence.RedirectChain
			route.FinalStatusCode = response.StatusCode
			route.FinalPath = redirectEvidencePath(&target)
			return route, response, nil
		}
		if redirects >= maxPublicRedirects {
			return ResponseEvidence{}, nil, fmt.Errorf("public HTTPS route %s exceeded the %d redirect limit", path, maxPublicRedirects)
		}
		next, err := safeConsoleRedirect(&target, base, response.Header.Values("Location"))
		if err != nil {
			return ResponseEvidence{}, nil, fmt.Errorf("public HTTPS route %s has an unsafe redirect: %w", path, err)
		}
		key := redirectLoopKey(next)
		if _, exists := seen[key]; exists {
			return ResponseEvidence{}, nil, fmt.Errorf("public HTTPS route %s contains a redirect loop", path)
		}
		seen[key] = struct{}{}
		evidence.RedirectChain = append(evidence.RedirectChain, RedirectEvidence{
			StatusCode: response.StatusCode,
			Path:       redirectEvidencePath(next),
			Headers:    selectedResponseHeaders(response.Header, false),
		})
		target = *next
		redirects++
	}
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
	var addresses []net.IP
	if len(pinnedAddresses) > 0 {
		addresses = pinnedAddresses[0]
	}
	return p.requestURL(ctx, &target, base.Hostname(), addresses, path)
}

func (p *HTTPProbe) requestURL(ctx context.Context, target *url.URL, pinnedHost string, pinnedAddresses []net.IP, path string) (*http.Response, error) {
	if len(pinnedAddresses) > 0 {
		ctx = context.WithValue(ctx, publicDestinationContextKey{}, pinnedPublicDestination{host: pinnedHost, ips: pinnedAddresses})
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

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func safeConsoleRedirect(current, base *url.URL, locations []string) (*url.URL, error) {
	if len(locations) != 1 || strings.TrimSpace(locations[0]) == "" {
		return nil, errors.New("redirect must contain exactly one Location")
	}
	raw := strings.TrimSpace(locations[0])
	if strings.Contains(raw, "\\") || strings.ContainsAny(raw, "\r\n\x00") || strings.HasPrefix(raw, "//") {
		return nil, errors.New("redirect Location uses an unsafe URL form")
	}
	reference, err := url.Parse(raw)
	if err != nil || reference.Opaque != "" || reference.User != nil || reference.Fragment != "" {
		return nil, errors.New("redirect Location is malformed or contains credentials/fragment")
	}
	next := current.ResolveReference(reference)
	if !strings.EqualFold(next.Scheme, "https") || next.User != nil || next.Fragment != "" || next.Opaque != "" {
		return nil, errors.New("redirect must remain on HTTPS without credentials or fragments")
	}
	if !validHTTPSPort(next) || !validHTTPSPort(base) {
		return nil, errors.New("redirect HTTPS port is not canonical")
	}
	canonicalHost, err := domainname.NormalizeHostname(next.Hostname())
	if err != nil || net.ParseIP(canonicalHost) != nil || strings.EqualFold(canonicalHost, "localhost") {
		return nil, errors.New("redirect must use the configured DNS hostname")
	}
	baseHost, err := domainname.NormalizeHostname(base.Hostname())
	if err != nil || canonicalHost != baseHost {
		return nil, errors.New("redirect changed the configured Console hostname")
	}
	if effectiveHTTPSPort(next) != effectiveHTTPSPort(base) {
		return nil, errors.New("redirect changed the configured HTTPS port")
	}
	decodedPath, err := url.PathUnescape(next.EscapedPath() + "?" + next.RawQuery)
	if err != nil || strings.Contains(decodedPath, "\\") || strings.ContainsAny(decodedPath, "\r\n\x00") {
		return nil, errors.New("redirect target contains an unsafe path")
	}
	// Rewrite a case- or IDNA-equivalent authority to the exact configured
	// authority, keeping the pinned DNS address and TLS name unchanged.
	next.Host = base.Host
	next.Scheme = "https"
	if next.Path == "" {
		next.Path = "/"
	}
	return next, nil
}

func validHTTPSPort(target *url.URL) bool {
	if !strings.Contains(target.Host, ":") {
		return true
	}
	if strings.HasSuffix(target.Host, ":") {
		return false
	}
	port := target.Port()
	parsed, err := strconv.Atoi(port)
	return err == nil && parsed > 0 && parsed <= 65535 && strconv.Itoa(parsed) == port
}

func effectiveHTTPSPort(target *url.URL) string {
	if target.Port() == "" {
		return "443"
	}
	return target.Port()
}

func redirectLoopKey(target *url.URL) string {
	return "https://" + strings.ToLower(target.Host) + redirectEvidencePath(target)
}

func redirectEvidencePath(target *url.URL) string {
	path := target.EscapedPath()
	if path == "" {
		path = "/"
	}
	if target.RawQuery != "" {
		path += "?" + target.RawQuery
	}
	return path
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
	selected := selectedResponseHeaders(response.Header, true)
	return ResponseEvidence{
		StatusCode: response.StatusCode,
		Headers:    selected,
		BodySHA256: response.Header.Get("X-Stealth-Body-SHA256"),
	}
}

func selectedResponseHeaders(headers http.Header, includeLocation bool) map[string]string {
	selected := make(map[string]string)
	for _, key := range []string{
		"Content-Type", "Location", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy",
		"X-Frame-Options", "Content-Security-Policy", "Strict-Transport-Security",
	} {
		if key == "Location" && !includeLocation {
			continue
		}
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			selected[key] = value
		}
	}
	return selected
}

func compareResponse(path string, baseline, after ResponseEvidence) error {
	if baseline.StatusCode == 0 {
		return errors.New("public HTTPS baseline is incomplete; rerun `stealth ingress cutover`")
	}
	if len(baseline.RedirectChain) != len(after.RedirectChain) {
		return fmt.Errorf("public route %s changed its redirect chain after cutover", path)
	}
	for index := range baseline.RedirectChain {
		before, current := baseline.RedirectChain[index], after.RedirectChain[index]
		if before.Path != current.Path {
			return fmt.Errorf("public route %s changed its normalized redirect target after cutover", path)
		}
		for _, header := range []string{"X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy", "X-Frame-Options", "Content-Security-Policy", "Strict-Transport-Security"} {
			if !strings.EqualFold(strings.TrimSpace(before.Headers[header]), strings.TrimSpace(current.Headers[header])) {
				return fmt.Errorf("public route %s changed the %s header on a redirect after cutover", path, header)
			}
		}
	}
	if baseline.FinalStatusCode != after.FinalStatusCode {
		return fmt.Errorf("public route %s changed final HTTP status from %d to %d after cutover", path, baseline.FinalStatusCode, after.FinalStatusCode)
	}
	if baseline.FinalPath != after.FinalPath {
		return fmt.Errorf("public route %s changed its final path after cutover", path)
	}
	for _, header := range []string{"Content-Type", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy", "X-Frame-Options", "Content-Security-Policy", "Strict-Transport-Security"} {
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
