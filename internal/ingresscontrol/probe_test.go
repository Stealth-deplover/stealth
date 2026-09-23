package ingresscontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newProbeServer(t *testing.T, mutate func(http.ResponseWriter, *http.Request)) (*HTTPProbe, string) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		if mutate != nil {
			mutate(w, r)
		}
		if w.Header().Get("Location") != "" {
			w.WriteHeader(http.StatusFound)
			return
		}
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("console document"))
		case "/v1/account":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		}
	}))
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	p := NewHTTPProbe()
	p.client = &http.Client{Transport: transport, Timeout: server.Client().Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	p.lookup = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("8.8.8.8")}, nil }
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = "localhost:" + parsed.Port()
	return p, parsed.String()
}

func TestPublicConsoleHTTPSHeadersHSTSAndParity(t *testing.T) {
	probe, publicURL := newProbeServer(t, nil)
	baseline, err := probe.CapturePublicConsole(context.Background(), publicURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Routes) != len(publicConsolePaths) || baseline.Routes["/v1/account"].StatusCode != http.StatusUnauthorized {
		t.Fatalf("public baseline = %#v", baseline)
	}
	if _, err := probe.VerifyPublicConsole(context.Background(), publicURL, &baseline); err != nil {
		t.Fatalf("same public origin failed parity verification: %v", err)
	}
}

func TestPublicConsoleFailsWhenHSTSOrSecurityHeaderIsMissing(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(http.ResponseWriter, *http.Request)
		want   string
	}{
		{"HSTS", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Del("Strict-Transport-Security")
			}
		}, "HSTS"},
		{"browser security", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Del("Content-Security-Policy")
			}
		}, "security header"},
		{"weaker browser policy", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Security-Policy", "default-src 'self'")
			}
		}, "security header"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, publicURL := newProbeServer(t, test.mutate)
			_, err := probe.CapturePublicConsole(context.Background(), publicURL)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("CapturePublicConsole error = %v", err)
			}
		})
	}
}

func TestPublicConsoleRejectsHTTPDowngradeRedirect(t *testing.T) {
	probe, publicURL := newProbeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "http://cloud.example.com/login")
		}
	})
	_, err := probe.CapturePublicConsole(context.Background(), publicURL)
	if err == nil || !strings.Contains(err.Error(), "redirects away") {
		t.Fatalf("downgrade error = %v", err)
	}
}

func TestPublicConsoleRejectsPrivateDNS(t *testing.T) {
	probe, publicURL := newProbeServer(t, nil)
	probe.lookup = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.7")}, nil }
	if _, err := probe.CapturePublicConsole(context.Background(), publicURL); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("private DNS error = %v", err)
	}
}

func TestHSTSRequiresOneYearAndIncludeSubdomains(t *testing.T) {
	for _, value := range []string{
		"max-age=31535999; includeSubDomains",
		"max-age=31536000",
		"includeSubDomains; max-age=0",
	} {
		if err := requireHSTS(value); err == nil {
			t.Errorf("requireHSTS(%q) unexpectedly passed", value)
		}
	}
	if err := requireHSTS("max-age=31536000; includeSubDomains"); err != nil {
		t.Fatalf("valid HSTS rejected: %v", err)
	}
}

func TestParsePublicURLRejectsUnsafeTargets(t *testing.T) {
	for _, value := range []string{"http://cloud.example.com", "https://127.0.0.1", "https://user:pass@cloud.example.com", "https://cloud.example.com/path", "file:///etc/passwd"} {
		if _, err := parsePublicURL(value); err == nil {
			t.Errorf("parsePublicURL(%q) unexpectedly passed", value)
		}
	}
}

func TestPublicSiteProbeUsesHTTPSAndOptionalDigest(t *testing.T) {
	const body = "deterministic platform Site response"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	probe := NewHTTPProbe()
	probe.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}
	probe.lookup = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("8.8.8.8")}, nil }
	digest := sha256.Sum256([]byte(body))
	wantDigest := hex.EncodeToString(digest[:])
	if err := probe.VerifyPublicSite(context.Background(), "portfolio.apps.example.com", "apps.example.com", wantDigest); err != nil {
		t.Fatalf("public platform Site verification: %v", err)
	}
	if err := probe.VerifyPublicSite(context.Background(), "portfolio.apps.example.com", "apps.example.com", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("wrong Site digest error = %v", err)
	}
}
