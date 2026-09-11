package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type SystemCheck struct {
	Name     string
	Detail   string
	OK       bool
	Required bool
}

func (a *App) systemChecks(ctx context.Context, installRoot string) []SystemCheck {
	checks := installerPlatformChecks(runtime.GOOS, runtime.GOARCH)
	if output, err := a.runner.Output(ctx, "", "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		checks = append(checks, SystemCheck{Name: "Docker", Detail: "not available", Required: true})
	} else {
		checks = append(checks, SystemCheck{Name: "Docker", Detail: strings.TrimSpace(string(output)), OK: true, Required: true})
	}
	if output, err := a.runner.Output(ctx, "", "docker", "compose", "version", "--short"); err != nil {
		checks = append(checks, SystemCheck{Name: "Docker Compose", Detail: "not available", Required: true})
	} else {
		checks = append(checks, SystemCheck{Name: "Docker Compose", Detail: strings.TrimSpace(string(output)), OK: true, Required: true})
	}

	dockerSocket := "/var/run/docker.sock"
	if gid, err := dockerSocketGID(dockerSocket); err != nil {
		checks = append(checks, SystemCheck{Name: "Docker socket", Detail: "not accessible", Required: true})
	} else {
		checks = append(checks, SystemCheck{Name: "Docker socket", Detail: "group " + strconv.FormatUint(uint64(gid), 10), OK: true, Required: true})
	}
	if free, err := freeBytes(filepath.Dir(installRoot)); err != nil {
		checks = append(checks, SystemCheck{Name: "Disk space", Detail: "not available", Required: false})
	} else {
		checks = append(checks, SystemCheck{Name: "Disk space", Detail: formatBytes(free) + " free", OK: free > 0, Required: false})
	}
	portsRequired := !installationExists(newInstallLayout(installRoot))
	for _, port := range []struct {
		name string
		port string
	}{
		{"API port", "18080"},
		{"Console port", "13000"},
		{"Proxy port", "8080"},
	} {
		available := portAvailable(port.port)
		detail := "available"
		if !available {
			detail = "already in use"
		}
		checks = append(checks, SystemCheck{Name: port.name, Detail: detail, OK: available, Required: portsRequired})
	}
	return checks
}

func installerPlatformChecks(goos, goarch string) []SystemCheck {
	osCheck := SystemCheck{Name: "OS", Detail: goos, OK: goos == "linux", Required: true}
	if !osCheck.OK {
		osCheck.Detail = fmt.Sprintf("%s (unsupported; Linux is supported)", goos)
	}
	architectureCheck := SystemCheck{
		Name:     "Architecture",
		Detail:   goarch,
		OK:       goarch == "amd64" || goarch == "arm64",
		Required: true,
	}
	if !architectureCheck.OK {
		architectureCheck.Detail = fmt.Sprintf("%s (unsupported; use amd64 or arm64)", goarch)
	}
	return []SystemCheck{osCheck, architectureCheck}
}

func checksPass(checks []SystemCheck) bool {
	for _, check := range checks {
		if check.Required && !check.OK {
			return false
		}
	}
	return true
}

func failedCheckSummary(checks []SystemCheck) string {
	var failed []string
	for _, check := range checks {
		if check.Required && !check.OK {
			failed = append(failed, check.Name+": "+check.Detail)
		}
	}
	return strings.Join(failed, "; ")
}

func renderCheck(check SystemCheck) string {
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

func dockerSocketGID(path string) (uint32, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unsupported file metadata")
	}
	return stat.Gid, nil
}

func freeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func portAvailable(port string) bool {
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return false
	}
	return listener.Close() == nil
}

func formatBytes(value uint64) string {
	const gib = 1 << 30
	const mib = 1 << 20
	if value >= gib {
		return fmt.Sprintf("%.1f GiB", float64(value)/gib)
	}
	return fmt.Sprintf("%.0f MiB", float64(value)/mib)
}

func (a *App) hasInteractiveTerminal() bool {
	if dumbTerminal() {
		return false
	}
	return isCharacterDevice(a.in) && isCharacterDevice(a.out)
}

func isCharacterDevice(value any) bool {
	file, ok := value.(*os.File)
	if !ok || file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
