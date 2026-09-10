package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type ServiceStatus struct {
	Service string
	State   string
	Health  string
	Status  string
	Exit    int
}

func (status ServiceStatus) Healthy() bool {
	if status.Service == "" {
		return false
	}
	if strings.EqualFold(status.Health, "healthy") {
		return true
	}
	return status.Service == "migrate" && (strings.EqualFold(status.State, "exited") || strings.EqualFold(status.State, "completed")) && status.Exit == 0
}

func (status ServiceStatus) Display() string {
	if status.Health != "" {
		return strings.ToLower(status.Health)
	}
	if status.State != "" {
		return strings.ToLower(status.State)
	}
	if status.Status != "" {
		return status.Status
	}
	return "unknown"
}

func (a *App) composeArgs(layout InstallLayout, args ...string) []string {
	result := []string{"compose", "--env-file", layout.EnvFile, "-f", layout.ComposeFile}
	return append(result, args...)
}

func (a *App) runCompose(ctx context.Context, layout InstallLayout, args ...string) error {
	return a.runCommandCaptured(ctx, layout.Root, "docker", a.composeArgs(layout, args...)...)
}

func (a *App) composeStatuses(ctx context.Context, layout InstallLayout) (map[string]ServiceStatus, error) {
	output, err := a.runner.Output(ctx, layout.Root, "docker", a.composeArgs(layout, "ps", "--format", "json")...)
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	return parseComposeStatuses(output)
}

func parseComposeStatuses(contents []byte) (map[string]ServiceStatus, error) {
	statuses := make(map[string]ServiceStatus)
	trimmed := bytes.TrimSpace(contents)
	if len(trimmed) == 0 {
		return statuses, nil
	}
	var entries []composeStatusJSON
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, fmt.Errorf("parse Compose status: %w", err)
		}
	} else {
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var entry composeStatusJSON
			if err := json.Unmarshal(line, &entry); err != nil {
				return nil, fmt.Errorf("parse Compose status: %w", err)
			}
			entries = append(entries, entry)
		}
	}
	for _, entry := range entries {
		service := strings.TrimSpace(entry.Service)
		if service == "" {
			continue
		}
		statuses[service] = ServiceStatus{
			Service: service,
			State:   entry.State,
			Health:  entry.Health,
			Status:  entry.Status,
			Exit:    entry.ExitCode,
		}
	}
	return statuses, nil
}

