package httpapi

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveClientIPTrustedProxyPolicy(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		trusted    []string
		xff        string
		forwarded  string
		realIP     string
		want       string
	}{
		{
			name:       "untrusted peer ignores forwarded headers",
			remoteAddr: "198.51.100.10:443",
			trusted:    []string{"10.0.0.0/8"},
			xff:        "203.0.113.10",
			forwarded:  "for=203.0.113.11",
			realIP:     "203.0.113.12",
			want:       "198.51.100.10",
		},
		{
			name:       "trusted peer uses x forwarded for",
			remoteAddr: "10.1.2.3:443",
			trusted:    []string{"10.0.0.0/8"},
			xff:        "203.0.113.10",
			want:       "203.0.113.10",
		},
		{
			name:       "trusted chain selects rightmost untrusted hop",
			remoteAddr: "10.1.2.3:443",
			trusted:    []string{"10.0.0.0/8"},
			xff:        "203.0.113.10, 10.2.3.4",
			want:       "203.0.113.10",
		},
		{
			name:       "malformed x forwarded for falls back",
			remoteAddr: "10.1.2.3:443",
			trusted:    []string{"10.0.0.0/8"},
			xff:        "not-an-ip",
			forwarded:  "for=203.0.113.10",
			want:       "10.1.2.3",
		},
		{
			name:       "forwarded fallback is parsed",
			remoteAddr: "10.1.2.3:443",
			trusted:    []string{"10.0.0.0/8"},
			forwarded:  "for=203.0.113.10;proto=https, for=10.2.3.4",
			want:       "203.0.113.10",
		},
		{
			name:       "real ip fallback is parsed",
			remoteAddr: "10.1.2.3:443",
			trusted:    []string{"10.0.0.0/8"},
			realIP:     "203.0.113.10",
			want:       "203.0.113.10",
		},
		{
			name:       "trusted ipv6 peer",
			remoteAddr: "[2001:db8::2]:443",
			trusted:    []string{"2001:db8::/32"},
			xff:        "2001:db9::10",
			want:       "2001:db9::10",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			networks := make([]*net.IPNet, 0, len(test.trusted))
			for _, raw := range test.trusted {
				_, network, err := net.ParseCIDR(raw)
				if err != nil {
					t.Fatal(err)
				}
				networks = append(networks, network)
			}
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.RemoteAddr = test.remoteAddr
			if test.xff != "" {
				request.Header.Set("X-Forwarded-For", test.xff)
			}
			if test.forwarded != "" {
				request.Header.Set("Forwarded", test.forwarded)
			}
			if test.realIP != "" {
				request.Header.Set("X-Real-IP", test.realIP)
			}
			if got := resolveClientIP(request, networks); got != test.want {
				t.Fatalf("resolveClientIP() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveClientIPTrustsNoProxyByDefault(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.10:443"
	request.Header.Set("X-Forwarded-For", "203.0.113.10")
	if got := resolveClientIP(request, nil); got != "198.51.100.10" {
		t.Fatalf("default client IP = %q, want direct peer", got)
	}
}
