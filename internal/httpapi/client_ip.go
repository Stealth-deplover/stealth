package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

const (
	maxForwardedHeaderBytes = 4096
	maxForwardedHops        = 32
)

// requestClientIP resolves the address used by rate limits. A forwarded
// header is only considered when the direct peer is in the explicit trusted
// proxy list; otherwise the peer address remains authoritative.
func (s *Server) requestClientIP(r *http.Request) string {
	return resolveClientIP(r, s.config.TrustedProxyCIDRs)
}

func resolveClientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	if r == nil {
		return "unknown"
	}
	remoteIP, remote := parseRemoteAddr(r.RemoteAddr)
	if remoteIP == nil || !trustedProxyContains(remoteIP, trustedProxies) {
		return remote
	}

	// This order matches the current Nginx deployment contract. X-Forwarded-
	// For is the accumulated hop chain, Forwarded is the standards-based
	// fallback, and X-Real-IP is the single-hop compatibility fallback.
	for _, header := range []struct {
		name  string
		parse func(string, []*net.IPNet) (net.IP, bool)
	}{
		{name: "X-Forwarded-For", parse: parseXForwardedFor},
		{name: "Forwarded", parse: parseForwarded},
		{name: "X-Real-IP", parse: parseXRealIP},
	} {
		raw, present, valid := joinedHeader(r, header.name)
		if !present {
			continue
		}
		if !valid {
			return remote
		}
		clientIP, parsed := header.parse(raw, trustedProxies)
		if !parsed {
			return remote
		}
		if clientIP != nil {
			return canonicalIP(clientIP).String()
		}
		return remote
	}
	return remote
}

func joinedHeader(r *http.Request, name string) (string, bool, bool) {
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", false, true
	}
	joined := strings.Join(values, ",")
	return joined, true, len(joined) <= maxForwardedHeaderBytes
}

func parseRemoteAddr(raw string) (net.IP, string) {
	value := strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	if ip := net.ParseIP(value); ip != nil {
		ip = canonicalIP(ip)
		return ip, ip.String()
	}
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
		return nil, "unknown"
	}
	return nil, value
}

func trustedProxyContains(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func canonicalIP(ip net.IP) net.IP {
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4
	}
	return ip.To16()
}

func parseXForwardedFor(raw string, trustedProxies []*net.IPNet) (net.IP, bool) {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 || len(parts) > maxForwardedHops {
		return nil, false
	}
	chain := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		ip := net.ParseIP(value)
		if value == "" || ip == nil {
			return nil, false
		}
		chain = append(chain, canonicalIP(ip))
	}
	return firstUntrustedForwardedIP(chain, trustedProxies), true
}

func parseForwarded(raw string, trustedProxies []*net.IPNet) (net.IP, bool) {
	elements, ok := splitForwardedList(raw, ',')
	if !ok || len(elements) == 0 || len(elements) > maxForwardedHops {
		return nil, false
	}
	chain := make([]net.IP, 0, len(elements))
	for _, element := range elements {
		ip, valid := parseForwardedElement(element)
		if !valid {
			return nil, false
		}
		chain = append(chain, ip)
	}
	return firstUntrustedForwardedIP(chain, trustedProxies), true
}

func parseForwardedElement(raw string) (net.IP, bool) {
	parameters, ok := splitForwardedList(raw, ';')
	if !ok {
		return nil, false
	}
	var clientIP net.IP
	foundFor := false
	for _, parameter := range parameters {
		key, value, found := strings.Cut(parameter, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !found || key == "" {
			return nil, false
		}
		if key != "for" {
			continue
		}
		if foundFor {
			return nil, false
		}
		var valid bool
		clientIP, valid = parseForwardedNode(value)
		if !valid {
			return nil, false
		}
		foundFor = true
	}
	return clientIP, foundFor
}

func parseForwardedNode(raw string) (net.IP, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, false
	}
	if value[0] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return nil, false
		}
		value = unquoted
	}
	if strings.HasPrefix(value, "[") {
		closing := strings.IndexByte(value, ']')
		if closing < 0 {
			return nil, false
		}
		if suffix := value[closing+1:]; suffix != "" {
			if !strings.HasPrefix(suffix, ":") || !validForwardedPort(suffix[1:]) {
				return nil, false
			}
		}
		value = value[1:closing]
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return nil, false
	}
	return canonicalIP(ip), true
}

func validForwardedPort(raw string) bool {
	if raw == "" || len(raw) > 5 {
		return false
	}
	port, err := strconv.ParseUint(raw, 10, 16)
	return err == nil && port <= 65535
}

func parseXRealIP(raw string, _ []*net.IPNet) (net.IP, bool) {
	value := strings.TrimSpace(raw)
	if strings.Contains(value, ",") {
		return nil, false
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return nil, false
	}
	return canonicalIP(ip), true
}

func firstUntrustedForwardedIP(chain []net.IP, trustedProxies []*net.IPNet) net.IP {
	for index := len(chain) - 1; index >= 0; index-- {
		if !trustedProxyContains(chain[index], trustedProxies) {
			return chain[index]
		}
	}
	return nil
}

func splitForwardedList(raw string, delimiter byte) ([]string, bool) {
	if raw == "" || len(raw) > maxForwardedHeaderBytes {
		return nil, false
	}
	parts := make([]string, 0, 4)
	start := 0
	quoted := false
	escaped := false
	for index := 0; index < len(raw); index++ {
		character := raw[index]
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				quoted = false
			}
			continue
		}
		if character == '"' {
			quoted = true
			continue
		}
		if character == delimiter {
			part := strings.TrimSpace(raw[start:index])
			if part == "" {
				return nil, false
			}
			parts = append(parts, part)
			start = index + 1
		}
	}
	if quoted || escaped {
		return nil, false
	}
	part := strings.TrimSpace(raw[start:])
	if part == "" {
		return nil, false
	}
	return append(parts, part), true
}
