package appruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func newCacheDeploymentID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestParseRuntimeImageReferenceRequiresExactStealthDeploymentTag(t *testing.T) {
	deploymentID := newCacheDeploymentID(t)
	valid := ImageTag(deploymentID)
	if got, ok := ParseRuntimeImageReference(valid); !ok || got != deploymentID {
		t.Fatalf("ParseRuntimeImageReference(%q) = %s, %v", valid, got, ok)
	}
	for _, invalid := range []string{
		"other/" + deploymentID.String() + ":runtime",
		"stealth-app/not-a-uuid:runtime",
		"stealth-app/00000000-0000-0000-0000-000000000000:runtime",
		"stealth-app/" + deploymentID.String() + ":latest",
		"stealth-app/" + deploymentID.String() + ":runtime:extra",
		"stealth-app/extra/" + deploymentID.String() + ":runtime",
		"stealth-app/" + strings.ToUpper(deploymentID.String()) + ":runtime",
		ImageTag(uuid.MustParse("22222222-2222-4222-8222-222222222222")),
	} {
		if _, ok := ParseRuntimeImageReference(invalid); ok {
			t.Errorf("ParseRuntimeImageReference(%q) accepted an invalid ownership reference", invalid)
		}
	}
}

func TestParseRuntimeImageInspectDeduplicatesSharedImageAccounting(t *testing.T) {
	first := newCacheDeploymentID(t)
	second := newCacheDeploymentID(t)
	firstRef, secondRef := ImageTag(first), ImageTag(second)
	imageID := "sha256:" + strings.Repeat("a", 64)
	created := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	output := []byte(fmt.Sprintf("%s\t%s\t3145728\t%s,%s,postgres:17-alpine\n", imageID, created.Format(time.RFC3339Nano), firstRef, secondRef))
	entries, err := parseRuntimeImageInspect(output, []string{firstRef, secondRef})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ImageID != entries[1].ImageID {
		t.Fatalf("shared image tag entries = %#v", entries)
	}
	got, err := runtimeImageCacheSizeBytes(entries)
	if err != nil || got != 3<<20 {
		t.Fatalf("deduplicated cache size = %d, %v; want 3 MiB", got, err)
	}
}

func TestParseRuntimeImageInspectIncludesOtherValidatedStealthTagsOnImage(t *testing.T) {
	requestedID := newCacheDeploymentID(t)
	discoveredID := newCacheDeploymentID(t)
	requestedReference := ImageTag(requestedID)
	discoveredReference := ImageTag(discoveredID)
	imageID := "sha256:" + strings.Repeat("a", 64)
	created := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	output := []byte(fmt.Sprintf("%s\t%s\t3145728\t%s,%s,postgres:17-alpine\n", imageID, created.Format(time.RFC3339Nano), requestedReference, discoveredReference))
	entries, err := parseRuntimeImageInspect(output, []string{requestedReference})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Reference != requestedReference || entries[1].Reference != discoveredReference {
		t.Fatalf("runtime image tags discovered from inspect = %#v", entries)
	}
}

func TestParseRuntimeImageInspectRejectsMalformedOrIncompleteDockerRows(t *testing.T) {
	deploymentID := newCacheDeploymentID(t)
	reference := ImageTag(deploymentID)
	imageID := "sha256:" + strings.Repeat("a", 64)
	created := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for _, output := range [][]byte{
		[]byte("not-a-digest\t" + created + "\t1\t" + reference + "\n"),
		[]byte(imageID + "\tmalformed-time\t1\t" + reference + "\n"),
		[]byte(imageID + "\t" + created + "\t-1\t" + reference + "\n"),
		[]byte(imageID + "\t" + created + "\t1\t" + reference + "," + reference + "\n"),
		[]byte(imageID + "\t" + created + "\t1\tstealth-app/malformed:runtime\n"),
	} {
		if _, err := parseRuntimeImageInspect(output, []string{reference}); !errors.Is(err, ErrImageVerification) {
			t.Errorf("malformed Docker inspect row %q = %v, want verification error", output, err)
		}
	}
	missingReference := ImageTag(newCacheDeploymentID(t))
	if _, err := parseRuntimeImageInspect([]byte(imageID+"\t"+created+"\t1\t"+reference+"\n"), []string{reference, missingReference}); !errors.Is(err, ErrImageVerification) {
		t.Fatalf("incomplete inspect result = %v, want verification error", err)
	}
}

