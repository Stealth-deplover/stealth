package appruntime

import (
	"fmt"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"slices"
	"strconv"
	"strings"
)

type RuntimeSecurityProfile struct {
	// Runtime is platform-owned. Empty means Docker's configured default.
	Runtime string
}

func ContainerMatchesDesired(container Container, job repository.AppRuntimeJob, image Image, networkName string) bool {
	return strings.TrimPrefix(container.Name, "/") == job.ContainerName && ContainerMatchesDesiredExceptName(container, job, image, networkName)
}

func ContainerMatchesDesiredExceptName(container Container, job repository.AppRuntimeJob, image Image, networkName string) bool {
	labels, err := ContainerLabels(job)
	if err != nil || !managedForApp(container, uuid.MustParse(job.App.ID), uuid.MustParse(job.App.ProjectID)) || !hasLabels(container.Config.Labels, labels) {
		return false
	}
	if container.ImageID != image.ID || container.HostConfig.NetworkMode != networkName || container.HostConfig.Privileged || container.HostConfig.AutoRemove || len(container.HostConfig.CapAdd) != 0 || !container.HostConfig.ReadonlyRootfs ||
		!slices.Contains(container.HostConfig.CapDrop, "ALL") || !slices.Contains(container.HostConfig.SecurityOpt, "no-new-privileges:true") ||
		container.HostConfig.Memory != job.App.Workload.Resources.MemoryBytes || container.HostConfig.MemorySwap != job.App.Workload.Resources.MemoryBytes ||
		container.HostConfig.NanoCpus != int64(job.App.Workload.Resources.CPUMillis)*1_000_000 || container.HostConfig.PidsLimit == nil || *container.HostConfig.PidsLimit != int64(job.App.Workload.Resources.PIDsLimit) ||
		container.HostConfig.RestartPolicy.Name != "no" || container.HostConfig.LogConfig.Type != "json-file" ||
		container.HostConfig.LogConfig.Config["max-size"] != "10m" || container.HostConfig.LogConfig.Config["max-file"] != "3" ||
		container.HostConfig.NetworkMode == "host" || container.HostConfig.PidMode == "host" || container.HostConfig.IpcMode == "host" ||
		container.HostConfig.UTSMode == "host" || container.HostConfig.UsernsMode == "host" || len(container.HostConfig.Binds) != 0 ||
		len(container.HostConfig.VolumesFrom) != 0 || len(container.HostConfig.PortBindings) != 0 || len(container.HostConfig.Devices) != 0 ||
		!exactTmpfs(container.HostConfig.Tmpfs) || !noUnexpectedMounts(container.Mounts) || !hasUlimits(container.HostConfig.Ulimits) || container.HostConfig.Init == nil || !*container.HostConfig.Init {
		return false
	}
	if len(container.Networks) != 1 {
		return false
	}
	if _, ok := container.Networks[networkName]; !ok {
		return false
	}
	command := job.App.Workload.Command
	if len(command) == 0 {
		command = image.Command
	}
	workingDir := image.WorkingDir
	if job.App.Workload.WorkingDirectory != nil {
		workingDir = *job.App.Workload.WorkingDirectory
	}
	// Docker merges the worker-supplied App environment into Config.Env. Runtime
	// values are fenced by the generation labels and change only when the
	// worker creates a new container, so Config.Env cannot be compared directly
	// with the image defaults here.
	return slices.Equal(container.Config.Cmd, command) && slices.Equal(container.Config.Entrypoint, image.Entrypoint) &&
		container.Config.WorkingDir == workingDir && container.Config.User == image.User
}

func managedAppContainer(container Container) bool {
	_, _, valid := validManagedAppContainerIdentity(container)
	return valid
}

func managedForApp(container Container, appID, projectID uuid.UUID) bool {
	if !managedAppContainer(container) || container.Config.Labels["stealth.app_id"] != appID.String() || container.Config.Labels["stealth.project_id"] != projectID.String() {
		return false
	}
	return repository.ValidAppRuntimeContainerName(appID, strings.TrimPrefix(container.Name, "/"))
}

func validRuntimeNameForApp(appID uuid.UUID, name string) bool {
	return repository.ValidAppRuntimeContainerName(appID, name)
}

func validManagedAppContainerIdentity(container Container) (uuid.UUID, uuid.UUID, bool) {
	labels := container.Config.Labels
	appID, appErr := uuid.Parse(labels["stealth.app_id"])
	projectID, projectErr := uuid.Parse(labels["stealth.project_id"])
	deploymentID, deploymentErr := uuid.Parse(labels["stealth.deployment_id"])
	generation, generationErr := strconv.ParseInt(labels["stealth.generation"], 10, 64)
	if labels["stealth.managed"] != "true" || labels["stealth.resource_type"] != "app" || labels["stealth.runtime_schema"] != runtimeSchema ||
		appErr != nil || projectErr != nil || deploymentErr != nil ||
		appID == uuid.Nil || projectID == uuid.Nil || deploymentID == uuid.Nil ||
		labels["stealth.app_id"] != appID.String() || labels["stealth.project_id"] != projectID.String() || labels["stealth.deployment_id"] != deploymentID.String() ||
		generation < 1 || generationErr != nil || strconv.FormatInt(generation, 10) != labels["stealth.generation"] ||
		!validWorkloadSpecDigest(labels["stealth.workload_spec_sha256"]) || !validRuntimeID(container.ID) ||
		!validManagedDockerContainerName(container.Name) {
		return uuid.Nil, uuid.Nil, false
	}
	return appID, projectID, true
}

func validManagedDockerContainerName(value string) bool {
	if len(value) < 2 || len(value) > 256 || value[0] != '/' || strings.Contains(value[1:], "/") {
		return false
	}
	for _, character := range value[1:] {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && !strings.ContainsRune("_.-", character) {
			return false
		}
	}
	return true
}

func validWorkloadSpecDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func hasLabels(actual, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func exactTmpfs(value map[string]string) bool {
	return len(value) == 1 && value["/tmp"] == "rw,nosuid,nodev,noexec,size=67108864"
}

func noUnexpectedMounts(mounts []containerMount) bool {
	// Docker reports explicit volumes and bind mounts here. Tmpfs mounts are
	// configured separately through HostConfig.Tmpfs and do not appear in this
	// list on Moby.
	return len(mounts) == 0
}

func hasUlimits(values []struct {
	Name string `json:"Name"`
	Soft int64  `json:"Soft"`
	Hard int64  `json:"Hard"`
}) bool {
	var fileLimit, coreLimit bool
	for _, value := range values {
		switch value.Name {
		case "nofile":
			fileLimit = value.Soft == 4096 && value.Hard == 4096
		case "core":
			coreLimit = value.Soft == 0 && value.Hard == 0
		}
	}
	return fileLimit && coreLimit
}

func dockerCPUValue(millis int) string {
	whole, fraction := millis/1000, millis%1000
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	value := fmt.Sprintf("%d.%03d", whole, fraction)
	return strings.TrimRight(strings.TrimRight(value, "0"), ".")
}

func validDockerName(value string) bool {
	if len(value) < 1 || len(value) > 63 || value != strings.ToLower(value) {
		return false
	}
	for index, character := range value {
		valid := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.'
		if !valid || (index == 0 && !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9'))) {
			return false
		}
	}
	return true
}

func validRuntimeID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
