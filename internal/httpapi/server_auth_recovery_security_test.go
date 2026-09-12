package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/mailer"
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
			redirect:   "https://console.example.test/reset-password",
			wantOrigin: "https://console.example.test",
		},
		{name: "untrusted HTTPS origin", redirect: "https://attacker.example/reset-password", wantError: true},
		{name: "javascript URL", redirect: "javascript:alert(1)", wantError: true},
		{name: "data URL", redirect: "data:text/html,alert(1)", wantError: true},
		{name: "HTTP origin when HTTPS is configured", redirect: "http://console.example.test/reset-password", wantError: true},
		{name: "CRLF injection", redirect: "https://console.example.test/reset\r\nBcc:attacker@example.test", wantError: true},
		{name: "query injection", redirect: "https://console.example.test/reset-password?next=https%3A%2F%2Fattacker.example", wantError: true},
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

func TestAuthLinkForUsesConfiguredOriginNotRequestHeaders(t *testing.T) {
	server := &Server{config: config.Config{PublicAppURL: "https://console.example.test"}}
	request := httptest.NewRequest(http.MethodPost, "/v1/account/recovery", nil)
	request.Host = "attacker.example"
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("X-Forwarded-Host", "attacker.example")

	link, err := server.authLinkFor(request, "reset-password", nil, "reset-token-123456", "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Scheme + "://" + parsed.Host; got != "https://console.example.test" {
		t.Fatalf("authLinkFor trusted origin = %q, want configured origin", got)
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

type authMessageRecorder struct {
	message mailer.Message
}

func (r *authMessageRecorder) Send(_ context.Context, message mailer.Message) error {
	r.message = message
	return nil
}

func TestSendAuthEmailUsesFixedTemplateAndServerLink(t *testing.T) {
	recorder := &authMessageRecorder{}
	server := &Server{
		config:          config.Config{AuthPasswordResetTTL: 15 * time.Minute},
		authEmailSender: mailer.NewAuthSender(recorder),
	}
	const token = "server-generated-reset-token"
	if err := server.sendAuthEmail(context.Background(), "user@example.test", mailer.AuthEmailAccountPasswordReset, "https://console.example.test/reset-password?token="+token); err != nil {
		t.Fatal(err)
	}
	if recorder.message.Subject != "Reset your Stealth password" {
		t.Fatalf("auth email subject = %q", recorder.message.Subject)
	}
	if !strings.Contains(recorder.message.TextBody, token) || strings.Count(recorder.message.TextBody, token) != 1 {
		t.Fatalf("auth email did not contain the server-generated token exactly once: %q", recorder.message.TextBody)
	}
	if strings.Contains(recorder.message.TextBody, "attacker body") {
		t.Fatalf("auth email accepted arbitrary body content: %q", recorder.message.TextBody)
	}
}
