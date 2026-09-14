// Package preflight owns the shared host-readiness checks used before Stealth
// setup and installation. OS, Docker, and network calls enter through
// replaceable probes so the CLI and browser adapters use the same check
// definitions without sharing presentation concerns.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	RecommendedCPUs        = 2
	RecommendedMemoryBytes = uint64(2) << 30
	RecommendedDiskBytes   = uint64(5) << 30
	CloudflareAPIURL       = "https://api.cloudflare.com/client/v4"
)

var DefaultCloudflareTunnelHosts = []string{
	"region1.v2.argotunnel.com",
	"region2.v2.argotunnel.com",
}

// Check is the presentation-neutral result shared by the CLI and HTTP
// adapters. Required checks gate progress; warnings remain visible but do not
// block setup.
type Check struct {
	Name     string
	Detail   string
	OK       bool
	Required bool
}

// CommandRunner is the process seam needed by Docker probes. It intentionally
// matches the runner shape already used by the CLI and install engine.
type CommandRunner interface {
	Output(context.Context, string, string, ...string) ([]byte, error)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Probes struct {
	CPUCount        func() int
	MemoryBytes     func() (uint64, error)
	FreeBytes       func(string) (uint64, error)
	DockerVersion   func(context.Context) (string, error)
	ComposeVersion  func(context.Context) (string, error)
	DockerSocketGID func(string) (uint32, error)
	HTTPStatus      func(context.Context, string) (int, error)
	LookupHost      func(context.Context, string) ([]string, error)
	DialContext     func(context.Context, string, string) (net.Conn, error)
}

// NewProbes creates production probes around the caller's command and HTTP
// seams. Passing nil is safe and makes the affected checks fail closed.
func NewProbes(runner CommandRunner, httpClient HTTPDoer) Probes {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	probes := Probes{
		CPUCount:    runtime.NumCPU,
		MemoryBytes: memoryBytes,
		FreeBytes:   freeBytes,
		DockerSocketGID: func(path string) (uint32, error) {
			return dockerSocketGID(path)
		},
		HTTPStatus: func(ctx context.Context, endpoint string) (int, error) {
			return httpStatus(ctx, httpClient, endpoint)
		},
		LookupHost: net.DefaultResolver.LookupHost,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
		},
	}
	if runner == nil {
		probes.DockerVersion = func(context.Context) (string, error) {
			return "", errors.New("Docker runner is not configured")
		}
		probes.ComposeVersion = func(context.Context) (string, error) {
			return "", errors.New("Docker runner is not configured")
		}
	} else {
		probes.DockerVersion = func(ctx context.Context) (string, error) {
			output, err := runner.Output(ctx, "", "docker", "version", "--format", "{{.Server.Version}}")
			return strings.TrimSpace(string(output)), err
		}
		probes.ComposeVersion = func(ctx context.Context) (string, error) {
			output, err := runner.Output(ctx, "", "docker", "compose", "version", "--short")
			return strings.TrimSpace(string(output)), err
		}
	}
	return probes
}

func (p Probes) normalized() Probes {
	defaults := NewProbes(nil, nil)
	if p.CPUCount == nil {
		p.CPUCount = defaults.CPUCount
	}
	if p.MemoryBytes == nil {
		p.MemoryBytes = defaults.MemoryBytes
	}
	if p.FreeBytes == nil {
		p.FreeBytes = defaults.FreeBytes
	}
	if p.DockerVersion == nil {
		p.DockerVersion = defaults.DockerVersion
	}
	if p.ComposeVersion == nil {
		p.ComposeVersion = defaults.ComposeVersion
	}
	if p.DockerSocketGID == nil {
		p.DockerSocketGID = defaults.DockerSocketGID
	}
	if p.HTTPStatus == nil {
		p.HTTPStatus = defaults.HTTPStatus
	}
	if p.LookupHost == nil {
		p.LookupHost = defaults.LookupHost
	}
	if p.DialContext == nil {
		p.DialContext = defaults.DialContext
	}
	return p
}