func TestMobyRuntimeImageCacheInventoryIsStrictBoundedAndTyped(t *testing.T) {
	deploymentID := newCacheDeploymentID(t)
	reference := ImageTag(deploymentID)
	imageID := "sha256:" + strings.Repeat("b", 64)
	created := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	runner := &scriptedRuntimeRunner{results: []CommandResult{
		{Stdout: []byte(reference + "\t" + imageID + "\n")},
		{Stdout: []byte(fmt.Sprintf("%s\t%s\t4096\t%s\n", imageID, created.Format(time.RFC3339Nano), reference))},
	}}
	moby, err := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := moby.ListRuntimeImageCache(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Reference != reference || entries[0].ImageID != imageID || entries[0].SizeBytes != 4096 {
		t.Fatalf("runtime image cache inventory = %#v, %v", entries, err)
	}
	if len(runner.calls) != 2 || runner.calls[0].args[0] != "image" || runner.calls[1].args[0] != "image" ||
		runner.calls[0].args[1] != "ls" || runner.calls[1].args[1] != "inspect" {
		t.Fatalf("inventory Docker argv = %#v", runner.calls)
	}
	if !strings.Contains(runner.calls[1].args[3], "%v") {
		t.Fatalf("image size inspect format is not type-neutral: %q", runner.calls[1].args[3])
	}
	for _, call := range runner.calls {
		if call.args[0] == "sh" || call.args[0] == "bash" {
			t.Fatalf("Docker inventory invoked a shell: %#v", call.args)
		}
	}
}

func TestMobyRuntimeImageCacheInventoryRejectsMalformedAndTruncatedData(t *testing.T) {
	for _, test := range []struct {
		name    string
		result  CommandResult
		wantErr error
	}{
		{name: "malformed reference", result: CommandResult{Stdout: []byte("stealth-app/not-a-deployment:runtime\n")}, wantErr: ErrImageVerification},
		{name: "truncated list", result: CommandResult{Stdout: []byte("stealth-app/"), StdoutTruncated: true}, wantErr: ErrDockerOutputTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRuntimeRunner{results: []CommandResult{test.result}}
			moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
			_, err := moby.ListRuntimeImageCache(context.Background())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ListRuntimeImageCache() = %v, want %v", err, test.wantErr)
			}
			if test.name == "malformed reference" && safeRuntimeError(err) != "runtime image tag ownership verification failed" {
				t.Fatalf("malformed reference diagnostic = %q", safeRuntimeError(err))
			}
			if len(runner.calls) != 1 {
				t.Fatalf("invalid list caused further Docker commands: %#v", runner.calls)
			}
		})
	}
	deploymentID := newCacheDeploymentID(t)
	reference := ImageTag(deploymentID)
	imageID := "sha256:" + strings.Repeat("c", 64)
	truncatedInspect := &scriptedRuntimeRunner{
		results: []CommandResult{{Stdout: []byte(reference + "\t" + imageID + "\n")}, {Stdout: []byte("partial"), StdoutTruncated: true}},
	}
	truncatedMoby, _ := NewMoby(truncatedInspect, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	_, err := truncatedMoby.ListRuntimeImageCache(context.Background())
	if !errors.Is(err, ErrDockerOutputTooLarge) {
		t.Fatalf("truncated inspect output = %v, want bounded-output error", err)
	}
	if safeRuntimeError(err) != "runtime image inspection exceeded bounds" {
		t.Fatalf("truncated inspect diagnostic = %q", safeRuntimeError(err))
	}
}