type composeStatusJSON struct {
	Service  string `json:"Service"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	Status   string `json:"Status"`
	ExitCode int    `json:"ExitCode"`
}

func anyServiceUnhealthy(statuses map[string]ServiceStatus) bool {
	for _, service := range []string{"api", "worker", "console", "postgres", "redis", "proxy"} {
		if !statuses[service].Healthy() {
			return true
		}
	}
	return false
}

func displayServiceName(service string) string {
	switch service {
	case "api":
		return "API"
	case "worker":
		return "Worker"
	case "console":
		return "Console"
	case "postgres":
		return "PostgreSQL"
	case "redis":
		return "Redis"
	case "proxy":
		return "Proxy"
	case "migrate":
		return "Migration"
	default:
		return service
	}
}

func (a *App) httpStatus(ctx context.Context, endpoint string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	response, err := a.httpClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode, nil
}

func httpStatusDetail(status int, err error) string {
	if err != nil {
		return "unreachable"
	}
	return "HTTP " + strconv.Itoa(status)
}

func (a *App) waitForInstallation(ctx context.Context, plan InstallPlan) error {
	maxWait := time.Duration(a.pollAttempts)*a.pollInterval + 30*time.Second
	waitContext, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	ports := portsFromConfig(map[string]string{
		"API_HOST_PORT":     "18080",
		"CONSOLE_HOST_PORT": "13000",
		"PROXY_HTTP_PORT":   "8080",
	})
	if values, err := readEnvFile(plan.Layout.EnvFile); err == nil {
		ports = portsFromConfig(values)
	}
	endpoints := []string{
		"http://127.0.0.1:" + ports.API + "/healthz",
		"http://127.0.0.1:" + ports.API + "/readyz",
		"http://127.0.0.1:" + ports.API + "/version",
		"http://127.0.0.1:" + ports.Console + "/",
		"http://127.0.0.1:" + ports.Proxy + "/",
	}
	for attempt := 0; attempt < a.pollAttempts; attempt++ {
		if err := waitContext.Err(); err != nil {
			return err
		}
		allReady := true
		for _, endpoint := range endpoints {
			status, err := a.httpStatus(waitContext, endpoint)
			if err != nil || status < 200 || status >= 300 {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		if attempt+1 < a.pollAttempts {
			timer := time.NewTimer(a.pollInterval)
			select {
			case <-waitContext.Done():
				timer.Stop()
				return waitContext.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("services did not become ready after %d checks", a.pollAttempts)
}

func (a *App) installStep(ctx context.Context, plan InstallPlan, step int) error {
	switch step {
	case installStepConfiguration:
		return a.prepareInstallation(ctx, plan)
	case installStepPull:
		return a.runCompose(ctx, plan.Layout, "pull")
	case installStepDependencies:
		return a.runCompose(ctx, plan.Layout, "up", "-d", "postgres", "redis")
	case installStepMigration:
		return a.runCompose(ctx, plan.Layout, "up", "migrate")
	case installStepServices:
		return a.runCompose(ctx, plan.Layout, "up", "-d", "api", "worker", "console", "proxy")
	case installStepVerify:
		return a.waitForInstallation(ctx, plan)
	default:
		return fmt.Errorf("unknown install step %d", step)
	}
}

const (
	installStepConfiguration = iota
	installStepPull
	installStepDependencies
	installStepMigration
	installStepServices
	installStepVerify
)

var installStepNames = []string{
	"Configuration and secrets",
	"Release images",
	"PostgreSQL and Redis",
	"Database migrations",
	"API, Worker, Console, and Proxy",
	"Health and readiness verification",
}

func (a *App) prepareInstallation(ctx context.Context, plan InstallPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(plan.Layout.Root, 0700); err != nil {
		return fmt.Errorf("create installation directory: %w", err)
	}
	if err := os.Chmod(plan.Layout.Root, 0700); err != nil {
		return fmt.Errorf("protect installation directory: %w", err)
	}
	if err := os.MkdirAll(plan.Layout.StateDir, 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if !plan.Existing {
		config, err := generateConfig(plan)
		if err != nil {
			return fmt.Errorf("generate configuration: %w", err)
		}
		if err := writePrivateFile(plan.Layout.EnvFile, config); err != nil {
			return fmt.Errorf("write configuration: %w", err)
		}
	} else if !fileIsPrivate(plan.Layout.EnvFile) {
		return fmt.Errorf("existing configuration permissions are too broad; expected mode 0600")
	}
	if !regularFile(plan.Layout.ComposeFile) {
		compose, err := a.fetchAsset(ctx, a.rawAssetURL(plan.Version, "compose.production.yaml"))
		if err != nil {
			return fmt.Errorf("download production Compose file: %w", err)
		}
		if !bytes.Contains(compose, []byte("services:")) {
			return fmt.Errorf("downloaded Compose file is invalid")
		}
		if err := writeAtomic(plan.Layout.ComposeFile, compose, 0644); err != nil {
			return fmt.Errorf("write production Compose file: %w", err)
		}
	}
	if !regularFile(plan.Layout.ProxyFile) {
		proxy, err := a.fetchAsset(ctx, a.rawAssetURL(plan.Version, "console/deploy/nginx.conf"))
		if err != nil {
			return fmt.Errorf("download proxy configuration: %w", err)
		}
		if !bytes.Contains(proxy, []byte("server {")) {
			return fmt.Errorf("downloaded proxy configuration is invalid")
		}
		if err := writeAtomic(plan.Layout.ProxyFile, proxy, 0644); err != nil {
			return fmt.Errorf("write proxy configuration: %w", err)
		}
	}
	if err := writeAtomic(plan.Layout.VersionFile, []byte(plan.Version+"\n"), 0644); err != nil {
		return fmt.Errorf("write version file: %w", err)
	}
	return nil
}
