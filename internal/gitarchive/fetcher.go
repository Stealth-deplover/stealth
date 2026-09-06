// Package gitarchive resolves a small, deliberately allow-listed subset of
// public Git hosting URLs into provider archive endpoints. It never accepts a
// user supplied download URL: the URL is reconstructed from validated
// repository and ref components, which keeps the control plane out of the
// usual SSRF and redirect-following traps.
package gitarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
	defaultRef     = "main"
	maxRefBytes    = 256
)

var (
	ErrInvalidRepository = errors.New("invalid git repository")
	ErrInvalidRef        = errors.New("invalid git ref")
	ErrTooLarge          = errors.New("git archive exceeds the configured maximum size")
	ErrUnavailable       = errors.New("git archive is unavailable")
	repositoryPart       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
)

// Archive is the validated source descriptor and its streaming response body.
// Callers must close Body, including when the upload is rejected.
type Archive struct {
	Provider   string
	Repository string
	Ref        string
	ArchiveURL string
	Filename   string
	Body       io.ReadCloser
}

// SourceFetcher is the narrow dependency used by the Sites HTTP handler. It
// makes provider access replaceable in tests without exposing an arbitrary
// URL fetch primitive to the rest of the API.
type SourceFetcher interface {
	Fetch(context.Context, string, string, int64) (Archive, error)
}

// Fetcher downloads archives from GitHub or GitLab using a transport that
// disables proxies and rejects private/link-local DNS answers.
type Fetcher struct {
	Client *http.Client
}