func runtimeImageCacheDockerFixtures(t *testing.T, tagCount, imageCount int) ([]byte, []CommandResult, []RuntimeImageCacheEntry) {
	t.Helper()
	if imageCount < 1 || imageCount > tagCount {
		t.Fatalf("invalid fixture counts: tags=%d images=%d", tagCount, imageCount)
	}
	created := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	tagsByImage := make(map[string][]string, imageCount)
	metadataByImage := make(map[string]RuntimeImageCacheEntry, imageCount)
	var listing strings.Builder
	entries := make([]RuntimeImageCacheEntry, 0, tagCount)
	for index := 0; index < tagCount; index++ {
		deploymentID := newCacheDeploymentID(t)
		reference := ImageTag(deploymentID)
		imageNumber := index % imageCount
		imageID := fmt.Sprintf("sha256:%064x", imageNumber+1)
		entry := RuntimeImageCacheEntry{
			DeploymentID: deploymentID,
			Reference:    reference,
			ImageID:      imageID,
			SizeBytes:    int64(imageNumber+1) * 1024,
			CreatedAt:    created.Add(time.Duration(imageNumber) * time.Second),
		}
		entries = append(entries, entry)
		tagsByImage[imageID] = append(tagsByImage[imageID], reference)
		metadataByImage[imageID] = entry
		listing.WriteString(reference)
		listing.WriteByte('\t')
		listing.WriteString(imageID)
		listing.WriteByte('\n')
	}
	imageIDs := make([]string, 0, imageCount)
	for imageID := range tagsByImage {
		imageIDs = append(imageIDs, imageID)
	}
	slices.Sort(imageIDs)
	results := make([]CommandResult, 0, (imageCount+runtimeImageInspectBatchSize-1)/runtimeImageInspectBatchSize)
	for start := 0; start < len(imageIDs); start += runtimeImageInspectBatchSize {
		end := min(start+runtimeImageInspectBatchSize, len(imageIDs))
		var inspect strings.Builder
		for _, imageID := range imageIDs[start:end] {
			entry := metadataByImage[imageID]
			tags := slices.Clone(tagsByImage[imageID])
			slices.Sort(tags)
			inspect.WriteString(fmt.Sprintf("%s\t%s\t%d\t%s\n", imageID, entry.CreatedAt.Format(time.RFC3339Nano), entry.SizeBytes, strings.Join(tags, ",")))
		}
		results = append(results, CommandResult{Stdout: []byte(inspect.String())})
	}
	return []byte(listing.String()), results, entries
}

func TestMobyRuntimeImageCacheInventoryAcceptsHighCardinalityAndBatchesInspect(t *testing.T) {
	for _, count := range []int{runtimeImageInspectBatchSize - 1, runtimeImageInspectBatchSize, runtimeImageInspectBatchSize + 1, 257, 512, 1000} {
		t.Run(fmt.Sprintf("tags_%d", count), func(t *testing.T) {
			listing, inspectResults, wantEntries := runtimeImageCacheDockerFixtures(t, count, count)
			results := append([]CommandResult{{Stdout: listing}}, inspectResults...)
			runner := &scriptedRuntimeRunner{results: results}
			moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
			entries, err := moby.ListRuntimeImageCache(context.Background())
			if err != nil {
				t.Fatalf("high-cardinality inventory failed: %v", err)
			}
			if len(entries) != count {
				t.Fatalf("inventory contains %d tags, want %d", len(entries), count)
			}
			sortRuntimeImageCacheEntries(wantEntries)
			if !slices.Equal(entries, wantEntries) {
				t.Fatal("inventory metadata or deterministic order differs from Docker fixtures")
			}
			wantInspectCalls := (count + runtimeImageInspectBatchSize - 1) / runtimeImageInspectBatchSize
			if len(runner.calls) != 1+wantInspectCalls {
				t.Fatalf("Docker calls = %d, want image list plus %d bounded inspections", len(runner.calls), wantInspectCalls)
			}
			for _, call := range runner.calls[1:] {
				if len(call.args)-4 > runtimeImageInspectBatchSize {
					t.Fatalf("image inspect argv has %d IDs, batch limit is %d", len(call.args)-4, runtimeImageInspectBatchSize)
				}
			}
		})
	}
}

func TestRuntimeImageCacheInventoryAndAccountingHandleManySharedTags(t *testing.T) {
	listing, inspectResults, wantEntries := runtimeImageCacheDockerFixtures(t, 1000, 100)
	results := append([]CommandResult{{Stdout: listing}}, inspectResults...)
	runner := &scriptedRuntimeRunner{results: results}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	actual, err := moby.ListRuntimeImageCache(context.Background())
	if err != nil || len(actual) != 1000 {
		t.Fatalf("shared-tag cache inventory = %d entries, %v", len(actual), err)
	}
	wantBytes, err := runtimeImageCacheSizeBytes(wantEntries)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := runtimeImageCacheSizeBytes(actual)
	if err != nil || gotBytes != wantBytes {
		t.Fatalf("shared image cache estimate = %d, %v; want %d", gotBytes, err, wantBytes)
	}
	sharedImageID := actual[0].ImageID
	remaining := slices.Clone(actual)
	for index, entry := range remaining {
		if entry.ImageID == sharedImageID {
			remaining = append(remaining[:index], remaining[index+1:]...)
			break
		}
	}
	afterOneAlias, err := runtimeImageCacheSizeBytes(remaining)
	if err != nil || afterOneAlias != wantBytes {
		t.Fatalf("removing one of several shared tags changed accounting: %d, %v; want %d", afterOneAlias, err, wantBytes)
	}
}

