package githubauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestManifestURLEncodesShortLivedStateWithoutCredentials(t *testing.T) {
	manifest := AppManifest{
		Name:          "Stealth",
		Description:   "Stealth developer control plane",
		URL:           "https://setup.example.test/",
		RedirectURL:   "https://setup.example.test/v1/setup/github/manifest/callback",
		Public:        false,
		DefaultEvents: []string{"installation"},
		DefaultPerms:  map[string]string{"metadata": "read"},
		SetupURL:      "https://setup.example.test/setup",
	}
	manifestURL, err := ManifestURL(manifest, "short-lived-state")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(manifestURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "github.com" || parsed.Path != "/settings/apps/new" || parsed.Query().Get("state") != "short-lived-state" {
		t.Fatalf("manifest URL = %q", manifestURL)
	}
	var decoded AppManifest
	if err := json.Unmarshal([]byte(parsed.Query().Get("manifest")), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.DefaultPerms["metadata"] != "read" || decoded.Public {
		t.Fatalf("decoded manifest = %#v", decoded)
	}
	if strings.Contains(manifestURL, "client_secret") {
		t.Fatal("manifest URL contains a credential field")
	}
}

func TestManifestClientConvertsCodeAndNeverReturnsProviderResponseOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app-manifests/code-123/conversions" {
			t.Fatalf("manifest request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":123,"name":"stealth","client_id":"Iv1.test","client_secret":"secret","pem":"-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----","webhook_secret":"webhook"}`)
	}))
	defer server.Close()
	client, err := NewManifestClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := client.ConvertManifest(context.Background(), "code-123")
	if err != nil || credentials.ID != 123 || credentials.ClientID != "Iv1.test" {
		t.Fatalf("ConvertManifest() = %#v, %v", credentials, err)
	}
}

func TestManifestURLRejectsInvalidCallbackConfiguration(t *testing.T) {
	base := AppManifest{Name: "Stealth", RedirectURL: "http://setup.example.test/callback"}
	if _, err := ManifestURL(base, "state"); err == nil {
		t.Fatal("ManifestURL accepted an HTTP callback")
	}
	if !ValidClientID("Iv1.client-id_123") || ValidClientID("client id") {
		t.Fatal("ValidClientID validation is incorrect")
	}
}
