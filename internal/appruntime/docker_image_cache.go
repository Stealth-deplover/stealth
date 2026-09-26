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
	runtimeImageInspectBatchSize      = 128
	maxRuntimeImageGCRemovalsPerSweep = 4
	runtimeImageRepositoryPrefix      = "stealth-app/"
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
// Docker output remains byte-bounded by the command runner; valid references
// are inspected in bounded image-ID batches so lifetime inventory size does
// not prevent later cache entries from being maintained.
func (m *Moby) ListRuntimeImageCache(ctx context.Context) ([]RuntimeImageCacheEntry, error) {
	result, err := m.runAction(ctx, []string{
		"image", "ls", "--all", "--no-trunc", "--filter", "reference=stealth-app/*:runtime",
		"--format", `{{printf "%s:%s\t%s" .Repository .Tag .ID}}`,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheReferenceList, err)
	}
	if result.StdoutTruncated {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheReferenceList, ErrDockerOutputTooLarge)
	}
	listedReferences, imageIDs, err := parseRuntimeImageCacheListing(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheReferenceList, err)
	}
	if len(imageIDs) == 0 {
		return nil, nil
	}
	byReference := make(map[string]RuntimeImageCacheEntry, len(listedReferences))
	for start := 0; start < len(imageIDs); start += runtimeImageInspectBatchSize {
		end := min(start+runtimeImageInspectBatchSize, len(imageIDs))
		args := []string{"image", "inspect", "--format", `{{printf "%s\t%s\t%v\t%s" .Id .Created .Size (join .RepoTags ",")}}`}
		args = append(args, imageIDs[start:end]...)
		result, err := m.runAction(ctx, args, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, err)
		}
		if result.StdoutTruncated {
			return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, ErrDockerOutputTooLarge)
		}
		batchEntries, err := parseRuntimeImageInspectBatch(result.Stdout, imageIDs[start:end])
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, err)
		}
		for _, entry := range batchEntries {
			if previous, exists := byReference[entry.Reference]; exists && !sameRuntimeImageCacheEntry(previous, entry) {
				return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, runtimeImageCacheInspectionFailure("runtime image tag points to conflicting image metadata"))
			}
			byReference[entry.Reference] = entry
		}
	}
	for reference, expectedImageID := range listedReferences {
		entry, ok := byReference[reference]
		if !ok || entry.ImageID != expectedImageID {
			return nil, fmt.Errorf("%w: %w", errRuntimeImageCacheInspection, runtimeImageCacheInspectionFailure("listed runtime image tag missing from inspection"))
		}
	}
	entries := make([]RuntimeImageCacheEntry, 0, len(byReference))
	for _, entry := range byReference {
		entries = append(entries, entry)
	}
	sortRuntimeImageCacheEntries(entries)
	return entries, nil
}

func parseRuntimeImageCacheListing(output []byte) (map[string]string, []string, error) {
	if len(output) == 0 {
		return nil, nil, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	listed := make(map[string]string, len(lines))
	seenImageIDs := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			return nil, nil, ErrImageVerification
		}
		if _, ok := ParseRuntimeImageReference(fields[0]); !ok || !validImageID(fields[1]) {
			return nil, nil, ErrImageVerification
		}
		if previous, exists := listed[fields[0]]; exists {
			if previous != fields[1] {
				return nil, nil, ErrImageVerification
			}
			continue
		}
		listed[fields[0]] = fields[1]
		seenImageIDs[fields[1]] = struct{}{}
	}
	imageIDs := make([]string, 0, len(seenImageIDs))
	for imageID := range seenImageIDs {
		imageIDs = append(imageIDs, imageID)
	}
	slices.Sort(imageIDs)
	return listed, imageIDs, nil
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
	byReference, _, _, err := parseRuntimeImageInspectRows(output)
	if err != nil {
		return nil, err
	}
	listed := make(map[string]uuid.UUID, len(references))
	for _, reference := range references {
		deploymentID, ok := ParseRuntimeImageReference(reference)
		if !ok {
			return nil, runtimeImageCacheInspectionFailure("runtime image reference ownership verification failed")
		}
		listed[reference] = deploymentID
	}
	entries := make([]RuntimeImageCacheEntry, 0, len(references))
	for _, reference := range references {
		entry, found := byReference[reference]
		if !found || entry.DeploymentID != listed[reference] {
			return nil, runtimeImageCacheInspectionFailure("listed runtime image tag missing from inspection")
		}
		entries = append(entries, entry)
	}
	var extraReferences []string
	for reference := range byReference {
		if _, requested := listed[reference]; !requested {
			extraReferences = append(extraReferences, reference)
		}
	}
	slices.Sort(extraReferences)
	for _, reference := range extraReferences {
		entries = append(entries, byReference[reference])
	}
	return entries, nil
}

