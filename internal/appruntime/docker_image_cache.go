package appruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxRuntimeImageCacheEntries  = 256
	maxRuntimeImageCacheRemovals = 4
	runtimeImageRepositoryPrefix = "stealth-app/"
)

var (
	errRuntimeImageCacheReferenceList = errors.New("runtime image cache reference inventory failed")
	errRuntimeImageCacheInspection    = errors.New("runtime image cache inspection failed")
)

// RuntimeImageCacheEntry describes one exact Stealth deployment tag. Multiple
// tags may share an image ID; callers must count SizeBytes once per ID.
type RuntimeImageCacheEntry struct {
	DeploymentID uuid.UUID
	Reference    string
	ImageID      string
	SizeBytes    int64
	CreatedAt    time.Time
}

func ParseRuntimeImageReference(reference string) (uuid.UUID, bool) {
	if len(reference) > 255 || !strings.HasPrefix(reference, runtimeImageRepositoryPrefix) || strings.Count(reference, ":") != 1 {
		return uuid.Nil, false
	}
	parts := strings.SplitN(strings.TrimPrefix(reference, runtimeImageRepositoryPrefix), ":", 2)
	if len(parts) != 2 || parts[1] != "runtime" || strings.Contains(parts[0], "/") {
		return uuid.Nil, false
	}
	deploymentID, err := uuid.Parse(parts[0])
	if err != nil || deploymentID == uuid.Nil || deploymentID.Version() != uuid.Version(7) || deploymentID.String() != parts[0] {
		return uuid.Nil, false
	}
	return deploymentID, true
}

// ListRuntimeImageCache lists only exact, well-formed Stealth runtime tags.
// The first command enumerates references and the second inspects only numeric
// identity/accounting fields, never image environment or other config data.
func (m *Moby) ListRuntimeImageCache(ctx context.Context) ([]RuntimeImageCacheEntry, error) {
	result, err := m.runAction(ctx, []string{
		"image", "ls", "--all", "--no-trunc", "--filter", "reference=stealth-app/*:runtime",
		"--format", "{{.Repository}}:{{.Tag}}",
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheReferenceList, err)
	}
	if result.StdoutTruncated {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheReferenceList, ErrDockerOutputTooLarge)
	}
	references, err := parseRuntimeImageReferenceList(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheReferenceList, err)
	}
	if len(references) == 0 {
		return nil, nil
	}
	args := []string{"image", "inspect", "--format", `{{printf "%s\t%s\t%d\t%s" .Id .Created .Size (join .RepoTags ",")}}`}
	args = append(args, references...)
	result, err = m.runAction(ctx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, err)
	}
	if result.StdoutTruncated {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, ErrDockerOutputTooLarge)
	}
	entries, err := parseRuntimeImageInspect(result.Stdout, references)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, err)
	}
	return entries, nil
}

func parseRuntimeImageReferenceList(output []byte) ([]string, error) {
	if len(output) == 0 {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) > maxRuntimeImageCacheEntries {
		return nil, ErrDockerOutputTooLarge
	}
	seen := make(map[string]struct{}, len(lines))
	references := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if _, ok := ParseRuntimeImageReference(line); !ok {
			return nil, ErrImageVerification
		}
		if _, duplicate := seen[line]; duplicate {
			continue
		}
		seen[line] = struct{}{}
		references = append(references, line)
	}
	if len(references) > maxRuntimeImageCacheEntries {
		return nil, ErrDockerOutputTooLarge
	}
	return references, nil
}

type runtimeImageInspectRow struct {
	imageID string
	created time.Time
	size    int64
	tags    []string
}

// runtimeImageCacheInspectionFailure carries a fixed diagnostic category; it
// never embeds Docker output, which can contain operator-controlled strings.
type runtimeImageCacheInspectionFailure string

func (failure runtimeImageCacheInspectionFailure) Error() string {
	return "runtime image inspection metadata verification failed"
}

func (failure runtimeImageCacheInspectionFailure) Unwrap() error {
	return ErrImageVerification
}

