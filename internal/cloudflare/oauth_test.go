package cloudflare

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthAuthorizationAndExchangeKeepProviderSecretServerSide(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read OAuth body: %v", err)
		}
		received, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-token","refresh_token":"refresh-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer server.Close()
	client, err := NewOAuthClient("client-id", "client-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.Authorization = server.URL + "/authorize"
	client.TokenURL = server.URL + "/token"
	authorizationURL, err := client.AuthorizationURL("https://setup.example.test/v1/callback", "state-value", []string{"account:read", "zone:read"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil || parsed.Query().Get("client_id") != "client-id" || parsed.Query().Get("state") != "state-value" {
		t.Fatalf("authorization URL = %q, %v", authorizationURL, err)
	}
	if strings.Contains(authorizationURL, "client-secret") {
		t.Fatal("authorization URL contains the OAuth client secret")
	}
	token, err := client.Exchange(context.Background(), "temporary-code", "https://setup.example.test/v1/callback")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access-token" || received.Get("client_secret") != "client-secret" || received.Get("code") != "temporary-code" {
		t.Fatalf("OAuth exchange = %#v, form = %#v", token, received)
	}
}

func TestOAuthRejectsUnsafeRedirects(t *testing.T) {
	client, err := NewOAuthClient("client-id", "client-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, redirect := range []string{"http://setup.example.test/callback", "https://user:pass@setup.example.test/callback", "https://setup.example.test/callback?state=secret"} {
		if _, err := client.AuthorizationURL(redirect, "state", nil); err == nil {
			t.Fatalf("AuthorizationURL accepted %q", redirect)
		}
	}
}
