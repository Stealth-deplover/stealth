package appruntime

import (
	"context"
	"errors"
	"fmt"
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

func parseRuntimeImageInspect(output []byte, references []string) ([]RuntimeImageCacheEntry, error) {
	if len(references) == 0 {
		if len(strings.TrimSpace(string(output))) != 0 {
			return nil, ErrImageVerification
		}
		return nil, nil
	}
	listed := make(map[string]uuid.UUID, len(references))
	for _, reference := range references {
		deploymentID, ok := ParseRuntimeImageReference(reference)
		if !ok {
			return nil, ErrImageVerification
		}
		listed[reference] = deploymentID
	}
	rows := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(rows) == 0 || (len(rows) == 1 && rows[0] == "") {
		return nil, ErrImageVerification
	}
	byReference := make(map[string]RuntimeImageCacheEntry, len(references))
	byImageID := make(map[string]runtimeImageInspectRow)
	for _, line := range rows {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || !validImageID(fields[0]) || strings.ContainsAny(fields[1], " \r\n\x00") {
			return nil, ErrImageVerification
		}
		createdAt, err := time.Parse(time.RFC3339Nano, fields[1])
		if err != nil {
			return nil, ErrImageVerification
		}
		sizeBytes, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || sizeBytes < 0 {
			return nil, ErrImageVerification
		}
		var tags []string
		if fields[3] != "" {
			tags = strings.Split(fields[3], ",")
		}
		if len(tags) == 0 {
			return nil, ErrImageVerification
		}
		row := runtimeImageInspectRow{imageID: fields[0], created: createdAt, size: sizeBytes, tags: tags}
		if previous, exists := byImageID[row.imageID]; exists {
			if previous.size != row.size || !previous.created.Equal(row.created) || !sameImageTags(previous.tags, row.tags) {
				return nil, ErrImageVerification
			}
		} else {
			byImageID[row.imageID] = row
		}
		seenTags := make(map[string]struct{}, len(tags))
		for _, tag := range tags {
			if tag == "" || strings.ContainsAny(tag, "\t\r\n\x00") {
				return nil, ErrImageVerification
			}
			if _, duplicate := seenTags[tag]; duplicate {
				return nil, ErrImageVerification
			}
			seenTags[tag] = struct{}{}
			if strings.HasPrefix(tag, runtimeImageRepositoryPrefix) {
				if _, ok := ParseRuntimeImageReference(tag); !ok {
					return nil, ErrImageVerification
				}
				deploymentID, requested := listed[tag]
				if !requested {
					return nil, ErrImageVerification
				}
				byReference[tag] = RuntimeImageCacheEntry{
					DeploymentID: deploymentID,
					Reference:    tag,
					ImageID:      row.imageID,
					SizeBytes:    row.size,
					CreatedAt:    row.created,
				}
			}
		}
	}
	entries := make([]RuntimeImageCacheEntry, 0, len(references))
	for _, reference := range references {
		entry, found := byReference[reference]
		if !found {
			return nil, ErrImageVerification
		}
		entries = append(entries, entry)
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
