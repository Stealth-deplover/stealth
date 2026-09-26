package appruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
		{Stdout: []byte(reference + "\n")},
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
	truncatedInspect := &scriptedRuntimeRunner{
		results: []CommandResult{{Stdout: []byte(reference + "\n")}, {Stdout: []byte("partial"), StdoutTruncated: true}},
	}
	truncatedMoby, _ := NewMoby(truncatedInspect, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	_, err := truncatedMoby.ListRuntimeImageCache(context.Background())
	if !errors.Is(err, ErrDockerOutputTooLarge) {
		t.Fatalf("truncated inspect output = %v, want bounded-output error", err)
	}
	if safeRuntimeError(err) != "runtime image inspection exceeded bounds" {
		t.Fatalf("truncated inspect diagnostic = %q", safeRuntimeError(err))
	}

	ids := make([]string, maxRuntimeImageCacheEntries+1)
	for index := range ids {
		ids[index] = ImageTag(newCacheDeploymentID(t))
	}
	runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: []byte(strings.Join(ids, "\n") + "\n")}}}
	moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if _, err := moby.ListRuntimeImageCache(context.Background()); !errors.Is(err, ErrDockerOutputTooLarge) {
		t.Fatalf("oversized inventory = %v, want bounded-output error", err)
	}
}

func TestMobyRuntimeImageCacheInspectionFailureHasBoundedSafeReason(t *testing.T) {
	deploymentID := newCacheDeploymentID(t)
	reference := ImageTag(deploymentID)
	imageID := "sha256:" + strings.Repeat("a", 64)
	runner := &scriptedRuntimeRunner{results: []CommandResult{
		{Stdout: []byte(reference + "\n")},
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

	ids := make([]string, maxRuntimeImageProtectionContainers+1)
	for index := range ids {
		ids[index] = fmt.Sprintf("%064x", index+1)
	}
	boundedRunner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: []byte(strings.Join(ids, "\n") + "\n")}}}
	boundedMoby, _ := NewMoby(boundedRunner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if _, err := boundedMoby.ListManagedAppImageReferences(context.Background()); !errors.Is(err, ErrDockerOutputTooLarge) || len(boundedRunner.calls) != 1 {
		t.Fatalf("oversized managed container inventory = %v, calls=%d", err, len(boundedRunner.calls))
	}
}
