package appruntime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
)

func cacheEntry(t *testing.T, deploymentID uuid.UUID, imageCharacter byte, size int64, createdAt time.Time) RuntimeImageCacheEntry {
	t.Helper()
	if deploymentID == uuid.Nil || deploymentID.Version() != uuid.Version(7) {
		t.Fatalf("test deployment identity is not UUIDv7: %s", deploymentID)
	}
	return RuntimeImageCacheEntry{
		DeploymentID: deploymentID,
		Reference:    ImageTag(deploymentID),
		ImageID:      "sha256:" + strings.Repeat(string(imageCharacter), 64),
		SizeBytes:    size,
		CreatedAt:    createdAt,
	}
}

func newImageCacheWorker(t *testing.T) (*Worker, *runtimeTestStore, *runtimeTestDriver) {
	t.Helper()
	worker, store, driver, _, _ := newRuntimeWorkerFixture(t, false)
	worker.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	worker.ImageCacheMaxBytes = 5 << 20
	worker.ImageCacheTargetBytes = 2 << 20
	worker.ImageCacheGCSweepInterval = time.Minute
	worker.Metrics = observability.NewWorkerMetrics()
	return worker, store, driver
}

func TestRuntimeImageCacheSweepProtectsDesiredContainerAndCurrentDeployments(t *testing.T) {
	worker, store, driver := newImageCacheWorker(t)
	now := time.Now().UTC()
	desiredID, containerID, currentID, unusedOldID, unusedNewID :=
		newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t)
	entries := []RuntimeImageCacheEntry{
		cacheEntry(t, unusedNewID, 'a', 2<<20, now.Add(-time.Minute)),
		cacheEntry(t, currentID, 'b', 2<<20, now.Add(-5*time.Minute)),
		cacheEntry(t, desiredID, 'c', 2<<20, now.Add(-4*time.Minute)),
		cacheEntry(t, unusedOldID, 'd', 2<<20, now.Add(-6*time.Minute)),
		cacheEntry(t, containerID, 'e', 2<<20, now.Add(-3*time.Minute)),
	}
	driver.runtimeImageCache = entries
	driver.containerImageRefs = []ManagedAppImageReference{{DeploymentID: containerID, ImageID: entries[4].ImageID}}
	store.protectedDeployments = []uuid.UUID{desiredID}

	worker.sweepRuntimeImageCacheIfDue(context.Background(), currentID)
	if len(driver.removedImageTags) != 2 {
		t.Fatalf("evicted tags = %#v, want the two safe tags", driver.removedImageTags)
	}
	want := []string{ImageTag(unusedOldID), ImageTag(unusedNewID)}
	for _, protectedID := range []uuid.UUID{desiredID, containerID, currentID} {
		if slices.Contains(driver.removedImageTags, ImageTag(protectedID)) {
			t.Fatalf("protected deployment %s was evicted: %#v", protectedID, driver.removedImageTags)
		}
	}
	for _, reference := range want {
		if !slices.Contains(driver.removedImageTags, reference) {
			t.Fatalf("safe unused tag %s was not selected: %#v", reference, driver.removedImageTags)
		}
	}
	var pressureMetric dto.Metric
	if err := worker.Metrics.AppRuntimeImageCachePressure.Write(&pressureMetric); err != nil {
		t.Fatal(err)
	}
	if pressureMetric.GetGauge().GetValue() != 1 {
		// Cache pressure remains because protected images alone exceed the max.
		t.Fatal("protected-only cache pressure was not retained")
	}
}

func TestRuntimeImageCacheSweepUsesStableDeploymentOrdering(t *testing.T) {
	worker, _, driver := newImageCacheWorker(t)
	worker.ImageCacheMaxBytes = 4 << 20
	worker.ImageCacheTargetBytes = 1 << 20
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ids := []uuid.UUID{newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t)}
	entries := make([]RuntimeImageCacheEntry, 0, len(ids))
	for index, id := range ids {
		entries = append(entries, cacheEntry(t, id, byte('f'-index), 1<<20, created.Add(-time.Duration(index)*24*time.Hour)))
	}
	slices.Reverse(entries)
	driver.runtimeImageCache = entries

	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	orderedIDs := slices.Clone(ids)
	slices.SortFunc(orderedIDs, func(left, right uuid.UUID) int { return strings.Compare(left.String(), right.String()) })
	want := []string{ImageTag(orderedIDs[0]), ImageTag(orderedIDs[1]), ImageTag(orderedIDs[2]), ImageTag(orderedIDs[3])}
	if !slices.Equal(driver.removedImageTags, want) {
		t.Fatalf("eviction order = %#v, want %#v", driver.removedImageTags, want)
	}
}

