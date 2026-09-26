package appruntime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

type NetworkInspect struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Driver   string            `json:"Driver"`
	Scope    string            `json:"Scope"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
	IPAM     struct {
		Config []struct {
			Subnet string `json:"Subnet"`
		} `json:"Config"`
	} `json:"IPAM"`
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
}

// EnsureNetwork creates only the separately labeled bridge. Existing names
// are reused only when the inspected driver and ownership labels match.
func (m *Moby) EnsureNetwork(ctx context.Context) error {
	network, found, err := m.inspectNetwork(ctx)
	if err != nil && !errors.Is(err, ErrDockerObjectNotFound) {
		return err
	}
	if found {
		return m.validateNetwork(ctx, network)
	}
	args := []string{"network", "create", "--driver", "bridge"}
	labels := networkLabels()
	for _, key := range []string{"stealth.managed", "stealth.resource_type", "stealth.runtime_schema"} {
		args = append(args, "--label", key+"="+labels[key])
	}
	args = append(args, m.NetworkName)
	_, createErr := m.runAction(ctx, args, nil)
	// A second worker may win creation, or the create response may be lost
	// after Docker made the network. Inspect the result before classifying it.
	network, found, err = m.inspectNetwork(ctx)
	if err != nil {
		return err
	}
	if !found {
		if createErr != nil {
			return createErr
		}
		return ErrRuntimeNetworkConflict
	}
	if err := m.validateNetwork(ctx, network); err != nil {
		return err
	}
	return nil
}

// EnsureRuntimeNetworkPeers validates the owned runtime bridge and joins only
// trusted Compose worker and Traefik peers. It is retried periodically so a
// Compose recreation or Docker daemon restart repairs membership.
func (m *Moby) EnsureRuntimeNetworkPeers(ctx context.Context) error {
	network, found, err := m.inspectNetwork(ctx)
	if err != nil {
		return err
	}
	if !found {
		return ErrRuntimeNetworkConflict
	}
	if err := m.validateNetwork(ctx, network); err != nil {
		return err
	}
	return m.ensureRuntimeNetworkPeers(ctx)
}

func (m *Moby) inspectNetwork(ctx context.Context) (NetworkInspect, bool, error) {
	result, err := m.runAction(ctx, []string{"network", "inspect", m.NetworkName}, nil)
	if errors.Is(err, ErrDockerObjectNotFound) {
		return NetworkInspect{}, false, nil
	}
	if err != nil {
		return NetworkInspect{}, false, err
	}
	var values []NetworkInspect
	if result.StdoutTruncated || json.Unmarshal(result.Stdout, &values) != nil || len(values) != 1 {
		return NetworkInspect{}, false, ErrRuntimeNetworkConflict
	}
	return values[0], true, nil
}

func (m *Moby) validateNetwork(ctx context.Context, network NetworkInspect) error {
	labels := networkLabels()
	if network.Name != m.NetworkName || network.Driver != "bridge" || network.Scope != "local" || network.Internal || !hasLabels(network.Labels, labels) {
		return ErrRuntimeNetworkConflict
	}
	for containerID := range network.Containers {
		if !validRuntimeID(containerID) {
			return ErrRuntimeNetworkConflict
		}
		container, found, err := m.inspectContainer(ctx, containerID)
		if err != nil {
			return err
		}
		if !found || (!managedAppContainer(container) && !managedRuntimeNetworkPeer(container)) {
			return ErrRuntimeNetworkConflict
		}
	}
	return nil
}

// ensureRuntimeNetworkPeers joins only the current trusted worker and
// Traefik containers to the separately owned App bridge. Apps remain isolated
// from all backend networks and no host ports are published.
func (m *Moby) ensureRuntimeNetworkPeers(ctx context.Context) error {
	for _, peer := range []struct {
		resourceType string
		service      string
		optional     bool
	}{
		{resourceType: "app_runtime_worker", service: "worker"},
		{resourceType: "app_runtime_ingress", service: "traefik", optional: true},
	} {
		result, err := m.runAction(ctx, []string{
			"container", "ls", "--quiet", "--no-trunc",
			"--filter", "label=stealth.managed=true",
			"--filter", "label=stealth.resource_type=" + peer.resourceType,
			"--filter", "label=stealth.runtime_schema=" + runtimeSchema,
		}, nil)
		if err != nil || result.StdoutTruncated {
			return errors.Join(ErrRuntimeNetworkConflict, err)
		}
		ids := strings.Fields(string(result.Stdout))
		if len(ids) == 0 && peer.optional {
			continue
		}
		if len(ids) != 1 || !validRuntimeID(ids[0]) {
			return ErrRuntimeNetworkConflict
		}
		container, found, err := m.inspectContainer(ctx, ids[0])
		if err != nil || !found || !runtimeNetworkPeerMatches(container, peer.resourceType, peer.service) {
			return errors.Join(ErrRuntimeNetworkConflict, err)
		}
		network, found, err := m.inspectNetwork(ctx)
		if err != nil || !found || m.validateNetwork(ctx, network) != nil {
			return errors.Join(ErrRuntimeNetworkConflict, err)
		}
		if _, connected := network.Containers[container.ID]; connected {
			continue
		}
		_, connectErr := m.runAction(ctx, []string{"network", "connect", m.NetworkName, container.ID}, nil)
		// A lost response or another worker may have completed the connection.
		network, found, err = m.inspectNetwork(ctx)
		if err != nil || !found || m.validateNetwork(ctx, network) != nil {
			return errors.Join(ErrRuntimeNetworkConflict, connectErr, err)
		}
		if _, connected := network.Containers[container.ID]; !connected {
			return errors.Join(ErrRuntimeNetworkConflict, connectErr)
		}
	}
	return nil
}

func managedRuntimeNetworkPeer(container Container) bool {
	labels := container.Config.Labels
	return runtimeNetworkPeerMatches(container, labels["stealth.resource_type"], labels["com.docker.compose.service"])
}

func runtimeNetworkPeerMatches(container Container, resourceType, service string) bool {
	labels := container.Config.Labels
	if labels["stealth.managed"] != "true" || labels["stealth.runtime_schema"] != runtimeSchema ||
		labels["stealth.resource_type"] != resourceType || labels["com.docker.compose.service"] != service ||
		container.HostConfig.Privileged || container.HostConfig.NetworkMode == "host" || container.HostConfig.NetworkMode == "none" || container.HostConfig.PidMode == "host" || container.HostConfig.IpcMode == "host" || container.HostConfig.UTSMode == "host" || container.HostConfig.UsernsMode == "host" ||
		len(container.HostConfig.PortBindings) != 0 || len(container.HostConfig.CapAdd) != 0 {
		return false
	}
	switch resourceType {
	case "app_runtime_worker":
		return service == "worker" && hasDockerSocketMount(container.Mounts)
	case "app_runtime_ingress":
		return service == "traefik" && container.HostConfig.ReadonlyRootfs && slices.Contains(container.HostConfig.CapDrop, "ALL") &&
			slices.Contains(container.HostConfig.SecurityOpt, "no-new-privileges:true") && !hasMountDestination(container.Mounts, "/var/run/docker.sock")
	default:
		return false
	}
}

func hasMountDestination(mounts []containerMount, destination string) bool {
	for _, mount := range mounts {
		if filepath.Clean(mount.Destination) == destination {
			return true
		}
	}
	return false
}

func hasDockerSocketMount(mounts []containerMount) bool {
	for _, mount := range mounts {
		if mount.Type == "bind" && filepath.Clean(mount.Source) == "/var/run/docker.sock" && filepath.Clean(mount.Destination) == "/var/run/docker.sock" {
			return true
		}
	}
	return false
}

func networkLabels() map[string]string {
	return map[string]string{
		"stealth.managed":        "true",
		"stealth.resource_type":  "app_runtime_network",
		"stealth.runtime_schema": runtimeSchema,
	}
}
