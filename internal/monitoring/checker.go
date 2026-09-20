// Package monitoring owns the trusted execution side of instance monitors.
// Definitions are persisted by the API, but network probes run only in the
// worker process and return bounded protocol facts to the control plane.
package monitoring

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
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

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/repository"
)

const (
	maxHTTPResponseBytes = 1 << 20
	maxRedirects         = 5
	maxMonitorError      = 240
)

type monitorConfig struct {
	Method                string            `json:"method,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	Body                  string            `json:"body,omitempty"`
	ExpectedStatus        int               `json:"expected_status,omitempty"`
	BodyContains          string            `json:"body_contains,omitempty"`
	LatencyThresholdMS    int               `json:"latency_threshold_ms,omitempty"`
	Host                  string            `json:"host,omitempty"`
	Port                  int               `json:"port,omitempty"`
	RecordType            string            `json:"record_type,omitempty"`
	ExpectedValues        []string          `json:"expected_values,omitempty"`
	GraceSeconds          int               `json:"grace_seconds,omitempty"`
	CertificateExpiryDays int               `json:"certificate_expiry_days,omitempty"`
	TokenHash             string            `json:"token_hash,omitempty"`
}

// ValidateConfig validates the non-secret monitor definition before it is
// encrypted and stored. The worker repeats the checks after decrypting the
// config so a corrupt or old row cannot become an unrestricted network probe.
func ValidateConfig(kind, target string, config []byte) error {
	var value monitorConfig
	if len(config) == 0 || !json.Valid(config) || json.Unmarshal(config, &value) != nil {
		return errors.New("monitor configuration is invalid")
	}
	return validate(kind, target, value)
}

func Check(ctx context.Context, job repository.AdminMonitorJob, cipher *functionsecret.Cipher) repository.AdminMonitorCheckInput {
	started := time.Now()
	result := repository.AdminMonitorCheckInput{Details: json.RawMessage(`{}`)}
	if cipher == nil {
		result.Error = "monitor secret decryption is unavailable"
		result.LatencyMS = time.Since(started).Milliseconds()
		return result
	}
	plaintext, err := cipher.Decrypt(job.EncryptedConfig)
	if err != nil {
		result.Error = "monitor configuration could not be decrypted"
		result.LatencyMS = time.Since(started).Milliseconds()
		return result
	}
	var config monitorConfig
	if err := json.Unmarshal(plaintext, &config); err != nil || validate(job.Kind, job.Target, config) != nil {
		result.Error = "monitor configuration is invalid"
		result.LatencyMS = time.Since(started).Milliseconds()
		return result
	}

	probeContext, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutMS)*time.Millisecond)
	defer cancel()
	var checkErr error
	switch job.Kind {
	case "http":
		result.StatusCode, checkErr = checkHTTP(probeContext, job, config, &result.Details)
	case "tcp":
		checkErr = checkTCP(probeContext, job, config, &result.Details)
	case "dns":
		checkErr = checkDNS(probeContext, job, config, &result.Details)
	case "tls":
		checkErr = checkTLS(probeContext, job, config, &result.Details)
	case "heartbeat":
		checkErr = checkHeartbeat(time.Now().UTC(), job, config, &result.Details)
	case "synthetic":
		checkErr = errors.New("synthetic runner is not enabled")
	default:
		checkErr = errors.New("monitor type is unsupported")
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	if result.LatencyMS < 0 {
		result.LatencyMS = 0
	}
	if checkErr == nil {
		result.Success = true
		if config.LatencyThresholdMS > 0 && result.LatencyMS > int64(config.LatencyThresholdMS) {
			result.Success = false
			result.Error = "probe exceeded the latency threshold"
		}
	} else {
		result.Error = safeCheckError(checkErr)
	}
	return result
}

func validate(kind, target string, config monitorConfig) error {
	target = strings.TrimSpace(target)
	if target == "" || strings.ContainsAny(target, "\x00\r\n") {
		return errors.New("target is invalid")
	}
	if config.LatencyThresholdMS < 0 || config.LatencyThresholdMS > 120000 || config.GraceSeconds < 0 || config.GraceSeconds > 7*86400 || config.CertificateExpiryDays < 0 || config.CertificateExpiryDays > 3650 {
		return errors.New("threshold is invalid")
	}
	switch kind {
	case "http":
		parsed, err := url.Parse(target)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			return errors.New("HTTP target must be an absolute HTTP(S) URL")
		}
		method := strings.ToUpper(strings.TrimSpace(config.Method))
		if method == "" {
			method = http.MethodGet
		}
		if !validHTTPMethod(method) {
			return errors.New("HTTP method is invalid")
		}
		if config.ExpectedStatus < 0 || config.ExpectedStatus > 599 {
			return errors.New("expected HTTP status is invalid")
		}
		if len(config.Headers) > 32 || len(config.Body) > 1<<20 || len(config.BodyContains) > 512 {
			return errors.New("HTTP assertion or request is too large")
		}
		for key, value := range config.Headers {
			if !validHeader(key, value) {
				return errors.New("HTTP header is invalid")
			}
		}
	case "tcp", "tls":
		host, port, err := hostPort(target, config)
		if err != nil || host == "" || port < 1 || port > 65535 {
			return errors.New("network target is invalid")
		}
	case "dns":
		if !validDNSName(target) {
			return errors.New("DNS target is invalid")
		}
		recordType := strings.ToUpper(strings.TrimSpace(config.RecordType))
		if recordType == "" {
			recordType = "A"
		}
		if recordType != "A" && recordType != "AAAA" && recordType != "CNAME" && recordType != "TXT" {
			return errors.New("DNS record type is unsupported")
		}
		if len(config.ExpectedValues) > 32 {
			return errors.New("too many expected DNS values")
		}
	case "heartbeat":
		if len(config.TokenHash) != sha256.Size*2 || !isHex(config.TokenHash) {
			return errors.New("heartbeat token hash is invalid")
		}
	case "synthetic":
		return errors.New("synthetic runner is not enabled")
	default:
		return errors.New("monitor type is unsupported")
	}
	return nil
}

func checkHTTP(ctx context.Context, job repository.AdminMonitorJob, config monitorConfig, details *json.RawMessage) (*int, error) {
	method := strings.ToUpper(strings.TrimSpace(config.Method))
	if method == "" {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, job.Target, strings.NewReader(config.Body))
	if err != nil {
		return nil, errors.New("HTTP request could not be created")
	}
	for key, value := range config.Headers {
		request.Header.Set(key, value)
	}
	client := &http.Client{
		Timeout: time.Duration(job.TimeoutMS) * time.Millisecond,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           safeDialContext,
			TLSHandshakeTimeout:   time.Duration(job.TimeoutMS) * time.Millisecond,
			ResponseHeaderTimeout: time.Duration(job.TimeoutMS) * time.Millisecond,
			DisableCompression:    false,
			MaxIdleConnsPerHost:   2,
		},
	}
	redirects := 0
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		redirects++
		if redirects > maxRedirects {
			return errors.New("too many redirects")
		}
		if err := validatePublicURL(next.Context(), next.URL); err != nil {
			return err
		}
		return nil
	}
	if err := validatePublicURL(ctx, request.URL); err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, classifyNetworkError(err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxHTTPResponseBytes+1))
	if readErr != nil {
		return &response.StatusCode, errors.New("HTTP response could not be read")
	}
	if len(body) > maxHTTPResponseBytes {
		return &response.StatusCode, errors.New("HTTP response exceeded the monitor limit")
	}
	expectedStatus := config.ExpectedStatus
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}
	if response.StatusCode != expectedStatus {
		return &response.StatusCode, fmt.Errorf("HTTP endpoint returned status %d", response.StatusCode)
	}
	if config.BodyContains != "" && !strings.Contains(string(body), config.BodyContains) {
		return &response.StatusCode, errors.New("HTTP body assertion did not match")
	}
	encoded, _ := json.Marshal(map[string]any{"status_code": response.StatusCode, "content_length": len(body)})
	*details = encoded
	return &response.StatusCode, nil
}

func checkTCP(ctx context.Context, job repository.AdminMonitorJob, config monitorConfig, details *json.RawMessage) error {
	host, port, _ := hostPort(job.Target, config)
	connection, err := safeDialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return classifyNetworkError(err)
	}
	_ = connection.Close()
	encoded, _ := json.Marshal(map[string]any{"host": host, "port": port})
	*details = encoded
	return nil
}

func checkDNS(ctx context.Context, job repository.AdminMonitorJob, config monitorConfig, details *json.RawMessage) error {
	recordType := strings.ToUpper(strings.TrimSpace(config.RecordType))
	if recordType == "" {
		recordType = "A"
	}
	var values []string
	var err error
	resolver := net.DefaultResolver
	switch recordType {
	case "A", "AAAA":
		ips, lookupErr := resolver.LookupIP(ctx, strings.ToLower(recordType), job.Target)
		err = lookupErr
		for _, ip := range ips {
			if (recordType == "A" && ip.To4() != nil) || (recordType == "AAAA" && ip.To4() == nil) {
				values = append(values, ip.String())
			}
		}
	case "CNAME":
		var value string
		value, err = resolver.LookupCNAME(ctx, job.Target)
		if value != "" {
			values = []string{strings.TrimSuffix(value, ".")}
		}
	case "TXT":
		values, err = resolver.LookupTXT(ctx, job.Target)
	}
	if err != nil {
		return errors.New("DNS lookup failed")
	}
	if len(config.ExpectedValues) > 0 && !expectedDNSValuesPresent(values, config.ExpectedValues) {
		return errors.New("DNS values did not match")
	}
	encoded, _ := json.Marshal(map[string]any{"record_type": recordType, "record_count": len(values)})
	*details = encoded
	return nil
}

func checkTLS(ctx context.Context, job repository.AdminMonitorJob, config monitorConfig, details *json.RawMessage) error {
	host, port, _ := hostPort(job.Target, config)
	connection, err := safeDialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return classifyNetworkError(err)
	}
	tlsConnection := tls.Client(connection, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host})
	defer tlsConnection.Close()
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		return classifyNetworkError(err)
	}
	state := tlsConnection.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return errors.New("TLS endpoint did not present a certificate")
	}
	certificate := state.PeerCertificates[0]
	now := time.Now()
	if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return errors.New("TLS certificate is not currently valid")
	}
	days := int(time.Until(certificate.NotAfter).Hours() / 24)
	encoded, _ := json.Marshal(map[string]any{"expires_at": certificate.NotAfter.UTC(), "issuer": certificate.Issuer.String(), "days_remaining": days})
	*details = encoded
	if config.CertificateExpiryDays > 0 && days < config.CertificateExpiryDays {
		return errors.New("TLS certificate is approaching expiry")
	}
	return nil
}

func checkHeartbeat(now time.Time, job repository.AdminMonitorJob, config monitorConfig, details *json.RawMessage) error {
	if job.LastHeartbeatAt == nil {
		return errors.New("heartbeat has not been received")
	}
	age := now.Sub(job.LastHeartbeatAt.UTC())
	grace := time.Duration(config.GraceSeconds) * time.Second
	if age > time.Duration(job.IntervalSeconds)*time.Second+grace {
		encoded, _ := json.Marshal(map[string]any{"last_heartbeat_at": job.LastHeartbeatAt.UTC(), "age_seconds": int64(age.Seconds())})
		*details = encoded
		return errors.New("heartbeat is late")
	}
	encoded, _ := json.Marshal(map[string]any{"last_heartbeat_at": job.LastHeartbeatAt.UTC(), "age_seconds": int64(age.Seconds())})
	*details = encoded
	return nil
}

func hostPort(target string, config monitorConfig) (string, int, error) {
	host := strings.TrimSpace(config.Host)
	port := config.Port
	if host == "" {
		var err error
		var portString string
		host, portString, err = net.SplitHostPort(target)
		if err != nil {
			return "", 0, err
		}
		port, err = strconv.Atoi(portString)
		if err != nil {
			return "", 0, err
		}
	}
	return strings.Trim(host, "[]"), port, nil
}

func validatePublicURL(ctx context.Context, value *url.URL) error {
	return validatePublicURLWithResolver(ctx, net.DefaultResolver, value)
}

func validatePublicURLWithResolver(ctx context.Context, resolver ipResolver, value *url.URL) error {
	if value == nil || (value.Scheme != "http" && value.Scheme != "https") || value.Hostname() == "" || value.User != nil || value.Fragment != "" {
		return errors.New("monitor URL is invalid")
	}
	if _, err := resolvePublicHostWithResolver(ctx, resolver, value.Hostname()); err != nil {
		return err
	}
	return nil
}

// ValidatePublicHTTPSURL is shared by trusted outbound workers such as alert
// notifications. It preserves the monitor SSRF boundary while allowing the
// query parameters used by provider webhook endpoints.
func ValidatePublicHTTPSURL(ctx context.Context, value *url.URL) error {
	return validatePublicHTTPSURLWithResolver(ctx, net.DefaultResolver, value)
}

func validatePublicHTTPSURLWithResolver(ctx context.Context, resolver ipResolver, value *url.URL) error {
	if value == nil || value.Scheme != "https" || value.Hostname() == "" || value.User != nil || value.Fragment != "" {
		return errors.New("notification URL is invalid")
	}
	if _, err := resolvePublicHostWithResolver(ctx, resolver, value.Hostname()); err != nil {
		return err
	}
	return nil
}

// NewSafeHTTPSClient is the trusted outbound client for owner-configured
// notification endpoints. It resolves and dials public addresses only and
// validates every redirect, so a notification URL cannot become an SSRF
// primitive for the worker.
func NewSafeHTTPSClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           safeDialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConnsPerHost:   2,
		},
	}
	redirects := 0
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		redirects++
		if redirects > maxRedirects {
			return errors.New("too many redirects")
		}
		return ValidatePublicHTTPSURL(next.Context(), next.URL)
	}
	return client
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("monitor network address is invalid")
	}
	ips, err := resolvePublicHost(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("monitor host has no public address")
	}
	return nil, lastErr
}

type ipResolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

func resolvePublicHost(ctx context.Context, host string) ([]net.IP, error) {
	return resolvePublicHostWithResolver(ctx, net.DefaultResolver, host)
}

func resolvePublicHostWithResolver(ctx context.Context, resolver ipResolver, host string) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if isPublicIP(ip) {
			return []net.IP{ip}, nil
		}
		return nil, errors.New("monitor target resolves to a private address")
	}
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("monitor target could not be resolved")
	}
	if len(ips) == 0 {
		return nil, errors.New("monitor target could not be resolved")
	}
	public := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, errors.New("monitor target resolves to a private address")
		}
		public = append(public, ip)
	}
	return public, nil
}

func isPublicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	if address.Is4In6() {
		address = address.Unmap()
	}
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range monitorDeniedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// monitorDeniedPrefixes is the explicit monitor egress policy. The standard
// library's IsPrivate and IsGlobalUnicast methods intentionally do not cover
// every special-use allocation, so the monitor policy denies the documented
// shared, documentation, benchmarking, reserved, and non-global ranges here.
// The list is maintained against the IANA IPv4/IPv6 special-purpose registries
// and deliberately errs on the side of rejecting special-purpose destinations.
var monitorDeniedPrefixes = mustParseMonitorPrefixes([]string{
	"0.0.0.0/8",         // IPv4 "this network" and other unspecified uses.
	"10.0.0.0/8",        // RFC 1918 private.
	"100.64.0.0/10",     // RFC 6598 shared address space.
	"127.0.0.0/8",       // IPv4 loopback.
	"169.254.0.0/16",    // IPv4 link-local and cloud metadata endpoints.
	"172.16.0.0/12",     // RFC 1918 private.
	"192.0.0.0/24",      // IETF protocol assignments.
	"192.0.2.0/24",      // TEST-NET-1 documentation.
	"192.31.196.0/24",   // AS112-v4.
	"192.52.193.0/24",   // AMT.
	"192.88.99.0/24",    // 6to4 relay anycast (deprecated).
	"192.168.0.0/16",    // RFC 1918 private.
	"192.175.48.0/24",   // Direct Delegation AS112.
	"198.18.0.0/15",     // Benchmarking.
	"198.51.100.0/24",   // TEST-NET-2 documentation.
	"203.0.113.0/24",    // TEST-NET-3 documentation.
	"224.0.0.0/4",       // IPv4 multicast.
	"240.0.0.0/4",       // IPv4 reserved and future use.
	"::/128",            // IPv6 unspecified.
	"::1/128",           // IPv6 loopback.
	"100::/64",          // IPv6 discard-only.
	"100:0:0:1::/64",    // IPv6 dummy prefix.
	"2001::/23",         // IETF protocol assignments and special subranges.
	"2001:db8::/32",     // IPv6 documentation.
	"2002::/16",         // 6to4.
	"2620:4f:8000::/48", // Direct Delegation AS112.
	"3fff::/20",         // IPv6 documentation.
	"5f00::/16",         // Segment Routing SIDs.
	"fc00::/7",          // IPv6 unique local.
	"fe80::/10",         // IPv6 link-local.
	"ff00::/8",          // IPv6 multicast.
})

func mustParseMonitorPrefixes(values []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			panic("invalid monitor egress policy prefix: " + value)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func validHTTPMethod(value string) bool {
	switch value {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodOptions:
		return true
	default:
		return false
	}
}

func validHeader(key, value string) bool {
	return strings.TrimSpace(key) != "" && len(key) <= 128 && len(value) <= 4096 && !strings.ContainsAny(key+value, "\x00\r\n")
}

func validDNSName(value string) bool {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	if len(value) < 1 || len(value) > 253 || strings.Contains(value, "..") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

// expectedDNSValuesPresent implements the monitor contract: every configured
// expected value must be present, while additional DNS records are allowed.
// This is a subset check rather than an exact-set check because public DNS
// names commonly return additional healthy addresses over time.
func expectedDNSValuesPresent(actual, expected []string) bool {
	seen := make(map[string]struct{}, len(actual))
	for _, value := range actual {
		seen[strings.TrimSpace(strings.TrimSuffix(value, "."))] = struct{}{}
	}
	for _, value := range expected {
		if _, ok := seen[strings.TrimSpace(strings.TrimSuffix(value, "."))]; !ok {
			return false
		}
	}
	return true
}

func classifyNetworkError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New("monitor probe timed out")
	}
	return errors.New("monitor probe could not connect")
}

func safeCheckError(err error) string {
	value := strings.TrimSpace(err.Error())
	value = strings.Map(func(r rune) rune {
		if r == '\x00' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, value)
	if len(value) > maxMonitorError {
		return value[:maxMonitorError]
	}
	if value == "" {
		return "monitor probe failed"
	}
	return value
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}