func TestRuntimeImageCacheSweepBoundsRemovalsAndFailsClosed(t *testing.T) {
	worker, store, driver := newImageCacheWorker(t)
	worker.ImageCacheMaxBytes = 5 << 20
	worker.ImageCacheTargetBytes = 1 << 20
	now := time.Now().UTC()
	for index := range 10 {
		id := newCacheDeploymentID(t)
		entry := cacheEntry(t, id, 'a', 1<<20, now.Add(time.Duration(index)*time.Second))
		entry.ImageID = fmt.Sprintf("sha256:%064x", index+1)
		driver.runtimeImageCache = append(driver.runtimeImageCache, entry)
	}
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != maxRuntimeImageCacheRemovals {
		t.Fatalf("image removals in one sweep = %d, want at most %d", len(driver.removedImageTags), maxRuntimeImageCacheRemovals)
	}

	worker, store, driver = newImageCacheWorker(t)
	store.activeRuntimeLease = true
	id := newCacheDeploymentID(t)
	driver.runtimeImageCache = []RuntimeImageCacheEntry{cacheEntry(t, id, 'b', 6<<20, now)}
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != 0 {
		t.Fatalf("active fenced worker image was evicted: %#v", driver.removedImageTags)
	}

	worker, store, driver = newImageCacheWorker(t)
	id = newCacheDeploymentID(t)
	malformed := cacheEntry(t, id, 'c', 6<<20, now)
	malformed.Reference = "stealth-app/not-a-deployment:runtime"
	driver.runtimeImageCache = []RuntimeImageCacheEntry{malformed}
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != 0 || store.failureCalls != 0 {
		t.Fatalf("malformed ownership affected deletion/App state: removed=%#v app failures=%d", driver.removedImageTags, store.failureCalls)
	}

	worker, _, driver = newImageCacheWorker(t)
	driver.listImageCacheErr = ErrRuntimeUnavailable
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != 0 {
		t.Fatalf("Docker inventory failure removed an image: %#v", driver.removedImageTags)
	}
}

func TestRuntimeImageCacheRemovalFailureIsMaintenanceOnlyAndRetriesLater(t *testing.T) {
	worker, store, driver := newImageCacheWorker(t)
	deploymentID := newCacheDeploymentID(t)
	entry := cacheEntry(t, deploymentID, 'd', 6<<20, time.Now().UTC())
	driver.runtimeImageCache = []RuntimeImageCacheEntry{entry}
	driver.removeImageTagErr = ErrRuntimeUnavailable

	before := time.Now()
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != 1 || store.failureCalls != 0 {
		t.Fatalf("cache removal failure changed App state: removals=%v app failures=%d", driver.removedImageTags, store.failureCalls)
	}
	if worker.imageCacheFailureCount != 1 || !worker.nextImageCacheSweep.After(before) {
		t.Fatalf("cache failure did not schedule a bounded retry: count=%d at=%s", worker.imageCacheFailureCount, worker.nextImageCacheSweep)
	}
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != 1 {
		t.Fatalf("cache sweep retried before its backoff: removals=%v", driver.removedImageTags)
	}
	var pressure dto.Metric
	pressureErr := worker.Metrics.AppRuntimeImageCachePressure.Write(&pressure)
	pressureValue := float64(-1)
	if pressureErr == nil && pressure.GetGauge() != nil {
		pressureValue = pressure.GetGauge().GetValue()
	}
	if pressureErr != nil || pressureValue != 1 {
		t.Fatalf("cache removal failure did not report pressure: value=%v err=%v", pressureValue, pressureErr)
	}

	driver.removeImageTagErr = nil
	worker.nextImageCacheSweep = time.Time{}
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != 2 || store.failureCalls != 0 {
		t.Fatalf("later cache sweep did not retry independently of App state: removals=%v app failures=%d", driver.removedImageTags, store.failureCalls)
	}
	if worker.imageCacheFailureCount != 0 {
		t.Fatalf("successful cache sweep did not reset failure count: %d", worker.imageCacheFailureCount)
	}
	pressureErr = worker.Metrics.AppRuntimeImageCachePressure.Write(&pressure)
	pressureValue = -1
	if pressureErr == nil && pressure.GetGauge() != nil {
		pressureValue = pressure.GetGauge().GetValue()
	}
	if pressureErr != nil || pressureValue != 0 {
		t.Fatalf("cache pressure remained after successful retry: value=%v err=%v", pressureValue, pressureErr)
	}
	families, err := worker.Metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "stealth_apps_worker_runtime_image_gc_total" {
			continue
		}
		results := map[string]float64{}
		for _, metric := range family.Metric {
			results[metric.Label[0].GetValue()] = metric.GetCounter().GetValue()
		}
		if results["error"] != 1 || results["completed"] != 1 {
			t.Fatalf("GC failure/retry result counters = %v", results)
		}
		return
	}
	t.Fatal("runtime image GC counter family was not registered")
}

