package appruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"net"
	"slices"
	"strconv"
	"strings"
)

type Container struct {
	ID         string                      `json:"Id"`
	Name       string                      `json:"Name"`
	ImageID    string                      `json:"Image"`
	Config     containerConfig             `json:"Config"`
	State      containerState              `json:"State"`
	HostConfig hostConfig                  `json:"HostConfig"`
	Networks   map[string]ContainerNetwork `json:"-"`
	Mounts     []containerMount            `json:"Mounts"`
}

type ContainerNetwork struct {
	NetworkID string `json:"NetworkID"`
	IPAddress string `json:"IPAddress"`
}

type containerConfig struct {
	Labels     map[string]string `json:"Labels"`
	Cmd        []string          `json:"Cmd"`
	Entrypoint []string          `json:"Entrypoint"`
	Env        []string          `json:"Env"`
	WorkingDir string            `json:"WorkingDir"`
	User       string            `json:"User"`
}

type containerState struct {
	Status    string `json:"Status"`
	Running   bool   `json:"Running"`
	ExitCode  int    `json:"ExitCode"`
	OOMKilled bool   `json:"OOMKilled"`
}

type hostConfig struct {
	Privileged     bool              `json:"Privileged"`
	ReadonlyRootfs bool              `json:"ReadonlyRootfs"`
	AutoRemove     bool              `json:"AutoRemove"`
	CapAdd         []string          `json:"CapAdd"`
	CapDrop        []string          `json:"CapDrop"`
	SecurityOpt    []string          `json:"SecurityOpt"`
	NetworkMode    string            `json:"NetworkMode"`
	Memory         int64             `json:"Memory"`
	MemorySwap     int64             `json:"MemorySwap"`
	NanoCpus       int64             `json:"NanoCpus"`
	PidsLimit      *int64            `json:"PidsLimit"`
	Tmpfs          map[string]string `json:"Tmpfs"`
	Binds          []string          `json:"Binds"`
	VolumesFrom    []string          `json:"VolumesFrom"`
	PortBindings   map[string][]any  `json:"PortBindings"`
	Devices        []any             `json:"Devices"`
	PidMode        string            `json:"PidMode"`
	IpcMode        string            `json:"IpcMode"`
	UTSMode        string            `json:"UTSMode"`
	UsernsMode     string            `json:"UsernsMode"`
	RestartPolicy  struct {
		Name string `json:"Name"`
	} `json:"RestartPolicy"`
	LogConfig struct {
		Type   string            `json:"Type"`
		Config map[string]string `json:"Config"`
	} `json:"LogConfig"`
	Ulimits []struct {
		Name string `json:"Name"`
		Soft int64  `json:"Soft"`
		Hard int64  `json:"Hard"`
	} `json:"Ulimits"`
	Init *bool `json:"Init"`
}

type containerMount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

func (m *Moby) InspectApp(ctx context.Context, appID uuid.UUID) (Container, bool, error) {
	if appID == uuid.Nil {
		return Container{}, false, ErrContainerInspection
	}
	result, err := m.runAction(ctx, []string{
		"container", "ls", "--all", "--quiet", "--no-trunc",
		"--filter", "label=stealth.managed=true",
		"--filter", "label=stealth.resource_type=app",
		"--filter", "label=stealth.app_id=" + appID.String(),
	}, nil)
	if err != nil {
		return Container{}, false, err
	}
	if result.StdoutTruncated {
		return Container{}, false, ErrDockerOutputTooLarge
	}
	ids := strings.Fields(string(result.Stdout))
	if len(ids) > 1 {
		return Container{}, false, ErrRuntimeOwnershipConflict
	}
	if len(ids) == 0 {
		return Container{}, false, nil
	}
	if !validRuntimeID(ids[0]) {
		return Container{}, false, ErrContainerInspection
	}
	return m.inspectContainer(ctx, ids[0])
}

