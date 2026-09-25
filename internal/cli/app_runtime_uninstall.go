package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const appRuntimeSchema = "v1"

type appRuntimePurgeContainer struct {
	ID          string
	Name        string
	Running     bool
	NetworkMode string
}

type appRuntimePurgeNetwork struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Scope      string            `json:"Scope"`
	Internal   bool              `json:"Internal"`
	Labels     map[string]string `json:"Labels"`
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
}

type appRuntimePurgeHostConfig struct {
	NetworkMode    string           `json:"NetworkMode"`
	Privileged     bool             `json:"Privileged"`
	PortBindings   map[string][]any `json:"PortBindings"`
	PidMode        string           `json:"PidMode"`
	IpcMode        string           `json:"IpcMode"`
	UTSMode        string           `json:"UTSMode"`
	UsernsMode     string           `json:"UsernsMode"`
	CapAdd         []string         `json:"CapAdd"`
	ReadonlyRootfs bool             `json:"ReadonlyRootfs"`
	CapDrop        []string         `json:"CapDrop"`
	SecurityOpt    []string         `json:"SecurityOpt"`
}

func appRuntimeNetworkOwned(name string, output []byte) bool {
	var network appRuntimePurgeNetwork
	return json.Unmarshal(output, &network) == nil && network.Name == name && network.Driver == "bridge" &&
		network.Scope == "local" && !network.Internal && network.Labels["stealth.managed"] == "true" &&
		network.Labels["stealth.resource_type"] == "app_runtime_network" && network.Labels["stealth.runtime_schema"] == appRuntimeSchema
}

type appRuntimePurgeInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
	HostConfig appRuntimePurgeHostConfig `json:"HostConfig"`
	Mounts     []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

func configuredRuntimeNetwork(values map[string]string) string {
	name := strings.TrimSpace(values["APPS_RUNTIME_NETWORK_NAME"])
	if name == "" {
		return "stealth_app_runtime"
	}
	return name
}

func (a *App) inspectAppRuntimeResources(ctx context.Context, plan uninstallPlan) ([]appRuntimePurgeContainer, *appRuntimePurgeNetwork, error) {
	if !validDockerResourceName(plan.appRuntimeNetwork) || len(plan.appRuntimeNetwork) > 63 {
		return nil, nil, fmt.Errorf("refusing to inspect invalid App runtime network name %q", plan.appRuntimeNetwork)
	}
	output, err := a.runner.Output(ctx, "", "docker", "container", "ls", "--all", "--quiet", "--no-trunc",
		"--filter", "label=stealth.managed=true", "--filter", "label=stealth.resource_type=app")
	if err != nil {
		return nil, nil, fmt.Errorf("inspect managed App containers: %w", err)
	}
	if len(output) > 1<<20 {
		return nil, nil, fmt.Errorf("managed App container list exceeds the validation limit")
	}
	ids := strings.Fields(string(output))
	if len(ids) > 10000 {
		return nil, nil, fmt.Errorf("managed App container count exceeds the validation limit")
	}
	containers := make([]appRuntimePurgeContainer, 0, len(ids))
	containerIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !validDockerContainerID(id) {
			return nil, nil, fmt.Errorf("refusing to inspect malformed managed App container ID")
		}
		inspect, err := a.inspectAppRuntimeContainer(ctx, id, plan.appRuntimeNetwork)
		if err != nil {
			return nil, nil, err
		}
		containers = append(containers, inspect)
		containerIDs[inspect.ID] = struct{}{}
	}

	network, found, err := a.inspectAppRuntimeNetwork(ctx, plan.appRuntimeNetwork)
	if err != nil {
		return nil, nil, err
	}
	if found {
		for id := range network.Containers {
			if !validDockerContainerID(id) {
				return nil, nil, fmt.Errorf("App runtime network contains a malformed container ID")
			}
			if _, managed := containerIDs[id]; !managed {
				if err := a.inspectAppRuntimePeer(ctx, id); err != nil {
					return nil, nil, fmt.Errorf("App runtime network contains a container outside the managed App ownership set; refusing purge: %w", err)
				}
			}
		}
		for _, container := range containers {
			if _, attached := network.Containers[container.ID]; !attached {
				return nil, nil, fmt.Errorf("managed App container %q is not attached to the owned App runtime network; refusing purge", container.Name)
			}
		}
		return containers, &network, nil
	}
	return containers, nil, nil
}