func TestMaintenanceBackoffIsExponentialAndBounded(t *testing.T) {
	want := []time.Duration{
		time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute,
		32 * time.Minute, 64 * time.Minute, 128 * time.Minute, 256 * time.Minute,
		512 * time.Minute, 1024 * time.Minute, maxRuntimeImageGCBackoff,
	}
	for attempt, expected := range want {
		if got := boundedMaintenanceBackoff(time.Minute, attempt+1, maxRuntimeImageGCBackoff); got != expected {
			t.Fatalf("maintenance retry %d = %s, want %s", attempt+1, got, expected)
		}
	}
	if got := boundedMaintenanceBackoff(0, 0, maxOrphanSweepBackoff); got != time.Second {
		t.Fatalf("zero base/attempt backoff = %s, want nonzero safe minimum", got)
	}
}

func TestRuntimeImageCacheSweepCountsSharedImageIDOnce(t *testing.T) {
	worker, _, driver := newImageCacheWorker(t)
	worker.ImageCacheMaxBytes = 4 << 20
	worker.ImageCacheTargetBytes = 2 << 20
	now := time.Now().UTC()
	first, second := newCacheDeploymentID(t), newCacheDeploymentID(t)
	sharedID := "sha256:" + strings.Repeat("a", 64)
	entries := []RuntimeImageCacheEntry{
		{DeploymentID: first, Reference: ImageTag(first), ImageID: sharedID, SizeBytes: 3 << 20, CreatedAt: now},
		{DeploymentID: second, Reference: ImageTag(second), ImageID: sharedID, SizeBytes: 3 << 20, CreatedAt: now.Add(time.Second)},
	}
	driver.runtimeImageCache = entries
	worker.sweepRuntimeImageCacheIfDue(context.Background(), uuid.Nil)
	if len(driver.removedImageTags) != 0 {
		t.Fatalf("same shared image was counted twice and needlessly evicted: %#v", driver.removedImageTags)
	}
}

func TestRuntimeProcessExitObservabilityUsesFixedReasons(t *testing.T) {
	worker, _, _ := newImageCacheWorker(t)
	_, _, _, job, _ := newRuntimeWorkerFixture(t, false)
	for _, test := range []struct {
		name   string
		state  containerState
		reason string
	}{
		{name: "out of memory", state: containerState{Status: "exited", ExitCode: 137, OOMKilled: true}, reason: "oom"},
		{name: "nonzero", state: containerState{Status: "exited", ExitCode: 2}, reason: "nonzero_exit"},
		{name: "clean", state: containerState{Status: "exited", ExitCode: 0}, reason: "clean_exit"},
		{name: "unknown", state: containerState{Status: "created"}, reason: "unknown"},
	} {
		worker.recordAppProcessExit(job, Container{State: test.state})
	}
	families, err := worker.Metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "stealth_apps_worker_runtime_process_exits_total" {
			continue
		}
		if len(family.Metric) != 4 {
			t.Fatalf("process exit metric has %d reasons, want four fixed reasons", len(family.Metric))
		}
		for _, metric := range family.Metric {
			if len(metric.Label) != 1 || metric.Label[0].GetName() != "reason" {
				t.Fatalf("process-exit labels are not low-cardinality: %#v", metric.Label)
			}
		}
		return
	}
	t.Fatal("process exit metric family was not registered")
}

func TestRuntimeRetryBackoffIsDeterministicAndCapped(t *testing.T) {
	previous := time.Duration(0)
	for attempt := -2; attempt < 30; attempt++ {
		delay := runtimeBackoff(attempt)
		if delay < time.Second || delay > maxRuntimeRetry || delay < previous {
			t.Fatalf("runtimeBackoff(%d) = %s after %s", attempt, delay, previous)
		}
		previous = delay
	}
	if previous != maxRuntimeRetry {
		t.Fatalf("runtimeBackoff did not reach its cap: %s", previous)
	}
}
