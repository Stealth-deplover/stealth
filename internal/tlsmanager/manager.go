// Package tlsmanager binds autocert to Stealth's verified custom-domain
// registry. A certificate is never requested for an arbitrary SNI name: the
// host must already have passed the Site DNS TXT challenge.
package tlsmanager

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nazxf/stealth-api/internal/repository"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

const defaultDirectoryURL = autocert.DefaultACMEDirectory

var (
	ErrInvalidHost      = errors.New("invalid TLS host")
	ErrUnverifiedHost   = errors.New("TLS host is not a verified Site domain")
	ErrHostPolicyFailed = errors.New("TLS host verification is unavailable")
)

// HostVerifier is implemented by the repository and intentionally accepts
// only a normalized hostname. Implementations should treat false as a normal
// unverified-host result, not as an infrastructure error.
type HostVerifier interface {
	IsVerifiedSiteHostname(context.Context, string) (bool, error)
}

// TLSStatusWriter records the coarse certificate lifecycle exposed by the
// Site domain API. The writer is best-effort and never blocks a handshake.
type TLSStatusWriter interface {
	SetVerifiedSiteDomainTLSStatus(context.Context, string, string) error
}

type Options struct {
	CacheDir     string
	Email        string
	DirectoryURL string
	Verifier     HostVerifier
	StatusWriter TLSStatusWriter
	Logger       *slog.Logger
}

type Manager struct {
	inner        *autocert.Manager
	statusWriter TLSStatusWriter
	logger       *slog.Logger
	statusMu     sync.Mutex
	status       map[string]string
}

// New creates a persistent ACME manager. The cache directory contains the
// account key and private certificate keys, so it is created with 0700 and
// must be backed by durable storage in a multi-restart deployment.
func New(options Options) (*Manager, error) {
	if options.Verifier == nil {
		return nil, errors.New("TLS host verifier is required")
	}
	directoryURL := strings.TrimSpace(options.DirectoryURL)
	if directoryURL == "" {
		directoryURL = defaultDirectoryURL
	}
	if !isDirectoryURL(directoryURL) {
		return nil, errors.New("ACME directory must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if strings.TrimSpace(options.Email) == "" || strings.ContainsAny(options.Email, "\x00\r\n") {
		return nil, errors.New("ACME account email is required")
	}
	rawCacheDir := strings.TrimSpace(options.CacheDir)
	if rawCacheDir == "" {
		return nil, errors.New("ACME cache directory must be a valid non-root path")
	}
	cacheDir, err := filepath.Abs(rawCacheDir)
	if err != nil || strings.TrimSpace(cacheDir) == "" || cacheDir == string(filepath.Separator) {
		return nil, errors.New("ACME cache directory must be a valid non-root path")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create ACME cache directory: %w", err)
	}
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure ACME cache directory: %w", err)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{statusWriter: options.StatusWriter, logger: logger, status: make(map[string]string)}
	m.inner = &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cacheDir),
		Email:      strings.TrimSpace(options.Email),
		Client:     &acme.Client{DirectoryURL: directoryURL},
		HostPolicy: m.hostPolicy(options.Verifier),
	}
	return m, nil
}

// TLSConfig returns the server configuration for a TLS listener. autocert
// includes both HTTP/2 and TLS-ALPN-01 support; TLS 1.2 is the minimum for
// modern public HTTPS endpoints.
func (m *Manager) TLSConfig() *tls.Config {
	if m == nil || m.inner == nil {
		return nil
	}
	config := m.inner.TLSConfig()
	config.GetCertificate = m.GetCertificate
	config.MinVersion = tls.VersionTLS12
	return config
}

// HTTPHandler serves ACME HTTP-01 challenges. The fallback is used for all
// other requests; nil produces autocert's HTTPS redirect handler.
func (m *Manager) HTTPHandler(fallback http.Handler) http.Handler {
	if m == nil || m.inner == nil {
		return fallback
	}
	return m.inner.HTTPHandler(fallback)
}

// GetCertificate delegates issuance and renewal to autocert, then records a
// best-effort status transition. Renewal timers are owned by autocert and use
// the same persistent cache, so no separate cron process is needed.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if m == nil || m.inner == nil {
		return nil, errors.New("TLS manager is not initialized")
	}
	if hello == nil {
		return nil, ErrInvalidHost
	}
	host, hostErr := normalizeHost(helloServerName(hello))
	if hostErr == nil {
		m.recordStatus(host, "pending")
	}
	certificate, err := m.inner.GetCertificate(hello)
	if hostErr == nil {
		if err != nil {
			m.recordStatus(host, "failed")
		} else {
			m.recordStatus(host, "active")
		}
	}
	return certificate, err
}

// CheckHost exposes the exact policy used by autocert for integration and
// readiness tests without triggering certificate issuance.
func (m *Manager) CheckHost(ctx context.Context, host string) error {
	if m == nil || m.inner == nil || m.inner.HostPolicy == nil {
		return ErrHostPolicyFailed
	}
	normalized, err := normalizeHost(host)
	if err != nil {
		return err
	}
	return m.inner.HostPolicy(ctx, normalized)
}

func (m *Manager) hostPolicy(verifier HostVerifier) autocert.HostPolicy {
	return func(ctx context.Context, host string) error {
		normalized, err := normalizeHost(host)
		if err != nil {
			return err
		}
		verified, err := verifier.IsVerifiedSiteHostname(ctx, normalized)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrHostPolicyFailed, err)
		}
		if !verified {
			return fmt.Errorf("%w: %s", ErrUnverifiedHost, normalized)
		}
		return nil
	}
}

func (m *Manager) recordStatus(host, status string) {
	if m.statusWriter == nil || host == "" {
		return
	}
	m.statusMu.Lock()
	if m.status[host] == status {
		m.statusMu.Unlock()
		return
	}
	m.status[host] = status
	m.statusMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.statusWriter.SetVerifiedSiteDomainTLSStatus(ctx, host, status); err != nil {
			m.statusMu.Lock()
			if m.status[host] == status {
				delete(m.status, host)
			}
			m.statusMu.Unlock()
			m.logger.Warn("site TLS status update failed", "hostname", host, "status", status, "error", err)
		}
	}()
}

func helloServerName(hello *tls.ClientHelloInfo) string {
	if hello == nil {
		return ""
	}
	return hello.ServerName
}

func normalizeHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	} else if strings.Contains(value, ":") {
		return "", ErrInvalidHost
	}
	hostname, err := repository.NormalizeSiteHostname(value)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidHost, err)
	}
	return hostname, nil
}

func isDirectoryURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(value, "\x00\r\n \t") {
		return false
	}
	if port := parsed.Port(); port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return false
		}
	}
	return true
}