func (a *App) inspectAppRuntimePeer(ctx context.Context, id string) error {
	output, err := a.runner.CombinedOutput(ctx, "", "docker", "container", "inspect", id)
	if err != nil {
		return fmt.Errorf("inspect App runtime peer: %w", err)
	}
	if len(output) > 1<<20 {
		return fmt.Errorf("App runtime peer inspect exceeds the validation limit")
	}
	var rows []appRuntimePurgeInspect
	if json.Unmarshal(output, &rows) != nil || len(rows) != 1 || !validDockerContainerID(rows[0].ID) || rows[0].ID != id {
		return fmt.Errorf("refusing to purge an unreadable App runtime peer")
	}
	item := rows[0]
	if appRuntimePurgePeerMatches(item) {
		return nil
	}
	return fmt.Errorf("App runtime peer is not an expected hardened worker or Traefik service")
}

func appRuntimePurgePeerMatches(item appRuntimePurgeInspect) bool {
	labels := item.Config.Labels
	if labels["stealth.managed"] != "true" || labels["stealth.runtime_schema"] != appRuntimeSchema || item.HostConfig.Privileged ||
		item.HostConfig.NetworkMode == "host" || item.HostConfig.NetworkMode == "none" || item.HostConfig.PidMode == "host" || item.HostConfig.IpcMode == "host" || item.HostConfig.UTSMode == "host" || item.HostConfig.UsernsMode == "host" ||
		len(item.HostConfig.PortBindings) != 0 || len(item.HostConfig.CapAdd) != 0 {
		return false
	}
	socketMount := false
	for _, mount := range item.Mounts {
		if mount.Type == "bind" && mount.Source == "/var/run/docker.sock" && mount.Destination == "/var/run/docker.sock" {
			socketMount = true
			break
		}
	}
	switch {
	case labels["stealth.resource_type"] == "app_runtime_worker" && labels["com.docker.compose.service"] == "worker":
		return socketMount
	case labels["stealth.resource_type"] == "app_runtime_ingress" && labels["com.docker.compose.service"] == "traefik":
		return !socketMount && item.HostConfig.ReadonlyRootfs && containsString(item.HostConfig.CapDrop, "ALL") &&
			containsString(item.HostConfig.SecurityOpt, "no-new-privileges:true")
	default:
		return false
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (a *App) inspectAppRuntimeContainer(ctx context.Context, id, runtimeNetwork string) (appRuntimePurgeContainer, error) {
	output, err := a.runner.CombinedOutput(ctx, "", "docker", "container", "inspect", id)
	if err != nil {
		return appRuntimePurgeContainer{}, fmt.Errorf("inspect managed App container %q: %w", id, err)
	}
	if len(output) > 1<<20 {
		return appRuntimePurgeContainer{}, fmt.Errorf("managed App container inspect exceeds the validation limit")
	}
	var rows []appRuntimePurgeInspect
	if err := json.Unmarshal(output, &rows); err != nil || len(rows) != 1 {
		return appRuntimePurgeContainer{}, fmt.Errorf("refusing to purge App container with an unreadable inspect result")
	}
	item := rows[0]
	labels := item.Config.Labels
	appID, appErr := uuid.Parse(labels["stealth.app_id"])
	projectID, projectErr := uuid.Parse(labels["stealth.project_id"])
	deploymentID, deploymentErr := uuid.Parse(labels["stealth.deployment_id"])
	generation, generationErr := strconv.ParseInt(labels["stealth.generation"], 10, 64)
	if !validDockerContainerID(item.ID) || item.ID != id || appErr != nil || projectErr != nil || deploymentErr != nil ||
		appID == uuid.Nil || projectID == uuid.Nil || deploymentID == uuid.Nil || generation < 1 || generationErr != nil ||
		labels["stealth.managed"] != "true" || labels["stealth.resource_type"] != "app" || labels["stealth.runtime_schema"] != appRuntimeSchema ||
		!validRuntimeSpecDigest(labels["stealth.workload_spec_sha256"]) || !validAppRuntimePurgeContainerName(appID, strings.TrimPrefix(item.Name, "/")) {
		return appRuntimePurgeContainer{}, fmt.Errorf("App container %q does not have a complete, deterministic Stealth ownership identity; refusing purge", id)
	}
	if !validDockerResourceName(item.Name[1:]) || item.HostConfig.NetworkMode != runtimeNetwork {
		return appRuntimePurgeContainer{}, fmt.Errorf("App container %q has an unsafe name or network mode; refusing purge", id)
	}
	return appRuntimePurgeContainer{ID: item.ID, Name: item.Name[1:], Running: item.State.Running, NetworkMode: item.HostConfig.NetworkMode}, nil
}

func validAppRuntimePurgeContainerName(appID uuid.UUID, name string) bool {
	if appID == uuid.Nil || name != strings.TrimSpace(name) || strings.ContainsRune(name, '/') {
		return false
	}
	app := strings.ReplaceAll(appID.String(), "-", "")
	if name == "stealth-app-"+app {
		return true
	}
	prefix := "st-" + app + "-"
	if len(name) != len(prefix)+24 || !strings.HasPrefix(name, prefix) {
		return false
	}
	for _, character := range name[len(prefix):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func (a *App) inspectAppRuntimeNetwork(ctx context.Context, name string) (appRuntimePurgeNetwork, bool, error) {
	output, err := a.runner.CombinedOutput(ctx, "", "docker", "network", "inspect", "--format", "{{json .}}", name)
	if err != nil {
		message := strings.ToLower(string(output) + " " + err.Error())
		if strings.Contains(message, "no such network") || strings.Contains(message, "network "+strings.ToLower(name)+" not found") {
			return appRuntimePurgeNetwork{}, false, nil
		}
		return appRuntimePurgeNetwork{}, false, fmt.Errorf("inspect App runtime network %q: %w", name, err)
	}
	if len(output) > 1<<20 {
		return appRuntimePurgeNetwork{}, false, fmt.Errorf("App runtime network inspect exceeds the validation limit")
	}
	var network appRuntimePurgeNetwork
	if !appRuntimeNetworkOwned(name, output) || json.Unmarshal(output, &network) != nil {
		return appRuntimePurgeNetwork{}, false, fmt.Errorf("App runtime network %q is not owned by this Stealth runtime; refusing purge", name)
	}
	return network, true, nil
}

func (a *App) validateAppRuntimePurgeScope(ctx context.Context, plan uninstallPlan) error {
	_, _, err := a.inspectAppRuntimeResources(ctx, plan)
	return err
}

func (a *App) removeAppRuntimeResources(ctx context.Context, plan uninstallPlan) error {
	containers, _, err := a.inspectAppRuntimeResources(ctx, plan)
	if err != nil {
		return err
	}
	for _, container := range containers {
		if container.Running {
			if err := a.runCommandCaptured(ctx, "", "docker", "container", "stop", "--time", "15", container.ID); err != nil {
				return fmt.Errorf("stop managed App container %q: %w", container.Name, err)
			}
		}
		if err := a.runCommandCaptured(ctx, "", "docker", "container", "rm", container.ID); err != nil {
			return fmt.Errorf("remove managed App container %q: %w", container.Name, err)
		}
	}
	remaining, _, err := a.inspectAppRuntimeResources(ctx, plan)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("managed App containers remain after purge")
	}
	network, found, err := a.inspectAppRuntimeNetwork(ctx, plan.appRuntimeNetwork)
	if err != nil {
		return err
	}
	if found {
		if len(network.Containers) != 0 {
			return fmt.Errorf("App runtime network still has attached containers; refusing to remove it")
		}
		if err := a.runCommandCaptured(ctx, "", "docker", "network", "rm", plan.appRuntimeNetwork); err != nil {
			return fmt.Errorf("remove owned App runtime network %q: %w", plan.appRuntimeNetwork, err)
		}
		if _, found, err := a.inspectAppRuntimeNetwork(ctx, plan.appRuntimeNetwork); err != nil {
			return err
		} else if found {
			return fmt.Errorf("owned App runtime network %q remains after purge", plan.appRuntimeNetwork)
		}
	}
	return nil
}

func validDockerContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validRuntimeSpecDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
