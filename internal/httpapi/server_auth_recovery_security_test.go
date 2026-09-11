package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/config"
)

func TestAuthLinkForRejectsUnsafeOrUntrustedRedirects(t *testing.T) {
	server := &Server{config: config.Config{PublicAppURL: "https://console.example.test"}}
	request := httptest.NewRequest(http.MethodPost, "/v1/account/recovery", nil)
	tests := []struct {
		name       string
		redirect   string
		wantError  bool
		wantOrigin string
	}{
		{
			name:       "same origin HTTPS",
			redirect:   "https://console.example.test/reset-password?next=%2Faccount",
			wantOrigin: "https://console.example.test",
		},
		{name: "untrusted HTTPS origin", redirect: "https://attacker.example/reset-password", wantError: true},
		{name: "javascript URL", redirect: "javascript:alert(1)", wantError: true},
		{name: "data URL", redirect: "data:text/html,alert(1)", wantError: true},
		{name: "HTTP origin when HTTPS is configured", redirect: "http://console.example.test/reset-password", wantError: true},
		{name: "CRLF injection", redirect: "https://console.example.test/reset\r\nBcc:attacker@example.test", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			link, err := server.authLinkFor(request, "reset-password", nil, "reset-token-123456", test.redirect)
			if test.wantError {
				if err == nil {
					t.Fatalf("authLinkFor(%q) returned %q, want an error", test.redirect, link)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(link)
			if err != nil {
				t.Fatal(err)
			}
			if got := parsed.Scheme + "://" + parsed.Host; got != test.wantOrigin {
				t.Fatalf("authLinkFor origin = %q, want %q", got, test.wantOrigin)
			}
			if parsed.Query().Get("token") != "reset-token-123456" {
				t.Fatalf("authLinkFor did not preserve the one-time token: %q", link)
			}
		})
	}
}

func TestAuthLinkForAllowsConfiguredLocalHTTPOrigin(t *testing.T) {
	server := &Server{config: config.Config{PublicAppURL: "http://localhost:4173"}}
	request := httptest.NewRequest(http.MethodPost, "/v1/account/recovery", nil)
	link, err := server.authLinkFor(request, "reset-password", nil, "reset-token-123456", "http://localhost:4173/reset-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "http://localhost:4173/reset-password?") {
		t.Fatalf("authLinkFor link = %q, want configured local HTTP origin", link)
	}
}
