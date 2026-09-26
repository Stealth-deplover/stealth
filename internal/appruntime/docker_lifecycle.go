package appruntime

import (
	"context"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"strconv"
	"strings"
)

// RenameApp rotates the container's Docker DNS identity before a restarted
// process starts. Docker updates the endpoint's DNS names with the rename, so
// an old Traefik snapshot cannot resolve its old target to the new process.
func (m *Moby) RenameApp(ctx context.Context, job repository.AppRuntimeJob, containerID, targetName string) (Container, error) {
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	if appErr != nil || projectErr != nil || appID == uuid.Nil || projectID == uuid.Nil ||
		!validRuntimeID(containerID) || targetName != job.ContainerName ||
		targetName != repository.AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) {
		return Container{}, ErrRuntimeOwnershipConflict
	}
	container, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found {
		if err != nil {
			return Container{}, err
		}
		return Container{}, ErrRuntimeOwnershipConflict
	}
	if container.ID != containerID || !managedForApp(container, appID, projectID) || !repository.ValidAppRuntimeContainerName(appID, targetName) {
		return Container{}, ErrRuntimeOwnershipConflict
	}
	if strings.TrimPrefix(container.Name, "/") != targetName {
		if _, err := m.runAction(ctx, []string{"container", "rename", container.ID, targetName}, nil); err != nil {
			return Container{}, errors.Join(ErrRuntimeOwnershipConflict, err)
		}
	}
	renamed, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found || renamed.ID != containerID || renamed.Name != "/"+targetName || !managedForApp(renamed, appID, projectID) {
		if err != nil {
			return Container{}, err
		}
		return Container{}, ErrRuntimeOwnershipConflict
	}
	return renamed, nil
}

func (m *Moby) StartApp(ctx context.Context, job repository.AppRuntimeJob, containerID string) (Container, error) {
	if !validRuntimeID(containerID) {
		return Container{}, ErrContainerStart
	}
	_, startErr := m.runAction(ctx, []string{"container", "start", containerID}, nil)
	container, found, inspectErr := m.InspectApp(ctx, uuid.MustParse(job.App.ID))
	if inspectErr != nil {
		return Container{}, inspectErr
	}
	if !found || container.ID != containerID || !container.State.Running {
		if startErr != nil {
			return Container{}, errors.Join(ErrContainerStart, startErr)
		}
		return Container{}, ErrContainerStart
	}
	return container, nil
}

func (m *Moby) RemoveApp(ctx context.Context, job repository.AppRuntimeJob, containerID string) error {
	if !validRuntimeID(containerID) {
		return ErrRuntimeOwnershipConflict
	}
	appID := uuid.MustParse(job.App.ID)
	container, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found {
		return err
	}
	if container.ID != containerID || !managedForApp(container, appID, uuid.MustParse(job.App.ProjectID)) {
		return ErrRuntimeOwnershipConflict
	}
	return m.stopAndRemove(ctx, containerID, job.App.Workload.StopGracePeriodSeconds)
}

func (m *Moby) RemoveCleanupTarget(ctx context.Context, job repository.AppRuntimeCleanupJob) error {
	identifier := job.ContainerName
	if job.ContainerID != nil {
		identifier = *job.ContainerID
	}
	container, found, err := m.inspectContainer(ctx, identifier)
	if err != nil || !found {
		return err
	}
	ownerAppID, ownerProjectID, validOwner := validManagedAppContainerIdentity(container)
	if !validOwner || ownerAppID != job.AppID {
		return ErrRuntimeOwnershipConflict
	}
	if job.ProjectID != nil && ownerProjectID != *job.ProjectID {
		return ErrRuntimeOwnershipConflict
	}
	if job.ContainerID != nil && container.ID != *job.ContainerID {
		return ErrRuntimeOwnershipConflict
	}
	storedName := strings.TrimPrefix(job.ContainerName, "/")
	actualName := strings.TrimPrefix(container.Name, "/")
	if actualName != storedName && (!repository.ValidAppRuntimeContainerName(job.AppID, actualName) || !repository.ValidAppRuntimeContainerName(job.AppID, storedName)) {
		return ErrRuntimeOwnershipConflict
	}
	if job.ContainerID == nil && !validRuntimeNameForApp(job.AppID, storedName) {
		return ErrRuntimeOwnershipConflict
	}
	return m.stopAndRemove(ctx, container.ID, job.StopGracePeriodSecs)
}

func (m *Moby) stopAndRemove(ctx context.Context, containerID string, grace int) error {
	if !validRuntimeID(containerID) || grace < 1 || grace > 120 {
		return ErrRuntimeOwnershipConflict
	}
	container, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found {
		return err
	}
	if container.ID != containerID {
		return ErrRuntimeOwnershipConflict
	}
	if container.State.Running {
		_, stopErr := m.runStopAction(ctx, []string{"container", "stop", "--time", strconv.Itoa(grace), container.ID}, grace)
		stopped, stillFound, inspectErr := m.inspectContainer(ctx, container.ID)
		if inspectErr != nil {
			return inspectErr
		}
		if stillFound && stopped.State.Running {
			if stopErr != nil {
				return errors.Join(ErrRuntimeUnavailable, stopErr)
			}
			return ErrRuntimeUnavailable
		}
		if !stillFound {
			return nil
		}
	}
	_, removeErr := m.runAction(ctx, []string{"container", "rm", containerID}, nil)
	remaining, found, inspectErr := m.inspectContainer(ctx, containerID)
	if inspectErr != nil {
		return inspectErr
	}
	if found {
		_ = remaining
		if removeErr != nil {
			return errors.Join(ErrRuntimeUnavailable, removeErr)
		}
		return ErrRuntimeUnavailable
	}
	return nil
}