// PlatformChecks is shared by all installation surfaces.
func PlatformChecks(goos, goarch string) []Check {
	osCheck := Check{Name: "OS", Detail: goos, OK: goos == "linux", Required: true}
	if !osCheck.OK {
		osCheck.Detail = fmt.Sprintf("%s (unsupported; Linux is supported)", goos)
	}
	architectureCheck := Check{
		Name:     "Architecture",
		Detail:   goarch,
		OK:       goarch == "amd64" || goarch == "arm64",
		Required: true,
	}
	if !architectureCheck.OK {
		architectureCheck.Detail = fmt.Sprintf("%s (unsupported; use amd64 or arm64)", goarch)
	}
	return []Check{osCheck, architectureCheck}
}

// ResourceChecks evaluates the CPU, memory, and disk recommendations shared
// by browser setup and the CLI. Resource failures are warnings because Docker
// and the application may still be usable on a smaller host.
func ResourceChecks(ctx context.Context, probes Probes, diskPath string) []Check {
	probes = probes.normalized()
	checks := make([]Check, 0, 3)
	cpuCount := probes.CPUCount()
	checks = append(checks, Check{Name: "CPU", Detail: fmt.Sprintf("%d logical CPUs available", cpuCount), OK: cpuCount >= RecommendedCPUs})
	if err := ctx.Err(); err != nil {
		checks = append(checks, Check{Name: "Memory", Detail: "check cancelled", Required: false})
		checks = append(checks, Check{Name: "Disk space", Detail: "check cancelled", Required: false})
		return checks
	}
	if memory, err := probes.MemoryBytes(); err != nil {
		checks = append(checks, Check{Name: "Memory", Detail: "host memory could not be read"})
	} else {
		checks = append(checks, Check{Name: "Memory", Detail: FormatBytes(memory) + " total (2 GiB recommended)", OK: memory >= RecommendedMemoryBytes})
	}
	if free, err := probes.FreeBytes(ExistingPath(diskPath)); err != nil {
		checks = append(checks, Check{Name: "Disk", Detail: "free space could not be measured"})
	} else {
		checks = append(checks, Check{Name: "Disk", Detail: FormatBytes(free) + " free (5 GiB recommended)", OK: free >= RecommendedDiskBytes})
	}
	return checks
}

// ExistingPath walks up to a path that exists so a not-yet-created install
// root is still checked against the filesystem that will contain it.
func ExistingPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return string(filepath.Separator)
		}
		path = parent
	}
}

// DockerChecks centralizes the Docker and Compose readiness definitions.
func DockerChecks(ctx context.Context, probes Probes, includeCompose, includeSocket bool) []Check {
	probes = probes.normalized()
	checks := make([]Check, 0, 3)
	if version, err := probes.DockerVersion(ctx); err != nil {
		checks = append(checks, Check{Name: "Docker", Detail: "Docker is not available", Required: true})
	} else {
		checks = append(checks, Check{Name: "Docker", Detail: version, OK: true, Required: true})
	}
	if includeCompose {
		if version, err := probes.ComposeVersion(ctx); err != nil {
			checks = append(checks, Check{Name: "Docker Compose", Detail: "Docker Compose is not available", Required: true})
		} else {
			checks = append(checks, Check{Name: "Docker Compose", Detail: version, OK: true, Required: true})
		}
	}
	if includeSocket {
		if gid, err := probes.DockerSocketGID("/var/run/docker.sock"); err != nil {
			checks = append(checks, Check{Name: "Docker socket", Detail: "not accessible", Required: true})
		} else {
			checks = append(checks, Check{Name: "Docker socket", Detail: "group " + strconv.FormatUint(uint64(gid), 10), OK: true, Required: true})
		}
	}
	return checks
}

