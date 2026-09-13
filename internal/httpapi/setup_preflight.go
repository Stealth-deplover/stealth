package httpapi

import (
	"context"
	"fmt"
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
	recommendedSetupCPUs        = 2
	recommendedSetupMemoryBytes = uint64(2) << 30
	recommendedSetupDiskBytes   = uint64(5) << 30
)

var cloudflareTunnelHosts = []string{
	"region1.v2.argotunnel.com",
	"region2.v2.argotunnel.com",
}

func (s *Server) setupPreflight(w http.ResponseWriter, r *http.Request) {
	checks := make([]setupCheck, 0, 10)
	add := func(name, detail string, ok, required bool) {
		status := "pass"
		if !ok {
			status = "fail"
			if !required {
				status = "warn"
			}
		}
		checks = append(checks, setupCheck{Name: name, Detail: detail, Status: status, Required: required})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cpuCount := runtime.NumCPU()
	add("CPU", fmt.Sprintf("%d logical CPUs available", cpuCount), cpuCount >= recommendedSetupCPUs, false)
	if memory, err := setupMemoryBytes(); err != nil {
		add("Memory", "host memory could not be read", false, false)
	} else {
		add("Memory", formatSetupBytes(memory)+" total (2 GiB recommended)", memory >= recommendedSetupMemoryBytes, false)
	}
	diskPath := setupExistingPath(s.config.InstallRoot)
	if free, err := setupFreeBytes(diskPath); err != nil {
		add("Disk", "free space could not be measured", false, false)
	} else {
		add("Disk", formatSetupBytes(free)+" free (5 GiB recommended)", free >= recommendedSetupDiskBytes, false)
	}

	if s.repo == nil {
		add("Database", "database dependency is not configured", false, true)
	} else if err := s.repo.Ping(ctx); err != nil {
		add("Database", "PostgreSQL is not ready", false, true)
	} else {
		add("Database", "PostgreSQL is reachable", true, true)
	}
	if s.storage == nil || !s.storageReady {
		add("Storage", "storage is not configured", false, true)
	} else if err := s.storage.Ping(ctx); err != nil {
		add("Storage", "storage is not reachable", false, true)
	} else {
		add("Storage", "storage is reachable", true, true)
	}
	if s.limiter == nil {
		add("Redis", "rate limiter is not configured", false, true)
	} else if err := s.limiter.Ping(ctx); err != nil {
		add("Redis", "Redis is not reachable", false, true)
	} else {
		add("Redis", "Redis is reachable", true, true)
	}
	if s.setupRunner == nil {
		add("Docker", "Docker command runner is not configured", false, true)
	} else if _, err := s.setupRunner.Output(ctx, "", "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		add("Docker", "Docker is not available", false, true)
	} else {
		add("Docker", "Docker is available", true, true)
	}

	if ok, detail := checkCloudflareAPI(ctx); !ok {
		add("Cloudflare API", detail, false, false)
	} else {
		add("Cloudflare API", detail, true, false)
	}
	if ok, detail := checkCloudflareTunnelConnectivity(ctx); !ok {
		add("Cloudflare Tunnel", detail, false, false)
	} else {
		add("Cloudflare Tunnel", detail, true, false)
	}

	writeJSON(w, http.StatusOK, setupPreflightResponse{Checks: checks})
}

func setupMemoryBytes() (uint64, error) {
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

func setupExistingPath(path string) string {
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

func setupFreeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

func formatSetupBytes(value uint64) string {
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

func checkCloudflareAPI(ctx context.Context) (bool, string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4", nil)
	if err != nil {
		return false, "Cloudflare API request could not be created"
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, "Cloudflare API is not reachable over HTTPS"
	}
	defer response.Body.Close()
	if response.StatusCode >= 500 {
		return false, fmt.Sprintf("Cloudflare API returned HTTP %d", response.StatusCode)
	}
	return true, fmt.Sprintf("Cloudflare API responded over HTTPS (HTTP %d)", response.StatusCode)
}

func checkCloudflareTunnelConnectivity(ctx context.Context) (bool, string) {
	resolver := net.DefaultResolver
	addresses := make([]string, 0, len(cloudflareTunnelHosts))
	for _, host := range cloudflareTunnelHosts {
		resolved, err := resolver.LookupHost(ctx, host)
		if err != nil || len(resolved) == 0 {
			return false, "Cloudflare Tunnel edge DNS is not resolving"
		}
		addresses = append(addresses, resolved...)
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, "7844"))
		if err != nil {
			continue
		}
		_ = connection.Close()
		return true, "Cloudflare Tunnel edge DNS resolves and TCP/7844 is reachable; cloudflared will check QUIC/UDP and HTTP/2/TCP at startup"
	}
	return false, "Cloudflare Tunnel edge resolves, but TCP/7844 is unreachable; allow outbound TCP or UDP 7844"
}
