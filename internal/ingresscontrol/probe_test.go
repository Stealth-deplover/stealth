package ingresscontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func newProbeServer(t *testing.T, mutate func(http.ResponseWriter, *http.Request)) (*HTTPProbe, string) {
	t.Helper()
	return newProbeServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
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
	})
}

func newProbeServerWithHandler(t *testing.T, handler func(http.ResponseWriter, *http.Request)) (*HTTPProbe, string) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	listenerAddress := server.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, listenerAddress)
	}
	p := NewHTTPProbe()
	p.client = &http.Client{Transport: transport, Timeout: server.Client().Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	p.lookup = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("8.8.8.8")}, nil }
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = "cloud.example.com:" + parsed.Port()
	return p, parsed.String()
}

type localTraefikRequests struct {
	mu    sync.Mutex
	paths []string
}

func (requests *localTraefikRequests) append(path string) {
	requests.mu.Lock()
	defer requests.mu.Unlock()
	requests.paths = append(requests.paths, path)
}

func (requests *localTraefikRequests) snapshot() []string {
	requests.mu.Lock()
	defer requests.mu.Unlock()
	return append([]string(nil), requests.paths...)
}

func newLocalTraefikProbe(t *testing.T, handler func(http.ResponseWriter, *http.Request)) (*HTTPProbe, *localTraefikRequests) {
	t.Helper()
	paths := &localTraefikRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths.append(r.URL.Path)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	listenerAddress := server.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, listenerAddress)
	}
	probe := NewHTTPProbe()
	probe.client = &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return probe, paths
}

func TestLocalTraefikAcceptsOnlySafeConsoleRootRedirect(t *testing.T) {
	for _, location := range []string{"/organizations", "https://cloud.example.com/organizations"} {
		t.Run(location, func(t *testing.T) {
			probe, paths := newLocalTraefikProbe(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.Header().Set("Location", location)
					w.WriteHeader(http.StatusTemporaryRedirect)
				case "/organizations":
					w.WriteHeader(http.StatusOK)
				case "/v1/account":
					w.WriteHeader(http.StatusUnauthorized)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})
			if err := probe.LocalTraefik(context.Background(), "cloud.example.com"); err != nil {
				t.Fatalf("safe local Console redirect failed: %v", err)
			}
			got := paths.snapshot()
			if strings.Join(got, ",") != "/,/organizations,/v1/account" {
				t.Fatalf("local preflight paths = %#v", got)
			}
		})
	}
}

func TestLocalTraefikRejectsExternalConsoleRootRedirect(t *testing.T) {
	probe, paths := newLocalTraefikProbe(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "https://evil.example.com/organizations")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := probe.LocalTraefik(context.Background(), "cloud.example.com"); err == nil {
		t.Fatal("unsafe local Traefik redirect was accepted")
	}
	if got := paths.snapshot(); len(got) != 1 {
		t.Fatalf("local preflight followed an external redirect: %#v", got)
	}
}

func TestPublicConsoleFollowsBoundedSameOriginHTTPSRedirects(t *testing.T) {
	tests := []struct {
		name     string
		location func(*http.Request) string
		status   int
	}{
		{name: "relative 307", status: http.StatusTemporaryRedirect, location: func(*http.Request) string { return "/organizations" }},
		{name: "absolute 308", status: http.StatusPermanentRedirect, location: func(r *http.Request) string { return "https://" + r.Host + "/organizations" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, publicURL := newProbeServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.Header().Set("Location", test.location(r))
					w.WriteHeader(test.status)
				case "/organizations":
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("organizations page"))
				case "/v1/account":
					w.WriteHeader(http.StatusUnauthorized)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})
			evidence, err := probe.CapturePublicConsole(context.Background(), publicURL)
			if err != nil {
				t.Fatalf("safe Console redirect failed: %v", err)
			}
			root := evidence.Routes["/"]
			if root.StatusCode != test.status || root.FinalStatusCode != http.StatusOK || root.FinalPath != "/organizations" {
				t.Fatalf("Console redirect evidence = %#v", root)
			}
			if len(root.RedirectChain) != 1 || root.RedirectChain[0].Path != "/organizations" {
				t.Fatalf("Console redirect chain = %#v", root.RedirectChain)
			}
		})
	}
}

func TestPublicConsoleRejectsUnsafeOrUnboundedRedirects(t *testing.T) {
	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{name: "HTTP downgrade", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "http://localhost/organizations")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
		{name: "cross host", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "https://evil.example.com/")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
		{name: "IP literal", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "https://8.8.8.8/")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
		{name: "embedded credentials", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "https://user@cloud.example.com/organizations")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
		{name: "noncanonical port", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "https://cloud.example.com:0443/organizations")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
		{name: "different HTTPS port", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "https://cloud.example.com:444/organizations")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
		{name: "malformed URL escape", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "https://cloud.example.com/%zz")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
		{name: "loop", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "/organizations")
				w.WriteHeader(http.StatusTemporaryRedirect)
			} else if r.URL.Path == "/organizations" {
				w.Header().Set("Location", "/")
				w.WriteHeader(http.StatusPermanentRedirect)
			}
		}},
		{name: "too many redirects", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v6" {
				w.WriteHeader(http.StatusOK)
				return
			}
			step := 0
			if r.URL.Path != "/" {
				_, _ = fmt.Sscanf(r.URL.Path, "/r%d", &step)
			}
			w.Header().Set("Location", fmt.Sprintf("/r%d", step+1))
			w.WriteHeader(http.StatusTemporaryRedirect)
		}},
		{name: "final 500", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Location", "/organizations")
				w.WriteHeader(http.StatusTemporaryRedirect)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{name: "redirect response missing HSTS", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Del("Strict-Transport-Security")
				w.Header().Set("Location", "/organizations")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, publicURL := newProbeServerWithHandler(t, test.handler)
			if _, err := probe.CapturePublicConsole(context.Background(), publicURL); err == nil {
				t.Fatal("unsafe redirect behavior unexpectedly passed")
			}
		})
	}
}

func TestPublicConsoleParityComparesNormalizedRedirectBehavior(t *testing.T) {
	baselineProbe, publicURL := newProbeServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "/organizations")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/organizations" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("organizations page"))
			return
		}
		if r.URL.Path == "/v1/account" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	baseline, err := baselineProbe.CapturePublicConsole(context.Background(), publicURL)
	if err != nil {
		t.Fatalf("baseline capture failed: %v", err)
	}
	afterProbe, afterURL := newProbeServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "https://"+r.Host+"/organizations")
			w.WriteHeader(http.StatusPermanentRedirect)
			return
		}
		if r.URL.Path == "/organizations" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("organizations page"))
			return
		}
		if r.URL.Path == "/v1/account" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	if _, err := afterProbe.VerifyPublicConsole(context.Background(), afterURL, &baseline); err != nil {
		t.Fatalf("equivalent relative/absolute redirect behavior failed parity: %v", err)
	}
	changedProbe, changedURL := newProbeServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "/dashboard")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/v1/account" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if _, err := changedProbe.VerifyPublicConsole(context.Background(), changedURL, &baseline); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("changed redirect target should fail parity, got %v", err)
	}
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
	if err == nil || !strings.Contains(err.Error(), "redirect") {
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