func TestMobyRuntimeImageCacheRejectsMalformedOwnershipInLargeInventory(t *testing.T) {
	listing, _, _ := runtimeImageCacheDockerFixtures(t, 999, 999)
	listing = append(listing, []byte("stealth-app/not-a-uuid:runtime\tsha256:"+strings.Repeat("f", 64)+"\n")...)
	runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: listing}}}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if _, err := moby.ListRuntimeImageCache(context.Background()); !errors.Is(err, ErrImageVerification) {
		t.Fatalf("large inventory with malformed Stealth reference = %v, want verification failure", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("malformed large inventory reached image inspect: %d Docker calls", len(runner.calls))
	}
}

func TestMobyRuntimeImageCacheInspectionFailureHasBoundedSafeReason(t *testing.T) {
	deploymentID := newCacheDeploymentID(t)
	reference := ImageTag(deploymentID)
	imageID := "sha256:" + strings.Repeat("a", 64)
	runner := &scriptedRuntimeRunner{results: []CommandResult{
		{Stdout: []byte(reference + "\t" + imageID + "\n")},
		{Stdout: []byte(imageID + "\tnot-a-time\t4096\t" + reference + "\n")},
	}}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	_, err := moby.ListRuntimeImageCache(context.Background())
	if !errors.Is(err, ErrImageVerification) {
		t.Fatalf("malformed image metadata error = %v, want image verification failure", err)
	}
	if got := safeRuntimeError(err); got != "runtime image creation time malformed" {
		t.Fatalf("malformed image metadata diagnostic = %q", got)
	}
}

func TestMobyRuntimeImageCacheRejectsUnavailableImageSize(t *testing.T) {
	deploymentID := newCacheDeploymentID(t)
	reference := ImageTag(deploymentID)
	imageID := "sha256:" + strings.Repeat("a", 64)
	runner := &scriptedRuntimeRunner{results: []CommandResult{
		{Stdout: []byte(reference + "\t" + imageID + "\n")},
		{Stdout: []byte(imageID + "\t2026-09-26T08:00:00Z\t-1\t" + reference + "\n")},
	}}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	_, err := moby.ListRuntimeImageCache(context.Background())
	if !errors.Is(err, ErrImageVerification) {
		t.Fatalf("unavailable image size error = %v, want image verification failure", err)
	}
	if got := safeRuntimeError(err); got != "runtime image size is unavailable" {
		t.Fatalf("unavailable image size diagnostic = %q", got)
	}
}

