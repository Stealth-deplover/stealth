package tlsmanager

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerHostPolicyRequiresVerifiedHostname(t *testing.T) {
	verifier := &testVerifier{verified: map[string]bool{"www.example.com": true}}
	manager, err := New(Options{
		CacheDir:     filepath.Join(t.TempDir(), "acme"),
		Email:        "ops@example.com",
		DirectoryURL: "https://acme.example.test/directory",
		Verifier:     verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckHost(context.Background(), "WWW.Example.com:443"); err != nil {
		t.Fatalf("verified host rejected: %v", err)
	}
	if err := manager.CheckHost(context.Background(), "unknown.example.com"); !errors.Is(err, ErrUnverifiedHost) {
		t.Fatalf("unknown host error = %v, want ErrUnverifiedHost", err)
	}
	if err := manager.CheckHost(context.Background(), "127.0.0.1"); !errors.Is(err, ErrInvalidHost) {
		t.Fatalf("IP host error = %v, want ErrInvalidHost", err)
	}
	if len(verifier.hosts) != 2 {
		t.Fatalf("verifier calls = %v, want two normalized hosts", verifier.hosts)
	}
}

func TestManagerHTTPHandlerFailsClosedForUnknownHost(t *testing.T) {
	manager, err := New(Options{
		CacheDir:     filepath.Join(t.TempDir(), "acme"),
		Email:        "ops@example.com",
		DirectoryURL: "https://acme.example.test/directory",
		Verifier:     &testVerifier{verified: map[string]bool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://unknown.example.com/.well-known/acme-challenge/token", nil)
	request.Host = "unknown.example.com"
	response := httptest.NewRecorder()
	manager.HTTPHandler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("challenge response status = %d, want 403", response.Code)
	}
}

func TestManagerTLSConfigAndCachePermissions(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "acme")
	manager, err := New(Options{
		CacheDir:     cacheDir,
		Email:        "ops@example.com",
		DirectoryURL: "https://acme.example.test/directory",
		Verifier:     &testVerifier{verified: map[string]bool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := manager.TLSConfig()
	if config == nil || config.MinVersion != tls.VersionTLS12 || config.GetCertificate == nil {
		t.Fatalf("unexpected TLS config: %#v", config)
	}
	info, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o700 {
		t.Fatalf("cache permissions = %o, want 0700", permissions)
	}
}

func TestManagerRejectsUnsafeOptions(t *testing.T) {
	verifier := &testVerifier{verified: map[string]bool{}}
	tests := []Options{
		{CacheDir: filepath.Join(t.TempDir(), "acme"), Email: "ops@example.com", DirectoryURL: "http://acme.example.test/directory", Verifier: verifier},
		{CacheDir: filepath.Join(t.TempDir(), "acme"), Email: "ops@example.com", DirectoryURL: "https://acme.example.test/directory?x=1", Verifier: verifier},
		{CacheDir: filepath.Join(t.TempDir(), "acme"), Email: "ops@example.com", DirectoryURL: "https://acme.example.test/directory", Verifier: nil},
	}
	for index, options := range tests {
		if _, err := New(options); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
}

type testVerifier struct {
	verified map[string]bool
	hosts    []string
}

func (v *testVerifier) IsVerifiedSiteHostname(_ context.Context, hostname string) (bool, error) {
	v.hosts = append(v.hosts, hostname)
	return v.verified[hostname], nil
}
