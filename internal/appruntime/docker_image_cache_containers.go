package appruntime

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const managedContainerInspectBatchSize = 128

// ManagedAppImageReference contains only identity needed to protect images
// referenced by a container whose ownership labels have been verified.
type ManagedAppImageReference struct {
	DeploymentID uuid.UUID
	ImageID      string
}

// ListManagedAppImageReferences returns a byte-bounded, minimal projection of
// Docker container identity. It deliberately avoids `docker inspect`'s full
// environment payload while verifying every ownership label in bounded
// batches, independent of lifetime container count.
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
	slices.Sort(ids)
	format := `{{printf "%s\t%s\t%s\t%s" .Id .Image .Name (json .Config.Labels)}}`
	seenRefs := make(map[string]ManagedAppImageReference, len(ids))
	for start := 0; start < len(ids); start += managedContainerInspectBatchSize {
		end := min(start+managedContainerInspectBatchSize, len(ids))
		args := []string{"container", "inspect", "--format", format}
		args = append(args, ids[start:end]...)
		inspected, err := m.runAction(ctx, args, nil)
		if err != nil {
			return nil, err
		}
		if inspected.StdoutTruncated {
			return nil, ErrDockerOutputTooLarge
		}
		batchRefs, err := parseManagedAppImageReferenceBatch(inspected.Stdout, ids[start:end])
		if err != nil {
			return nil, err
		}
		for _, ref := range batchRefs {
			key := ref.DeploymentID.String() + ":" + ref.ImageID
			seenRefs[key] = ref
		}
	}
	refs := make([]ManagedAppImageReference, 0, len(seenRefs))
	for _, ref := range seenRefs {
		refs = append(refs, ref)
	}
	sortManagedAppImageReferences(refs)
	return refs, nil
}

func parseManagedAppImageReferenceBatch(output []byte, ids []string) ([]ManagedAppImageReference, error) {
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) != len(ids) {
		return nil, ErrContainerInspection
	}
	expected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		expected[id] = struct{}{}
	}
	seenIDs := make(map[string]struct{}, len(ids))
	seenRefs := make(map[string]ManagedAppImageReference, len(ids))
	for _, line := range lines {
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 || !validRuntimeID(fields[0]) || !validImageID(fields[1]) || !validManagedDockerContainerName(fields[2]) {
			return nil, ErrContainerInspection
		}
		if _, requested := expected[fields[0]]; !requested {
			return nil, ErrContainerInspection
		}
		if _, duplicate := seenIDs[fields[0]]; duplicate {
			return nil, ErrContainerInspection
		}
		seenIDs[fields[0]] = struct{}{}
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
		seenRefs[key] = ManagedAppImageReference{DeploymentID: deploymentID, ImageID: container.ImageID}
	}
	if len(seenIDs) != len(expected) {
		return nil, ErrContainerInspection
	}
	refs := make([]ManagedAppImageReference, 0, len(seenRefs))
	for _, ref := range seenRefs {
		refs = append(refs, ref)
	}
	sortManagedAppImageReferences(refs)
	return refs, nil
}

func sortManagedAppImageReferences(refs []ManagedAppImageReference) {
	slices.SortFunc(refs, func(left, right ManagedAppImageReference) int {
		if comparison := strings.Compare(left.DeploymentID.String(), right.DeploymentID.String()); comparison != 0 {
			return comparison
		}
		return strings.Compare(left.ImageID, right.ImageID)
	})
}