func TestMobyRuntimeImageRemovalUsesExactValidatedTagAndTreatsAbsenceAsSuccess(t *testing.T) {
	deploymentID := newCacheDeploymentID(t)
	entry := RuntimeImageCacheEntry{
		DeploymentID: deploymentID,
		Reference:    ImageTag(deploymentID),
		ImageID:      "sha256:" + strings.Repeat("c", 64),
		SizeBytes:    1024,
		CreatedAt:    time.Now().UTC(),
	}
	runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: []byte(entry.ImageID + "\t" + entry.Reference + "\n")}, {}}}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err := moby.RemoveRuntimeImageTag(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 || runner.calls[1].args[0] != "image" || runner.calls[1].args[1] != "rm" || runner.calls[1].args[2] != entry.Reference {
		t.Fatalf("image removal argv = %#v", runner.calls)
	}
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "prune") || call.args[0] == "sh" || call.args[0] == "bash" {
			t.Fatalf("unsafe Docker cleanup command: %#v", call.args)
		}
	}

	missingRunner := &scriptedRuntimeRunner{errors: []error{&CommandFailure{ExitCode: 1, Stderr: "Error: No such image: " + entry.Reference}}}
	missingMoby, _ := NewMoby(missingRunner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err := missingMoby.RemoveRuntimeImageTag(context.Background(), entry); err != nil {
		t.Fatalf("already absent validated image tag should converge: %v", err)
	}
	if len(missingRunner.calls) != 1 {
		t.Fatalf("already absent image tag issued extra commands: %#v", missingRunner.calls)
	}

	removeFailure := errors.New("docker image remove failed")
	failingRunner := &scriptedRuntimeRunner{
		results: []CommandResult{{Stdout: []byte(entry.ImageID + "\t" + entry.Reference + "\n")}},
		errors:  []error{nil, removeFailure},
	}
	failingMoby, _ := NewMoby(failingRunner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err := failingMoby.RemoveRuntimeImageTag(context.Background(), entry); !errors.Is(err, removeFailure) {
		t.Fatalf("Docker image removal failure = %v, want propagated maintenance error", err)
	}
	if len(failingRunner.calls) != 2 || failingRunner.calls[1].args[0] != "image" || failingRunner.calls[1].args[1] != "rm" || failingRunner.calls[1].args[2] != entry.Reference {
		t.Fatalf("failed removal did not target one exact tag: %#v", failingRunner.calls)
	}

	entry.Reference = "operator/image:latest"
	if err := moby.RemoveRuntimeImageTag(context.Background(), entry); !errors.Is(err, ErrImageVerification) {
		t.Fatalf("unowned image tag removal = %v, want ownership rejection", err)
	}
}

func TestMobyManagedAppImageReferenceInventoryIsBoundedAndOwnershipChecked(t *testing.T) {
	job := runtimeTestJob()
	deploymentID := newCacheDeploymentID(t)
	selectedDeploymentID := deploymentID.String()
	job.App.DesiredDeploymentID = &selectedDeploymentID
	labels, err := ContainerLabels(job)
	if err != nil {
		t.Fatal(err)
	}
	containerID := strings.Repeat("1", 64)
	imageID := "sha256:" + strings.Repeat("d", 64)
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		t.Fatal(err)
	}
	output := fmt.Sprintf("%s\t%s\t/%s\t%s\n", containerID, imageID, job.ContainerName, labelsJSON)
	runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: []byte(containerID + "\n")}, {Stdout: []byte(output)}}}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	refs, err := moby.ListManagedAppImageReferences(context.Background())
	if err != nil || len(refs) != 1 || refs[0] != (ManagedAppImageReference{DeploymentID: deploymentID, ImageID: imageID}) {
		t.Fatalf("verified container references = %#v, %v", refs, err)
	}
	if len(runner.calls) != 2 || runner.calls[1].args[0] != "container" || runner.calls[1].args[1] != "inspect" {
		t.Fatalf("container protection Docker argv = %#v", runner.calls)
	}
	if strings.Contains(strings.Join(runner.calls[1].args, " "), ".Config.Env") {
		t.Fatalf("container image protection inspect unnecessarily includes App environment: %#v", runner.calls[1].args)
	}

	labels["stealth.project_id"] = "not-a-uuid"
	labelsJSON, _ = json.Marshal(labels)
	badRunner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: []byte(containerID + "\n")}, {Stdout: []byte(fmt.Sprintf("%s\t%s\t/%s\t%s\n", containerID, imageID, job.ContainerName, labelsJSON))}}}
	badMoby, _ := NewMoby(badRunner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if _, err := badMoby.ListManagedAppImageReferences(context.Background()); !errors.Is(err, ErrRuntimeOwnershipConflict) {
		t.Fatalf("malformed managed labels inventory = %v, want fail-closed ownership conflict", err)
	}

}

type managedContainerDockerFixture struct {
	id     string
	output string
	ref    ManagedAppImageReference
}