func parseRuntimeImageInspect(output []byte, references []string) ([]RuntimeImageCacheEntry, error) {
	if len(references) == 0 {
		if len(strings.TrimSpace(string(output))) != 0 {
			return nil, runtimeImageCacheInspectionFailure("runtime image inspect returned unexpected rows")
		}
		return nil, nil
	}
	listed := make(map[string]uuid.UUID, len(references))
	for _, reference := range references {
		deploymentID, ok := ParseRuntimeImageReference(reference)
		if !ok {
			return nil, runtimeImageCacheInspectionFailure("runtime image reference ownership verification failed")
		}
		listed[reference] = deploymentID
	}
	rows := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(rows) == 0 || (len(rows) == 1 && rows[0] == "") {
		return nil, runtimeImageCacheInspectionFailure("runtime image inspect returned no rows")
	}
	byReference := make(map[string]RuntimeImageCacheEntry, len(references))
	byImageID := make(map[string]runtimeImageInspectRow)
	for _, line := range rows {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || strings.ContainsAny(fields[1], " \r\n\x00") {
			return nil, runtimeImageCacheInspectionFailure("runtime image metadata row malformed")
		}
		if !validImageID(fields[0]) {
			return nil, runtimeImageCacheInspectionFailure("runtime image ID malformed")
		}
		createdAt, err := time.Parse(time.RFC3339Nano, fields[1])
		if err != nil {
			return nil, runtimeImageCacheInspectionFailure("runtime image creation time malformed")
		}
		sizeBytes, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || sizeBytes < 0 {
			return nil, runtimeImageCacheInspectionFailure("runtime image size malformed")
		}
		var tags []string
		if fields[3] != "" {
			tags = strings.Split(fields[3], ",")
		}
		if len(tags) == 0 {
			return nil, runtimeImageCacheInspectionFailure("runtime image has no repository tags")
		}
		row := runtimeImageInspectRow{imageID: fields[0], created: createdAt, size: sizeBytes, tags: tags}
		if previous, exists := byImageID[row.imageID]; exists {
			if previous.size != row.size || !previous.created.Equal(row.created) || !sameImageTags(previous.tags, row.tags) {
				return nil, runtimeImageCacheInspectionFailure("duplicate runtime image metadata disagrees")
			}
		} else {
			byImageID[row.imageID] = row
		}
		seenTags := make(map[string]struct{}, len(tags))
		for _, tag := range tags {
			if tag == "" || strings.ContainsAny(tag, "\t\r\n\x00") {
				return nil, runtimeImageCacheInspectionFailure("runtime image tag metadata malformed")
			}
			if _, duplicate := seenTags[tag]; duplicate {
				return nil, runtimeImageCacheInspectionFailure("runtime image tag metadata duplicated")
			}
			seenTags[tag] = struct{}{}
			if strings.HasPrefix(tag, runtimeImageRepositoryPrefix) {
				deploymentID, valid := ParseRuntimeImageReference(tag)
				if !valid {
					return nil, runtimeImageCacheInspectionFailure("runtime image tag ownership verification failed")
				}
				entry := RuntimeImageCacheEntry{
					DeploymentID: deploymentID,
					Reference:    tag,
					ImageID:      row.imageID,
					SizeBytes:    row.size,
					CreatedAt:    row.created,
				}
				if previous, exists := byReference[tag]; exists && (previous.ImageID != entry.ImageID || previous.SizeBytes != entry.SizeBytes || !previous.CreatedAt.Equal(entry.CreatedAt)) {
					return nil, runtimeImageCacheInspectionFailure("runtime image tag points to conflicting image metadata")
				}
				byReference[tag] = entry
			}
		}
	}
	entries := make([]RuntimeImageCacheEntry, 0, len(references))
	for _, reference := range references {
		entry, found := byReference[reference]
		if !found {
			return nil, runtimeImageCacheInspectionFailure("listed runtime image tag missing from inspection")
		}
		entries = append(entries, entry)
	}

	// An image can gain another valid Stealth tag after image ls but before
	// inspect, or the same image ID may expose multiple tags. Include any such
	// exact owned tag discovered in RepoTags so protection queries cover it.
	extraReferences := make([]string, 0, len(byReference))
	for reference := range byReference {
		if _, requested := listed[reference]; !requested {
			extraReferences = append(extraReferences, reference)
		}
	}
	slices.Sort(extraReferences)
	for _, reference := range extraReferences {
		entries = append(entries, byReference[reference])
	}
	if len(entries) > maxRuntimeImageCacheEntries {
		return nil, ErrDockerOutputTooLarge
	}
	return entries, nil
}

func sameImageTags(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, tag := range left {
		seen[tag]++
	}
	for _, tag := range right {
		seen[tag]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

// RemoveRuntimeImageTag validates current Docker ownership immediately before
// removing one exact Stealth tag. A tag that disappeared after inventory is
// already converged; no image ID or global prune operation is used.
func (m *Moby) RemoveRuntimeImageTag(ctx context.Context, entry RuntimeImageCacheEntry) error {
	deploymentID, ok := ParseRuntimeImageReference(entry.Reference)
	if !ok || deploymentID != entry.DeploymentID || !validImageID(entry.ImageID) {
		return ErrImageVerification
	}
	result, err := m.runAction(ctx, []string{
		"image", "inspect", "--format", `{{printf "%s\t%s" .Id (join .RepoTags ",")}}`, entry.Reference,
	}, nil)
	if errors.Is(err, ErrDockerObjectNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if result.StdoutTruncated {
		return ErrDockerOutputTooLarge
	}
	lines := strings.Split(strings.TrimSuffix(string(result.Stdout), "\n"), "\n")
	if len(lines) != 1 {
		return ErrImageVerification
	}
	fields := strings.Split(lines[0], "\t")
	if len(fields) != 2 || fields[0] != entry.ImageID {
		return ErrImageVerification
	}
	foundReference := false
	for _, reference := range strings.Split(fields[1], ",") {
		if strings.HasPrefix(reference, runtimeImageRepositoryPrefix) {
			if _, ok := ParseRuntimeImageReference(reference); !ok {
				return ErrImageVerification
			}
		}
		if reference == entry.Reference {
			foundReference = true
		}
	}
	if !foundReference {
		return ErrImageVerification
	}
	_, err = m.runAction(ctx, []string{"image", "rm", entry.Reference}, nil)
	if errors.Is(err, ErrDockerObjectNotFound) {
		return nil
	}
	return err
}