// ProbeApp probes only the current owned container on the Stealth App bridge.
// The destination is rebuilt from that container's Docker inspection and
// checked against the owned network subnet for every probe.
func (m *Moby) ProbeApp(ctx context.Context, job repository.AppHealthCheckJob, expected Container) error {
	if !validHealthCheckJobIdentity(job) || !containerMatchesHealthIdentity(expected, job, m.NetworkName) {
		return ErrHealthRuntimeDrift
	}
	latest, found, err := m.inspectContainer(ctx, expected.ID)
	if err != nil || !found || latest.ID != expected.ID || !containerMatchesHealthIdentity(latest, job, m.NetworkName) {
		return errors.Join(ErrHealthRuntimeDrift, err)
	}
	network, found, err := m.inspectNetwork(ctx)
	if err != nil || !found || m.validateNetwork(ctx, network) != nil {
		return errors.Join(ErrHealthRuntimeDrift, err)
	}
	address, ok := managedContainerAddress(latest, network, m.NetworkName)
	if !ok || address != job.Address {
		return ErrHealthRuntimeDrift
	}
	return runHealthProbe(ctx, address, job.App.Workload)
}

func managedContainerAddress(container Container, network NetworkInspect, networkName string) (string, bool) {
	attachment, attached := container.Networks[networkName]
	if !attached || attachment.IPAddress == "" || network.ID == "" || attachment.NetworkID != network.ID {
		return "", false
	}
	address := net.ParseIP(attachment.IPAddress)
	if address == nil || address.To4() == nil || !address.IsPrivate() || address.IsLoopback() || address.IsUnspecified() || address.IsLinkLocalUnicast() || address.IsMulticast() {
		return "", false
	}
	for _, configured := range network.IPAM.Config {
		_, subnet, err := net.ParseCIDR(configured.Subnet)
		if err == nil && subnet.Contains(address) {
			return address.To4().String(), true
		}
	}
	return "", false
}

func containerAddress(container Container, networkName string) string {
	return container.Networks[networkName].IPAddress
}

func validHealthCheckJobIdentity(job repository.AppHealthCheckJob) bool {
	if job.App.DesiredDeploymentID == nil || job.App.WorkloadSpecSHA256 == "" {
		return false
	}
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	deploymentID, deploymentErr := uuid.Parse(*job.App.DesiredDeploymentID)
	workload, workloadErr := workloadspec.Normalize(job.App.Workload)
	workloadDigest, digestErr := workloadspec.Digest(workload)
	return appErr == nil && projectErr == nil && deploymentErr == nil && appID != uuid.Nil && projectID != uuid.Nil && deploymentID != uuid.Nil &&
		job.RouteIdentity != uuid.Nil && job.ContainerName == repository.AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) &&
		job.App.Enabled && job.App.RuntimeStatus == "running" && job.App.ObservedGeneration == job.App.DesiredGeneration &&
		job.App.DesiredGeneration > 0 && validRuntimeID(job.ContainerID) && job.LeaseToken != uuid.Nil && job.WorkerID != "" &&
		len(job.App.WorkloadSpecSHA256) == 64 && workloadErr == nil && digestErr == nil && workloadDigest == job.App.WorkloadSpecSHA256 &&
		validPrivateProbeAddress(job.Address)
}

