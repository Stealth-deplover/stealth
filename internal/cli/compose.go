package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/installengine"
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
	composeFile := layout.ComposeFile
	if values, err := readEnvFile(layout.EnvFile); err == nil && strings.EqualFold(values["SETUP_MODE"], "true") && layout.SetupComposeFile != "" {
		composeFile = layout.SetupComposeFile
	}
	result := []string{"compose", "--env-file", layout.EnvFile, "-f", composeFile}
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
	return anyRequiredServiceUnhealthy(statuses, false)
}

func anyRequiredServiceUnhealthy(statuses map[string]ServiceStatus, setup bool) bool {
	services := []string{"api", "worker", "console", "postgres", "redis", "proxy"}
	if setup {
		services = []string{"postgres", "redis", "setup", "setup-console", "setup-proxy"}
	}
	for _, service := range services {
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
	case "clickhouse":
		return "ClickHouse"
	case "otel-collector":
		return "OTel Collector"
	case "telemetry-docker-proxy":
		return "Docker Metrics Proxy"
	case "telemetry-docker":
		return "Docker Metrics"
	case "proxy":
		return "Proxy"
	case "setup":
		return "Setup API"
	case "setup-console":
		return "Setup Console"
	case "setup-proxy":
		return "Setup Proxy"
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
	return a.installEngine().Wait(ctx, plan)
}

func (a *App) installStep(ctx context.Context, plan InstallPlan, step int) error {
	return a.installEngine().RunStep(ctx, plan, installengine.Step(step))
}

const (
	installStepConfiguration = int(installengine.StepConfiguration)
	installStepPull          = int(installengine.StepPull)
	installStepDependencies  = int(installengine.StepDependencies)
	installStepMigration     = int(installengine.StepMigration)
	installStepServices      = int(installengine.StepServices)
	installStepVerify        = int(installengine.StepVerify)
)

var installStepNames = installengine.StepNames

func (a *App) prepareInstallation(ctx context.Context, plan InstallPlan) error {
	return a.installEngine().Prepare(ctx, plan)
}

func (a *App) installEngine() *installengine.Engine {
	output := io.Discard
	if a.verbose {
		output = a.errOut
	}
	return installengine.New(installengine.Options{
		Runner:       a.runner,
		HTTPClient:   a.httpClient,
		AssetBaseURL: a.assetBase,
		Output:       output,
		PollAttempts: a.pollAttempts,
		PollInterval: a.pollInterval,
	})
}
