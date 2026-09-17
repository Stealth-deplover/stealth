package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tlsSettings owns the optional ACME listener and certificate-cache contract.
// The loader receives the already-resolved storage and HTTP listener values so
// its cross-domain safety checks remain explicit without reaching back into
// environment variables.
type tlsSettings struct {
	enabled              bool
	email                string
	directoryURL         string
	tlsAddress           string
	httpChallengeAddress string
	certCacheDir         string
}

func loadTLSSettings(storageRoot, httpAddress string) (tlsSettings, error) {
	enabled, err := strconv.ParseBool(value("ACME_ENABLED", "false"))
	if err != nil {
		return tlsSettings{}, fmt.Errorf("ACME_ENABLED must be true or false")
	}
	directoryURL := value("ACME_DIRECTORY_URL", "https://acme-v02.api.letsencrypt.org/directory")
	if !isACMEDirectoryURL(directoryURL) {
		return tlsSettings{}, fmt.Errorf("ACME_DIRECTORY_URL must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	tlsAddress := value("ACME_TLS_ADDR", ":8443")
	if !isListenAddress(tlsAddress) {
		return tlsSettings{}, fmt.Errorf("ACME_TLS_ADDR must be a TCP host:port with a port between 1 and 65535")
	}
	httpChallengeAddress := value("ACME_HTTP_CHALLENGE_ADDR", ":8081")
	if !isListenAddress(httpChallengeAddress) {
		return tlsSettings{}, fmt.Errorf("ACME_HTTP_CHALLENGE_ADDR must be a TCP host:port with a port between 1 and 65535")
	}
	email := strings.TrimSpace(os.Getenv("ACME_EMAIL"))
	certCacheDir, err := filepath.Abs(value("ACME_CERT_CACHE_DIR", filepath.Join(storageRoot, "acme")))
	if err != nil || strings.TrimSpace(certCacheDir) == "" || certCacheDir == string(filepath.Separator) {
		return tlsSettings{}, fmt.Errorf("ACME_CERT_CACHE_DIR must be a valid non-root filesystem path")
	}
	if enabled && !isACMEEmail(email) {
		return tlsSettings{}, fmt.Errorf("ACME_EMAIL must be a valid email address when ACME_ENABLED is true")
	}
	if enabled && tlsAddress == httpChallengeAddress {
		return tlsSettings{}, fmt.Errorf("ACME_TLS_ADDR and ACME_HTTP_CHALLENGE_ADDR must be different listeners")
	}
	if enabled && (sameListenPort(tlsAddress, httpAddress) || sameListenPort(httpChallengeAddress, httpAddress)) {
		return tlsSettings{}, fmt.Errorf("ACME listeners must not reuse the HTTP_ADDR port")
	}
	return tlsSettings{
		enabled:              enabled,
		email:                email,
		directoryURL:         directoryURL,
		tlsAddress:           tlsAddress,
		httpChallengeAddress: httpChallengeAddress,
		certCacheDir:         filepath.Clean(certCacheDir),
	}, nil
}

func (s tlsSettings) apply(c *Config) {
	c.ACMEEnabled = s.enabled
	c.ACMEEmail = s.email
	c.ACMEDirectoryURL = s.directoryURL
	c.ACMETLSAddress = s.tlsAddress
	c.ACMEHTTPChallengeAddress = s.httpChallengeAddress
	c.ACMECertCacheDir = s.certCacheDir
}
