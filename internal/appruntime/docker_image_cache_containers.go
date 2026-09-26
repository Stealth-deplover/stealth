package appruntime

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const maxRuntimeImageProtectionContainers = 512

// ManagedAppImageReference contains only identity needed to protect images
// referenced by a container whose ownership labels have been verified.
type ManagedAppImageReference struct {
	DeploymentID uuid.UUID
	ImageID      string
}

// ListManagedAppImageReferences returns a bounded, minimal projection of
// Docker container identity. It deliberately avoids `docker inspect`'s full
// environment payload while verifying every ownership label.
func (m *Moby) ListManagedAppImageReferences(ctx context.Context) ([]ManagedAppImageReference, error) {
	listed, err := m.runAction(ctx, []string{
		"container", "ls", "--all", "--quiet", "--no-trunc",
		"--filter", "label=stealth.managed=true",
		"--filter", "label=stealth.resource_type=app",
	}, nil)
	if err != nil {
		return nil, err
	}
	if listed.StdoutTruncated {
		return nil, ErrDockerOutputTooLarge
	}
	ids := strings.Fields(string(listed.Stdout))
	if len(ids) > maxRuntimeImageProtectionContainers {
		return nil, ErrDockerOutputTooLarge
	}
	if len(ids) == 0 {
		return nil, nil
	}
	seenIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !validRuntimeID(id) {
			return nil, ErrContainerInspection
		}
		if _, duplicate := seenIDs[id]; duplicate {
			return nil, ErrContainerInspection
		}
		seenIDs[id] = struct{}{}
	}
	format := `{{printf "%s\t%s\t%s\t%s" .Id .Image .Name (json .Config.Labels)}}`
	args := []string{"container", "inspect", "--format", format}
	args = append(args, ids...)
	inspected, err := m.runAction(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	if inspected.StdoutTruncated {
		return nil, ErrDockerOutputTooLarge
	}
	lines := strings.Split(strings.TrimSuffix(string(inspected.Stdout), "\n"), "\n")
	if len(lines) != len(ids) {
		return nil, ErrContainerInspection
	}
	seenRefs := make(map[string]ManagedAppImageReference, len(ids))
	for _, line := range lines {
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 || !validRuntimeID(fields[0]) || !validImageID(fields[1]) || !validManagedDockerContainerName(fields[2]) {
			return nil, ErrContainerInspection
		}
		var labels map[string]string
		if err := json.Unmarshal([]byte(fields[3]), &labels); err != nil || labels == nil {
			return nil, ErrContainerInspection
		}
		container := Container{ID: fields[0], ImageID: fields[1], Name: fields[2], Config: containerConfig{Labels: labels}}
		appID, projectID, valid := validManagedAppContainerIdentity(container)
		if !valid || !managedForApp(container, appID, projectID) {
			return nil, ErrRuntimeOwnershipConflict
		}
		deploymentID, _ := uuid.Parse(labels["stealth.deployment_id"])
		key := deploymentID.String() + ":" + container.ImageID
		if previous, duplicate := seenRefs[key]; duplicate {
			if previous.DeploymentID != deploymentID || previous.ImageID != container.ImageID {
				return nil, ErrContainerInspection
			}
			continue
		}
		seenRefs[key] = ManagedAppImageReference{DeploymentID: deploymentID, ImageID: container.ImageID}
	}
	refs := make([]ManagedAppImageReference, 0, len(seenRefs))
	for _, ref := range seenRefs {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(left, right ManagedAppImageReference) int {
		if comparison := strings.Compare(left.DeploymentID.String(), right.DeploymentID.String()); comparison != 0 {
			return comparison
		}
		return strings.Compare(left.ImageID, right.ImageID)
	})
	return refs, nil
}