func NewFetcher() *Fetcher {
	return &Fetcher{Client: &http.Client{
		Transport:     safeTransport(),
		Timeout:       10 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Parse validates and canonicalizes a public repository URL and Git ref.
// GitHub repositories are owner/repo; GitLab also permits nested groups.
func Parse(repositoryURL, ref string) (Archive, error) {
	parsed, err := url.Parse(strings.TrimSpace(repositoryURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Archive{}, fmt.Errorf("%w: repository must be an HTTPS GitHub or GitLab URL without credentials, query, or fragment", ErrInvalidRepository)
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Port() != "" && parsed.Port() != "443" {
		return Archive{}, fmt.Errorf("%w: repository port is not allowed", ErrInvalidRepository)
	}
	provider := ""
	switch host {
	case "github.com":
		provider = ProviderGitHub
	case "gitlab.com":
		provider = ProviderGitLab
	default:
		return Archive{}, fmt.Errorf("%w: only github.com and gitlab.com are supported", ErrInvalidRepository)
	}
	pathValue := strings.Trim(parsed.Path, "/")
	if pathValue == "" || strings.Contains(pathValue, "\\") || strings.Contains(pathValue, "\x00") {
		return Archive{}, fmt.Errorf("%w: repository path is invalid", ErrInvalidRepository)
	}
	parts := strings.Split(pathValue, "/")
	if provider == ProviderGitHub && len(parts) != 2 || provider == ProviderGitLab && len(parts) < 2 {
		return Archive{}, fmt.Errorf("%w: repository path has the wrong number of segments", ErrInvalidRepository)
	}
	for index, part := range parts {
		if index == len(parts)-1 {
			part = strings.TrimSuffix(part, ".git")
			parts[index] = part
		}
		if part == "" || part == "." || part == ".." || !repositoryPart.MatchString(part) {
			return Archive{}, fmt.Errorf("%w: repository path contains an unsafe segment", ErrInvalidRepository)
		}
	}
	canonicalRef, err := validateRef(ref)
	if err != nil {
		return Archive{}, err
	}
	canonicalURL := "https://" + host + "/" + escapeSegments(parts)
	if len([]byte(canonicalURL)) > 512 {
		return Archive{}, fmt.Errorf("%w: repository URL is too long", ErrInvalidRepository)
	}
	archiveURL := buildArchiveURL(provider, parts, canonicalRef)
	filename := archiveFilename(provider, parts[len(parts)-1], canonicalRef, canonicalURL)
	return Archive{Provider: provider, Repository: canonicalURL, Ref: canonicalRef, ArchiveURL: archiveURL, Filename: filename}, nil
}

func validateRef(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultRef
	}
	if len([]byte(value)) > maxRefBytes || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") || strings.Contains(value, "..") || strings.Contains(value, "@{") {
		return "", fmt.Errorf("%w: ref contains an unsafe path", ErrInvalidRef)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".lock") {
			return "", fmt.Errorf("%w: ref contains an unsafe segment", ErrInvalidRef)
		}
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || strings.ContainsRune("._/-", character) {
			continue
		}
		return "", fmt.Errorf("%w: ref contains unsupported characters", ErrInvalidRef)
	}
	return value, nil
}

func buildArchiveURL(provider string, parts []string, ref string) string {
	if provider == ProviderGitHub {
		return "https://codeload.github.com/" + escapeSegments(parts) + "/tar.gz/" + escapeSegments(strings.Split(ref, "/"))
	}
	project := parts[len(parts)-1]
	refPath := escapeSegments(strings.Split(ref, "/"))
	archiveName := url.PathEscape(project+"-"+strings.ReplaceAll(ref, "/", "-")) + ".tar.gz"
	return "https://gitlab.com/" + escapeSegments(parts) + "/-/archive/" + refPath + "/" + archiveName
}

func escapeSegments(parts []string) string {
	escaped := make([]string, len(parts))
	for index, part := range parts {
		escaped[index] = url.PathEscape(part)
	}
	return strings.Join(escaped, "/")
}

func archiveFilename(provider, project, ref, canonicalURL string) string {
	safeRef := strings.ReplaceAll(ref, "/", "-")
	name := "git-" + provider + "-" + project + "-" + safeRef + ".tar.gz"
	if len([]byte(name)) <= 255 {
		return name
	}
	digest := sha256.Sum256([]byte(canonicalURL + "#" + ref))
	return "git-" + provider + "-" + project + "-" + hex.EncodeToString(digest[:8]) + ".tar.gz"
}

func (f *Fetcher) Fetch(ctx context.Context, repositoryURL, ref string, maxBytes int64) (Archive, error) {
	descriptor, err := Parse(repositoryURL, ref)
	if err != nil {
		return Archive{}, err
	}
	if maxBytes <= 0 {
		return Archive{}, fmt.Errorf("%w: maximum archive size is invalid", ErrTooLarge)
	}
	client := (*http.Client)(nil)
	if f != nil {
		client = f.Client
	}
	if client == nil {
		client = NewFetcher().Client
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, descriptor.ArchiveURL, nil)
	if err != nil {
		return Archive{}, fmt.Errorf("%w: create archive request: %v", ErrUnavailable, err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := client.Do(request)
	if err != nil {
		return Archive{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return Archive{}, fmt.Errorf("%w: provider returned HTTP %d", ErrUnavailable, response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		_ = response.Body.Close()
		return Archive{}, ErrTooLarge
	}
	return Archive{Provider: descriptor.Provider, Repository: descriptor.Repository, Ref: descriptor.Ref, ArchiveURL: descriptor.ArchiveURL, Filename: descriptor.Filename, Body: &boundedBody{body: response.Body, remaining: maxBytes}}, nil
}

type boundedBody struct {
	body       io.ReadCloser
	remaining  int64
	checkedEOF bool
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining > 0 {
		if int64(len(p)) > b.remaining {
			p = p[:b.remaining]
		}
		n, err := b.body.Read(p)
		b.remaining -= int64(n)
		if err == io.EOF && b.remaining > 0 {
			b.checkedEOF = true
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return n, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return n, err
	}
	if b.checkedEOF {
		return 0, io.EOF
	}
	var probe [1]byte
	n, err := b.body.Read(probe[:])
	if n > 0 {
		return 0, ErrTooLarge
	}
	b.checkedEOF = true
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return 0, err
}

func (b *boundedBody) Close() error { return b.body.Close() }

func safeTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           safeDialContext(dialer),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		// Apply the archive limit to the bytes delivered to the caller. Keeping
		// compression enabled lets the transport transparently decompress a
		// provider response before boundedBody counts it.
		DisableCompression: false,
		ForceAttemptHTTP2:  true,
	}
}

func safeDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			if blockedIP(ip) {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("all provider addresses are private or unavailable")
	}
}

func blockedIP(ip netipLike) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// net.IP and netip.Addr have similar predicates, but this tiny interface
// keeps blockedIP easy to exercise with either representation.
type netipLike interface {
	IsPrivate() bool
	IsLoopback() bool
	IsLinkLocalUnicast() bool
	IsLinkLocalMulticast() bool
	IsUnspecified() bool
	IsMulticast() bool
}