func containerMatchesHealthIdentity(container Container, job repository.AppHealthCheckJob, networkName string) bool {
	if !validHealthCheckJobIdentity(job) || job.App.DesiredDeploymentID == nil {
		return false
	}
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	if appErr != nil || projectErr != nil || !managedForApp(container, appID, projectID) ||
		container.Name != "/"+job.ContainerName || container.ID != job.ContainerID || !container.State.Running {
		return false
	}
	deploymentID, err := uuid.Parse(*job.App.DesiredDeploymentID)
	if err != nil {
		return false
	}
	expected := map[string]string{
		"stealth.managed": "true", "stealth.resource_type": "app", "stealth.app_id": appID.String(),
		"stealth.project_id": projectID.String(), "stealth.deployment_id": deploymentID.String(),
		"stealth.generation":           strconv.FormatInt(job.App.DesiredGeneration, 10),
		"stealth.workload_spec_sha256": job.App.WorkloadSpecSHA256, "stealth.runtime_schema": runtimeSchema,
	}
	if !hasLabels(container.Config.Labels, expected) || container.HostConfig.NetworkMode != networkName || len(container.Networks) != 1 ||
		container.HostConfig.Privileged || container.HostConfig.AutoRemove || len(container.HostConfig.CapAdd) != 0 || !container.HostConfig.ReadonlyRootfs ||
		!slices.Contains(container.HostConfig.CapDrop, "ALL") || !slices.Contains(container.HostConfig.SecurityOpt, "no-new-privileges:true") ||
		container.HostConfig.Memory != job.App.Workload.Resources.MemoryBytes || container.HostConfig.MemorySwap != job.App.Workload.Resources.MemoryBytes ||
		container.HostConfig.NanoCpus != int64(job.App.Workload.Resources.CPUMillis)*1_000_000 || container.HostConfig.PidsLimit == nil || *container.HostConfig.PidsLimit != int64(job.App.Workload.Resources.PIDsLimit) ||
		container.HostConfig.NetworkMode == "host" || container.HostConfig.PidMode == "host" || container.HostConfig.IpcMode == "host" ||
		container.HostConfig.UTSMode == "host" || container.HostConfig.UsernsMode == "host" || len(container.HostConfig.Binds) != 0 ||
		len(container.HostConfig.VolumesFrom) != 0 || len(container.HostConfig.PortBindings) != 0 || len(container.HostConfig.Devices) != 0 ||
		!exactTmpfs(container.HostConfig.Tmpfs) || !noUnexpectedMounts(container.Mounts) {
		return false
	}
	_, ok := container.Networks[networkName]
	return ok
}

func (m *Moby) inspectContainer(ctx context.Context, identifier string) (Container, bool, error) {
	result, err := m.runAction(ctx, []string{"container", "inspect", identifier}, nil)
	if errors.Is(err, ErrDockerObjectNotFound) {
		return Container{}, false, nil
	}
	if err != nil {
		return Container{}, false, err
	}
	var values []struct {
		Container
		NetworkSettings struct {
			Networks map[string]ContainerNetwork `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if result.StdoutTruncated || json.Unmarshal(result.Stdout, &values) != nil || len(values) != 1 {
		return Container{}, false, ErrContainerInspection
	}
	container := values[0].Container
	if container.ID == "" || !validRuntimeID(container.ID) || container.Name == "" {
		return Container{}, false, ErrContainerInspection
	}
	container.Networks = values[0].NetworkSettings.Networks
	if container.Networks == nil {
		container.Networks = map[string]ContainerNetwork{}
	}
	return container, true, nil
}

func (m *Moby) ListManagedAppContainers(ctx context.Context) ([]Container, error) {
	result, err := m.runAction(ctx, []string{
		"container", "ls", "--all", "--quiet", "--no-trunc",
		"--filter", "label=stealth.managed=true",
		"--filter", "label=stealth.resource_type=app",
	}, nil)
	if err != nil {
		return nil, err
	}
	if result.StdoutTruncated {
		return nil, ErrDockerOutputTooLarge
	}
	lines := strings.Fields(string(result.Stdout))
	if len(lines) > maxContainerList {
		return nil, ErrDockerOutputTooLarge
	}
	containers := make([]Container, 0, len(lines))
	for _, id := range lines {
		if !validRuntimeID(id) {
			return nil, ErrContainerInspection
		}
		container, found, err := m.inspectContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		if found {
			containers = append(containers, container)
		}
	}
	return containers, nil
}