func parseRuntimeImageInspectBatch(output []byte, imageIDs []string) ([]RuntimeImageCacheEntry, error) {
	if len(imageIDs) == 0 {
		if len(strings.TrimSpace(string(output))) != 0 {
			return nil, runtimeImageCacheInspectionFailure("runtime image inspect returned unexpected rows")
		}
		return nil, nil
	}
	byReference, byImageID, rowCount, err := parseRuntimeImageInspectRows(output)
	if err != nil {
		return nil, err
	}
	expected := make(map[string]struct{}, len(imageIDs))
	for _, imageID := range imageIDs {
		if !validImageID(imageID) {
			return nil, runtimeImageCacheInspectionFailure("runtime image ID malformed")
		}
		expected[imageID] = struct{}{}
	}
	if rowCount != len(expected) || len(byImageID) != len(expected) {
		return nil, runtimeImageCacheInspectionFailure("runtime image inspect returned an incomplete batch")
	}
	for imageID := range byImageID {
		if _, requested := expected[imageID]; !requested {
			return nil, runtimeImageCacheInspectionFailure("runtime image inspect returned an unexpected image")
		}
	}
	entries := make([]RuntimeImageCacheEntry, 0, len(byReference))
	for _, entry := range byReference {
		entries = append(entries, entry)
	}
	sortRuntimeImageCacheEntries(entries)
	return entries, nil
}

func parseRuntimeImageInspectRows(output []byte) (map[string]RuntimeImageCacheEntry, map[string]runtimeImageInspectRow, int, error) {
	rows := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(rows) == 0 || (len(rows) == 1 && rows[0] == "") {
		return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image inspect returned no rows")
	}
	byReference := make(map[string]RuntimeImageCacheEntry)
	byImageID := make(map[string]runtimeImageInspectRow)
	for _, line := range rows {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || strings.ContainsAny(fields[1], " \r\n\x00") {
			return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image metadata row malformed")
		}
		if !validImageID(fields[0]) {
			return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image ID malformed")
		}
		createdAt, err := time.Parse(time.RFC3339Nano, fields[1])
		if err != nil {
			return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image creation time malformed")
		}
		sizeBytes, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image size is not an integer")
		}
		if sizeBytes < 0 {
			return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image size is unavailable")
		}
		var tags []string
		if fields[3] != "" {
			tags = strings.Split(fields[3], ",")
		}
		if len(tags) == 0 {
			return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image has no repository tags")
		}
		row := runtimeImageInspectRow{imageID: fields[0], created: createdAt, size: sizeBytes, tags: tags}
		if previous, exists := byImageID[row.imageID]; exists {
			if previous.size != row.size || !previous.created.Equal(row.created) || !sameImageTags(previous.tags, row.tags) {
				return nil, nil, 0, runtimeImageCacheInspectionFailure("duplicate runtime image metadata disagrees")
			}
		} else {
			byImageID[row.imageID] = row
		}
		seenTags := make(map[string]struct{}, len(tags))
		for _, tag := range tags {
			if tag == "" || strings.ContainsAny(tag, "\t\r\n\x00") {
				return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image tag metadata malformed")
			}
			if _, duplicate := seenTags[tag]; duplicate {
				return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image tag metadata duplicated")
			}
			seenTags[tag] = struct{}{}
			if strings.HasPrefix(tag, runtimeImageRepositoryPrefix) {
				deploymentID, valid := ParseRuntimeImageReference(tag)
				if !valid {
					return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image tag ownership verification failed")
				}
				entry := RuntimeImageCacheEntry{
					DeploymentID: deploymentID,
					Reference:    tag,
					ImageID:      row.imageID,
					SizeBytes:    row.size,
					CreatedAt:    row.created,
				}
				if previous, exists := byReference[tag]; exists && !sameRuntimeImageCacheEntry(previous, entry) {
					return nil, nil, 0, runtimeImageCacheInspectionFailure("runtime image tag points to conflicting image metadata")
				}
				byReference[tag] = entry
			}
		}
	}
	return byReference, byImageID, len(rows), nil
}

func sameRuntimeImageCacheEntry(left, right RuntimeImageCacheEntry) bool {
	return left.DeploymentID == right.DeploymentID && left.Reference == right.Reference && left.ImageID == right.ImageID &&
		left.SizeBytes == right.SizeBytes && left.CreatedAt.Equal(right.CreatedAt)
}

func sortRuntimeImageCacheEntries(entries []RuntimeImageCacheEntry) {
	slices.SortFunc(entries, func(left, right RuntimeImageCacheEntry) int {
		if comparison := strings.Compare(left.DeploymentID.String(), right.DeploymentID.String()); comparison != 0 {
			return comparison
		}
		return strings.Compare(left.Reference, right.Reference)
	})
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