func managedContainerDockerFixtures(t *testing.T, count, malformedIndex int) ([]byte, []CommandResult, []ManagedAppImageReference) {
	t.Helper()
	fixtures := make([]managedContainerDockerFixture, 0, count)
	for index := 0; index < count; index++ {
		job := runtimeTestJob()
		appID, projectID, deploymentID, routeID := newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t), newCacheDeploymentID(t)
		job.App.ID = appID.String()
		job.App.ProjectID = projectID.String()
		deploymentString := deploymentID.String()
		job.App.DesiredDeploymentID = &deploymentString
		job.App.DesiredGeneration = int64(index + 1)
		job.RouteIdentity = routeID
		job.ContainerName = repository.AppRuntimeContainerNameForIncarnation(appID, routeID)
		labels, err := ContainerLabels(job)
		if err != nil {
			t.Fatal(err)
		}
		if index == malformedIndex {
			labels["stealth.project_id"] = "not-a-uuid"
		}
		encodedLabels, err := json.Marshal(labels)
		if err != nil {
			t.Fatal(err)
		}
		containerID := fmt.Sprintf("%064x", index+1)
		imageID := fmt.Sprintf("sha256:%064x", index+1)
		line := fmt.Sprintf("%s\t%s\t/%s\t%s\n", containerID, imageID, job.ContainerName, encodedLabels)
		fixtures = append(fixtures, managedContainerDockerFixture{
			id: containerID, output: line,
			ref: ManagedAppImageReference{DeploymentID: deploymentID, ImageID: imageID},
		})
	}
	slices.SortFunc(fixtures, func(left, right managedContainerDockerFixture) int { return strings.Compare(left.id, right.id) })
	var listing strings.Builder
	byID := make(map[string]managedContainerDockerFixture, count)
	refs := make([]ManagedAppImageReference, 0, count)
	for _, fixture := range fixtures {
		listing.WriteString(fixture.id)
		listing.WriteByte('\n')
		byID[fixture.id] = fixture
		refs = append(refs, fixture.ref)
	}
	results := make([]CommandResult, 0, (count+managedContainerInspectBatchSize-1)/managedContainerInspectBatchSize)
	ids := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		ids = append(ids, fixture.id)
	}
	for start := 0; start < len(ids); start += managedContainerInspectBatchSize {
		end := min(start+managedContainerInspectBatchSize, len(ids))
		var output strings.Builder
		for _, id := range ids[start:end] {
			output.WriteString(byID[id].output)
		}
		results = append(results, CommandResult{Stdout: []byte(output.String())})
	}
	return []byte(listing.String()), results, refs
}

func TestMobyManagedAppImageReferencesInventoryBatchesAndAcceptsLargeHosts(t *testing.T) {
	for _, count := range []int{managedContainerInspectBatchSize - 1, managedContainerInspectBatchSize, managedContainerInspectBatchSize + 1, 2*managedContainerInspectBatchSize + 1, 600} {
		t.Run(fmt.Sprintf("containers_%d", count), func(t *testing.T) {
			listing, inspectResults, wantRefs := managedContainerDockerFixtures(t, count, -1)
			results := append([]CommandResult{{Stdout: listing}}, inspectResults...)
			runner := &scriptedRuntimeRunner{results: results}
			moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
			refs, err := moby.ListManagedAppImageReferences(context.Background())
			if err != nil {
				t.Fatalf("managed container inventory of %d failed: %v", count, err)
			}
			sortManagedAppImageReferences(wantRefs)
			if !slices.Equal(refs, wantRefs) {
				t.Fatalf("verified refs = %d, want %d complete ownership records", len(refs), len(wantRefs))
			}
			wantCalls := 1 + (count+managedContainerInspectBatchSize-1)/managedContainerInspectBatchSize
			if len(runner.calls) != wantCalls {
				t.Fatalf("Docker calls = %d, want %d bounded inventory calls", len(runner.calls), wantCalls)
			}
			for _, call := range runner.calls[1:] {
				if len(call.args)-4 > managedContainerInspectBatchSize {
					t.Fatalf("container inspect argv has %d IDs, batch limit is %d", len(call.args)-4, managedContainerInspectBatchSize)
				}
			}
		})
	}
}

func TestMobyManagedAppImageReferencesFailsClosedOnMalformedLaterBatch(t *testing.T) {
	listing, inspectResults, _ := managedContainerDockerFixtures(t, 2*managedContainerInspectBatchSize+1, 2*managedContainerInspectBatchSize)
	results := append([]CommandResult{{Stdout: listing}}, inspectResults...)
	runner := &scriptedRuntimeRunner{results: results}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if _, err := moby.ListManagedAppImageReferences(context.Background()); !errors.Is(err, ErrRuntimeOwnershipConflict) {
		t.Fatalf("malformed later-batch ownership = %v, want ownership conflict", err)
	}
	wantCalls := 1 + (2*managedContainerInspectBatchSize+1+managedContainerInspectBatchSize-1)/managedContainerInspectBatchSize
	if len(runner.calls) != wantCalls {
		t.Fatalf("malformed later batch caused %d calls, want stop after %d", len(runner.calls), wantCalls)
	}
	for _, call := range runner.calls {
		if slices.Contains(call.args, "rm") || strings.Contains(strings.Join(call.args, " "), "prune") {
			t.Fatalf("failed ownership inventory issued a destructive Docker command: %#v", call.args)
		}
	}
}
