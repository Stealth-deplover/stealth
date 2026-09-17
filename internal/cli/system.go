package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/preflight"
)

type SystemCheck = preflight.Check

func (a *App) systemChecks(ctx context.Context, installRoot string) []SystemCheck {
	probes := preflight.NewProbes(a.runner, a.httpClient)
	checks := preflight.PlatformChecks(runtime.GOOS, runtime.GOARCH)
	checks = append(checks, preflight.ResourceChecks(ctx, probes, filepath.Dir(installRoot))...)
	checks = append(checks, preflight.DockerChecks(ctx, probes, true, true)...)
	layout := newInstallLayout(installRoot)
	setupMode := false
	if installationExists(layout) {
		if values, err := readEnvFile(layout.EnvFile); err == nil {
			setupMode = strings.EqualFold(strings.TrimSpace(values["SETUP_MODE"]), "true")
		}
	} else {
		setupMode = true
	}
	portsRequired := !installationExists(layout)
	portSet := []struct {
		name string
		port string
	}{
		{"API port", "18080"},
		{"Console port", "13000"},
		{"Proxy port", "8080"},
	}
	if setupMode {
		portSet = []struct {
			name string
			port string
		}{
			{"Setup API port", "18081"},
			{"Setup Console port", "13001"},
			{"Setup proxy port", "8081"},
		}
	}
	for _, port := range portSet {
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
	return preflight.PlatformChecks(goos, goarch)
}

func checksPass(checks []SystemCheck) bool {
	return preflight.ChecksPass(checks)
}

func failedCheckSummary(checks []SystemCheck) string {
	return preflight.FailedSummary(checks)
}

func renderCheck(check SystemCheck) string {
	return preflight.RenderCheck(check)
}

func dockerSocketGID(path string) (uint32, error) {
	return preflight.DockerSocketGID(path)
}

func freeBytes(path string) (uint64, error) {
	return preflight.FreeBytes(path)
}

func portAvailable(port string) bool {
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return false
	}
	return listener.Close() == nil
}

func formatBytes(value uint64) string {
	return preflight.FormatBytes(value)
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
