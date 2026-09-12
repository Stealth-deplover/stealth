package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"unicode"
)

// transportSettings owns the process listeners, Redis endpoint, metrics
// credential, and trusted proxy boundary. Keeping these values together makes
// the network-facing configuration contract explicit at the composition root.
type transportSettings struct {
	redisURL          string
	httpAddress       string
	metricsToken      string
	trustedProxyCIDRs []*net.IPNet
}

func loadTransportSettings() (transportSettings, error) {
	trustedProxyCIDRs, err := parseTrustedProxyCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return transportSettings{}, err
	}
	metricsToken := strings.TrimSpace(os.Getenv("METRICS_TOKEN"))
	if len(metricsToken) > 256 || strings.IndexFunc(metricsToken, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return transportSettings{}, fmt.Errorf("METRICS_TOKEN must be at most 256 characters and contain no whitespace or control characters")
	}
	return transportSettings{
		redisURL:          value("REDIS_URL", "redis://127.0.0.1:6379/0"),
		httpAddress:       value("HTTP_ADDR", ":8080"),
		metricsToken:      metricsToken,
		trustedProxyCIDRs: trustedProxyCIDRs,
	}, nil
}

func (s transportSettings) apply(c *Config) {
	c.RedisURL = s.redisURL
	c.HTTPAddress = s.httpAddress
	c.MetricsToken = s.metricsToken
	c.TrustedProxyCIDRs = cloneIPNetworks(s.trustedProxyCIDRs)
}

func cloneIPNetworks(networks []*net.IPNet) []*net.IPNet {
	if networks == nil {
		return nil
	}
	cloned := make([]*net.IPNet, len(networks))
	for index, network := range networks {
		if network == nil {
			continue
		}
		cloned[index] = &net.IPNet{
			IP:   append(net.IP(nil), network.IP...),
			Mask: append(net.IPMask(nil), network.Mask...),
		}
	}
	return cloned
}

// parseTrustedProxyCIDRs parses the direct peers that may provide sanitized
// client-IP forwarding headers. Bare IPs are accepted as host networks for
// small deployments; an empty value means trust nobody.
func parseTrustedProxyCIDRs(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 4096 {
		return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS must be at most 4096 characters")
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 64 {
		return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS must contain at most 64 networks")
	}
	networks := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS contains an empty entry")
		}
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ipv4 := ip.To4(); ipv4 != nil {
				ip = ipv4
				bits = 32
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil || network == nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS contains invalid IP or CIDR %q", value)
		}
		networks = append(networks, network)
	}
	return networks, nil
}
