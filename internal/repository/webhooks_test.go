package repository

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeWebhookURL(t *testing.T) {
	valid := []string{
		"https://hooks.example.com/stealth",
		"https://hooks.example.com:8443/events?tenant=one",
		"https://[2001:db8::1]/callback",
	}
	for _, value := range valid {
		if got, err := NormalizeWebhookURL(value); err != nil || got != value {
			t.Fatalf("NormalizeWebhookURL(%q) = %q, %v", value, got, err)
		}
	}
	invalid := []string{
		"http://hooks.example.com/callback",
		"https://user:pass@hooks.example.com/callback",
		"https://hooks.example.com/callback#fragment",
		"https://hooks.example.com:0/callback",
		"https://hooks.example.com:65536/callback",
		"https://hooks.example.com/call back",
	}
	for _, value := range invalid {
		if _, err := NormalizeWebhookURL(value); !errors.Is(err, ErrInvalidWebhook) {
			t.Errorf("NormalizeWebhookURL(%q) error = %v, want ErrInvalidWebhook", value, err)
		}
	}
}

func TestNormalizeWebhookEvents(t *testing.T) {
	got, err := NormalizeWebhookEvents([]string{"function_execution.succeeded", "database_row.create", "function_execution.succeeded"})
	if err == nil || !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("duplicate events error = %v", err)
	}
	got, err = NormalizeWebhookEvents([]string{"function_execution.succeeded", "database_row.create"})
	if err != nil || strings.Join(got, ",") != "database_row.create,function_execution.succeeded" {
		t.Fatalf("normalized events = %#v, %v", got, err)
	}
	got, err = NormalizeWebhookEvents(nil)
	if err != nil || len(got) != 1 || got[0] != "*" {
		t.Fatalf("default events = %#v, %v", got, err)
	}
	for _, value := range []string{"Function.Create", "1bad event", "-bad", "foo*"} {
		if _, err := NormalizeWebhookEvents([]string{value}); !errors.Is(err, ErrInvalidWebhook) {
			t.Errorf("NormalizeWebhookEvents(%q) error = %v, want ErrInvalidWebhook", value, err)
		}
	}
}

func TestNewWebhookSecret(t *testing.T) {
	secret, err := newWebhookSecret()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "whsec_") || len(secret) != len("whsec_")+43 {
		t.Fatalf("secret has unexpected shape: %q", secret)
	}
}