// CloudflareChecks verifies only outbound connectivity. It does not send a
// token and therefore cannot validate authorization or expose provider
// credentials during preflight.
func CloudflareChecks(ctx context.Context, probes Probes, hosts []string) []Check {
	probes = probes.normalized()
	checks := make([]Check, 0, 2)
	status, err := probes.HTTPStatus(ctx, CloudflareAPIURL)
	if err != nil {
		checks = append(checks, Check{Name: "Cloudflare API", Detail: "Cloudflare API is not reachable over HTTPS"})
	} else if status >= 500 {
		checks = append(checks, Check{Name: "Cloudflare API", Detail: fmt.Sprintf("Cloudflare API returned HTTP %d", status)})
	} else {
		checks = append(checks, Check{Name: "Cloudflare API", Detail: fmt.Sprintf("Cloudflare API responded over HTTPS (HTTP %d)", status), OK: true})
	}

	addresses := make([]string, 0, len(hosts))
	resolved := true
	for _, host := range hosts {
		values, lookupErr := probes.LookupHost(ctx, host)
		if lookupErr != nil || len(values) == 0 {
			resolved = false
			break
		}
		addresses = append(addresses, values...)
	}
	if !resolved {
		checks = append(checks, Check{Name: "Cloudflare Tunnel", Detail: "Cloudflare Tunnel edge DNS is not resolving"})
		return checks
	}
	for _, address := range addresses {
		connection, dialErr := probes.DialContext(ctx, "tcp", net.JoinHostPort(address, "7844"))
		if dialErr != nil || connection == nil {
			continue
		}
		_ = connection.Close()
		checks = append(checks, Check{Name: "Cloudflare Tunnel", Detail: "Cloudflare Tunnel edge DNS resolves and TCP/7844 is reachable; cloudflared will check QUIC/UDP and HTTP/2/TCP at startup", OK: true})
		return checks
	}
	checks = append(checks, Check{Name: "Cloudflare Tunnel", Detail: "Cloudflare Tunnel edge resolves, but TCP/7844 is unreachable; allow outbound TCP or UDP 7844"})
	return checks
}

func ChecksPass(checks []Check) bool {
	for _, check := range checks {
		if check.Required && !check.OK {
			return false
		}
	}
	return true
}

func FailedSummary(checks []Check) string {
	failed := make([]string, 0)
	for _, check := range checks {
		if check.Required && !check.OK {
			failed = append(failed, check.Name+": "+check.Detail)
		}
	}
	return strings.Join(failed, "; ")
}

func RenderCheck(check Check) string {
	mark := "✗"
	if check.OK {
		mark = "✓"
	}
	if !check.Required && !check.OK {
		mark = "!"
	}
	if check.Detail == "" {
		return fmt.Sprintf("%s %s", mark, check.Name)
	}
	return fmt.Sprintf("%s %-16s %s", mark, check.Name, check.Detail)
}

func FormatBytes(value uint64) string {
	const gib = uint64(1) << 30
	if value >= gib {
		return fmt.Sprintf("%.1f GiB", float64(value)/float64(gib))
	}
	const mib = uint64(1) << 20
	if value >= mib {
		return fmt.Sprintf("%.1f MiB", float64(value)/float64(mib))
	}
	return fmt.Sprintf("%d KiB", value/1024)
}

// FreeBytes and DockerSocketGID are exported for small CLI lifecycle paths
// that need a direct probe outside the aggregated check report.
func FreeBytes(path string) (uint64, error) {
	return freeBytes(path)
}

func DockerSocketGID(path string) (uint32, error) {
	return dockerSocketGID(path)
}

func memoryBytes() (uint64, error) {
	contents, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "MemTotal:" {
			continue
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || kilobytes > ^uint64(0)/1024 {
			return 0, fmt.Errorf("invalid MemTotal")
		}
		return kilobytes * 1024, nil
	}
	return 0, fmt.Errorf("MemTotal is missing")
}

func freeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

func dockerSocketGID(path string) (uint32, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("unsupported file metadata")
	}
	return stat.Gid, nil
}

func httpStatus(ctx context.Context, client HTTPDoer, endpoint string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode, nil
}
