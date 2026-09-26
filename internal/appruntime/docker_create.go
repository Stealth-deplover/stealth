package appruntime

import (
	"context"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"strconv"
)

func (m *Moby) CreateApp(ctx context.Context, job repository.AppRuntimeJob, image Image, environment []RuntimeEnvironmentVariable) (Container, error) {
	environmentFile := ""
	var cleanupEnvironment func() error
	if len(environment) > 0 {
		var err error
		environmentFile, cleanupEnvironment, err = writeRuntimeEnvironmentFile(environment)
		if err != nil {
			return Container{}, ErrContainerCreate
		}
	}
	if cleanupEnvironment != nil {
		defer func() {
			if cleanupEnvironment != nil {
				_ = cleanupEnvironment()
			}
		}()
	}
	args, err := containerCreateArgsWithEnvironmentFile(job, image.Tag, m.NetworkName, m.Security, environmentFile)
	if err != nil {
		return Container{}, err
	}
	_, createErr := m.runAction(ctx, args, nil)
	if cleanupEnvironment != nil {
		cleanupErr := cleanupEnvironment()
		if cleanupErr != nil {
			return Container{}, cleanupErr
		}
		cleanupEnvironment = nil
	}
	container, found, inspectErr := m.InspectApp(ctx, uuid.MustParse(job.App.ID))
	if inspectErr != nil {
		return Container{}, inspectErr
	}
	if !found {
		if createErr != nil {
			return Container{}, errors.Join(ErrContainerCreate, createErr)
		}
		return Container{}, ErrContainerCreate
	}
	return container, nil
}

func ContainerCreateArgs(job repository.AppRuntimeJob, imageRef, networkName string, profile RuntimeSecurityProfile) ([]string, error) {
	return containerCreateArgsWithEnvironmentFile(job, imageRef, networkName, profile, "")
}

func containerCreateArgsWithEnvironmentFile(job repository.AppRuntimeJob, imageRef, networkName string, profile RuntimeSecurityProfile, environmentFile string) ([]string, error) {
	appID, err := uuid.Parse(job.App.ID)
	if err != nil || appID == uuid.Nil {
		return nil, ErrContainerCreate
	}
	projectID, err := uuid.Parse(job.App.ProjectID)
	if err != nil || projectID == uuid.Nil || job.App.DesiredDeploymentID == nil || job.App.WorkloadSpecSHA256 == "" || !validDockerName(networkName) || !validImageTag(imageRef) {
		return nil, ErrContainerCreate
	}
	if job.RouteIdentity == uuid.Nil || job.ContainerName != repository.AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) {
		return nil, ErrContainerCreate
	}
	if profile.Runtime != "" && !validDockerName(profile.Runtime) {
		return nil, ErrContainerCreate
	}
	if environmentFile != "" && !validRuntimeEnvironmentFilePath(environmentFile) {
		return nil, ErrContainerCreate
	}
	spec, err := workloadspec.Normalize(job.App.Workload)
	if err != nil {
		return nil, ErrContainerCreate
	}
	labels := map[string]string{
		"stealth.managed":              "true",
		"stealth.resource_type":        "app",
		"stealth.app_id":               appID.String(),
		"stealth.project_id":           projectID.String(),
		"stealth.deployment_id":        *job.App.DesiredDeploymentID,
		"stealth.generation":           strconv.FormatInt(job.App.DesiredGeneration, 10),
		"stealth.workload_spec_sha256": job.App.WorkloadSpecSHA256,
		"stealth.runtime_schema":       runtimeSchema,
	}
	args := []string{"container", "create", "--name", job.ContainerName, "--network", networkName}
	for _, key := range []string{"stealth.managed", "stealth.resource_type", "stealth.app_id", "stealth.project_id", "stealth.deployment_id", "stealth.generation", "stealth.workload_spec_sha256", "stealth.runtime_schema"} {
		args = append(args, "--label", key+"="+labels[key])
	}
	args = append(args,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true",
		"--cpus", dockerCPUValue(spec.Resources.CPUMillis),
		"--memory", strconv.FormatInt(spec.Resources.MemoryBytes, 10),
		"--memory-swap", strconv.FormatInt(spec.Resources.MemoryBytes, 10),
		"--pids-limit", strconv.Itoa(spec.Resources.PIDsLimit),
		"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=67108864",
		"--restart", "no",
		"--log-driver", "json-file",
		"--log-opt", "max-size=10m",
		"--log-opt", "max-file=3",
		"--ulimit", "nofile=4096:4096",
		"--ulimit", "core=0:0",
		"--init",
	)
	if profile.Runtime != "" {
		args = append(args, "--runtime", profile.Runtime)
	}
	if spec.WorkingDirectory != nil {
		args = append(args, "--workdir", *spec.WorkingDirectory)
	}
	if environmentFile != "" {
		args = append(args, "--env-file", environmentFile)
	}
	args = append(args, imageRef)
	args = append(args, spec.Command...)
	return args, nil
}

func ContainerLabels(job repository.AppRuntimeJob) (map[string]string, error) {
	appID, err := uuid.Parse(job.App.ID)
	if err != nil {
		return nil, ErrInvalidRuntimeJob
	}
	projectID, err := uuid.Parse(job.App.ProjectID)
	if err != nil || job.App.DesiredDeploymentID == nil {
		return nil, ErrInvalidRuntimeJob
	}
	if _, err := uuid.Parse(*job.App.DesiredDeploymentID); err != nil {
		return nil, ErrInvalidRuntimeJob
	}
	return map[string]string{
		"stealth.managed":              "true",
		"stealth.resource_type":        "app",
		"stealth.app_id":               appID.String(),
		"stealth.project_id":           projectID.String(),
		"stealth.deployment_id":        *job.App.DesiredDeploymentID,
		"stealth.generation":           strconv.FormatInt(job.App.DesiredGeneration, 10),
		"stealth.workload_spec_sha256": job.App.WorkloadSpecSHA256,
		"stealth.runtime_schema":       runtimeSchema,
	}, nil
}
