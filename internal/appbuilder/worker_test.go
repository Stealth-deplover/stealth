package appbuilder

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
	"github.com/Stealth-deplover/stealth/internal/appstore"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionrunner"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

type fakePersistence struct {
	job           repository.AppBuildJob
	claimed       int
	failed        string
	deferred      string
	completed     bool
	completeErr   error
	completeTried bool
	digest        string
	imagePath     string
	imageChecksum string
	imageSize     int64
	reserved      int64
	logs          []string
	claimTokens   []string
}

func (p *fakePersistence) RequeueStaleAppDeployments(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (p *fakePersistence) ClaimNextAppDeployment(_ context.Context, workerID string) (repository.AppBuildJob, error) {
	p.claimed++
	p.claimTokens = append(p.claimTokens, workerID)
	if p.job.Deployment.ID == "" {
		return repository.AppBuildJob{}, repository.ErrNoAppDeploymentJob
	}
	p.job.WorkerID = workerID
	return p.job, nil
}

func (p *fakePersistence) DeferAppDeploymentBuild(_ context.Context, _, _, _ uuid.UUID, _ string, message string) error {
	p.deferred = message
	return nil
}

func (p *fakePersistence) ReserveAppImagePublish(_ context.Context, _, _, _ uuid.UUID, _ string, size int64, _ repository.ArtifactCleanupInput) error {
	p.reserved = size
	return nil
}

func (p *fakePersistence) CompleteAppDeploymentBuildWithCleanup(_ context.Context, _, _, _ uuid.UUID, _ string, digest, path, checksum string, size int64, _ repository.ArtifactCleanupInput) (domain.AppDeployment, error) {
	p.completeTried = true
	if p.completeErr != nil {
		p.imagePath, p.digest, p.imageChecksum, p.imageSize = path, digest, checksum, size
		return domain.AppDeployment{}, p.completeErr
	}
	p.completed, p.digest, p.imagePath, p.imageChecksum, p.imageSize = true, digest, path, checksum, size
	p.logs = append(p.logs, "Build completed; verified OCI artifact persisted")
	return domain.AppDeployment{Status: "ready", BuildStatus: "succeeded", ImageDigest: &digest}, nil
}

func (p *fakePersistence) FailAppDeploymentBuild(_ context.Context, _, _, _ uuid.UUID, _ string, message string) (domain.AppDeployment, error) {
	p.failed = message
	p.logs = append(p.logs, message)
	return domain.AppDeployment{Status: "failed", BuildStatus: "failed"}, nil
}

func (p *fakePersistence) AppendAppBuildLog(_ context.Context, _, _, _ uuid.UUID, _ string, _ uuid.UUID, _, message string) (domain.AppBuildLog, error) {
	p.logs = append(p.logs, message)
	return domain.AppBuildLog{}, nil
}

type fakeCommandRunner struct {
	mode           string
	args           []string
	env            []string
	readinessCalls int
}

func (r *fakeCommandRunner) Run(ctx context.Context, program string, args, env []string, stdout, _ io.Writer) error {
	r.args = append([]string(nil), args...)
	r.env = append([]string(nil), env...)
	if contains(args, "debug") {
		r.readinessCalls++
		if r.mode == "unavailable" || r.mode == "unavailable_after_ready" && r.readinessCalls > 1 {
			return errors.New("connection refused")
		}
		return nil
	}
	if r.mode == "unavailable_after_ready" {
		return errors.New("connection refused")
	}
	if r.mode == "dockerfile_failure" {
		return errors.New("buildctl exited with status 1")
	}
	if r.mode == "timeout" {
		<-ctx.Done()
		return ctx.Err()
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, "#1 exporting OCI image\n")
	}
	outputPath := argumentValue(args, "--output", "type=oci,dest=")
	metadataPath := argumentAfter(args, "--metadata-file")
	digest, archive, archiveErr := buildTestOCIArchive()
	if archiveErr != nil {
		return archiveErr
	}
	if r.mode == "invalid_oci" {
		archive = []byte("invalid tar")
	} else if r.mode == "too_large" || r.mode == "too_large_and_wait" {
		archive = bytes.Repeat([]byte("x"), (1<<20)+1)
	}
	if err := os.WriteFile(outputPath, archive, 0o600); err != nil {
		return err
	}
	if r.mode == "too_large_and_wait" {
		<-ctx.Done()
		return ctx.Err()
	}
	if r.mode == "malformed_metadata" {
		return os.WriteFile(metadataPath, []byte("{"), 0o600)
	}
	if r.mode == "missing_digest" {
		return os.WriteFile(metadataPath, []byte(`{"containerimage.config.digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), 0o600)
	}
	metadata, _ := json.Marshal(map[string]string{"containerimage.digest": digest})
	return os.WriteFile(metadataPath, metadata, 0o600)
}

func TestCleanStaleStagingRemovesOnlyOldUUIDDirectories(t *testing.T) {
	root := t.TempDir()
	oldID := uuid.Must(uuid.NewV7())
	oldPath := filepath.Join(root, oldID.String())
	if err := os.Mkdir(oldPath, 0o700); err != nil {
		t.Fatal(err)
	}
	recentID := uuid.Must(uuid.NewV7())
	recentPath := filepath.Join(root, recentID.String())
	if err := os.Mkdir(recentPath, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(oldPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	unknownPath := filepath.Join(root, "operator-data")
	if err := os.Mkdir(unknownPath, 0o700); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{StagingRoot: root}
	removed, err := worker.cleanStaleStaging(time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("cleanStaleStaging() = %d, %v", removed, err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old staging directory remains: %v", err)
	}
	for _, path := range []string{recentPath, unknownPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("staging cleanup removed or changed %s: %v", path, err)
		}
	}
}

func TestWorkerPublishesVerifiedOCIAndKeepsBuildInputsPrivate(t *testing.T) {
	worker, persistence, runner, appArtifacts, projectID, appID, deploymentID := newTestWorker(t, "success")
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce = %v, %v", processed, err)
	}
	if !persistence.completed || persistence.failed != "" || persistence.deferred != "" {
		t.Fatalf("build lifecycle = completed %v failed %q deferred %q", persistence.completed, persistence.failed, persistence.deferred)
	}
	if len(persistence.claimTokens) != 1 || !safeWorkerID(persistence.claimTokens[0]) || persistence.claimTokens[0] == "worker-1" {
		t.Fatalf("App build did not use a unique per-claim lease token: %#v", persistence.claimTokens)
	}
	if len(persistence.digest) != 71 || !strings.HasPrefix(persistence.digest, "sha256:") || len(persistence.imageChecksum) != 64 || persistence.imageSize <= 0 || persistence.reserved != persistence.imageSize {
		t.Fatalf("verified output metadata = %#v", persistence)
	}
	image, err := appArtifacts.Images.OpenRelative(context.Background(), persistence.imagePath)
	if err != nil {
		t.Fatalf("completed OCI artifact is missing: %v", err)
	}
	_ = image.Close()
	if projectID.String()+"/"+appID.String()+"/"+deploymentID.String() != persistence.job.SourcePath {
		t.Fatalf("source artifact path is not UUID-derived: %s", persistence.job.SourcePath)
	}
	for _, arg := range runner.args {
		if arg == "--allow" || arg == "--secret" || arg == "--ssh" || strings.HasPrefix(arg, "build-arg:") || strings.Contains(arg, "gateway.v0") || strings.Contains(arg, "docker build") {
			t.Fatalf("unsafe build argument was passed: %q", arg)
		}
	}
	if len(runner.env) != 3 || strings.Contains(strings.Join(runner.env, " "), "DATABASE_URL") {
		t.Fatalf("buildctl inherited a non-minimal environment: %#v", runner.env)
	}
	for _, message := range persistence.logs {
		if strings.Contains(message, worker.StagingRoot) {
			t.Fatalf("build progress exposed worker path: %q", message)
		}
	}
	if countLogMessage(persistence.logs, "Build completed; verified OCI artifact persisted") != 1 {
		t.Fatalf("completion log should be recorded by the durable completion transaction: %#v", persistence.logs)
	}
}

func TestWorkerReadinessFailureDoesNotClaimQueuedDeployment(t *testing.T) {
	worker, persistence, _, _, _, _, _ := newTestWorker(t, "unavailable")
	processed, err := worker.RunOnce(context.Background())
	if err != nil || processed || persistence.claimed != 0 {
		t.Fatalf("RunOnce = %v, %v; claims = %d", processed, err, persistence.claimed)
	}
}

func TestWorkerRechecksReadinessAndDefersWhenBuildKitDropsAfterClaim(t *testing.T) {
	worker, persistence, _, _, _, _, _ := newTestWorker(t, "unavailable_after_ready")
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed || persistence.claimed != 1 || persistence.deferred == "" || persistence.failed != "" || persistence.completed {
		t.Fatalf("BuildKit loss after claim was not deferred: processed=%v err=%v persistence=%+v", processed, err, persistence)
	}
}

func TestWorkerDoesNotDeletePublishedArtifactAfterAmbiguousCompletion(t *testing.T) {
	worker, persistence, _, artifacts, _, _, _ := newTestWorker(t, "success")
	persistence.completeErr = errors.New("database completion response was lost")
	processed, err := worker.RunOnce(context.Background())
	if err == nil || !processed || !persistence.completeTried || persistence.failed != "" {
		t.Fatalf("ambiguous completion did not stop without terminal failure: processed=%v err=%v persistence=%+v", processed, err, persistence)
	}
	artifact, openErr := artifacts.Images.OpenRelative(context.Background(), persistence.imagePath)
	if openErr != nil {
		t.Fatalf("artifact was deleted after ambiguous completion: %v", openErr)
	}
	_ = artifact.Close()
}

func TestWorkerClassifiesSourceMismatchTimeoutAndInvalidOCI(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		badChecksum bool
		timeout     time.Duration
		wantFailure string
	}{
		{name: "source checksum", mode: "success", badChecksum: true, wantFailure: "source checksum mismatch"},
		{name: "timeout", mode: "timeout", timeout: 15 * time.Millisecond, wantFailure: "BuildKit timeout"},
		{name: "nonzero Dockerfile build", mode: "dockerfile_failure", wantFailure: "Dockerfile build failed"},
		{name: "malformed metadata", mode: "malformed_metadata", wantFailure: "BuildKit metadata invalid"},
		{name: "missing metadata digest", mode: "missing_digest", wantFailure: "BuildKit metadata invalid"},
		{name: "invalid OCI", mode: "invalid_oci", wantFailure: "OCI export invalid"},
		{name: "artifact too large", mode: "too_large", wantFailure: "artifact too large"},
		{name: "oversized output cancels BuildKit promptly", mode: "too_large_and_wait", wantFailure: "artifact too large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worker, persistence, _, _, _, _, _ := newTestWorker(t, test.mode)
			if test.badChecksum {
				persistence.job.Deployment.SourceChecksumSHA256 = strings.Repeat("0", 64)
			}
			if test.timeout > 0 {
				worker.BuildTimeout = test.timeout
			}
			processed, err := worker.RunOnce(context.Background())
			if err != nil || !processed || persistence.failed != test.wantFailure || persistence.completed {
				t.Fatalf("RunOnce = %v, %v; failed=%q completed=%v", processed, err, persistence.failed, persistence.completed)
			}
			if countLogMessage(persistence.logs, test.wantFailure) != 1 {
				t.Fatalf("failure log should be recorded by the durable failure transaction: %#v", persistence.logs)
			}
		})
	}
}

func countLogMessage(messages []string, wanted string) int {
	count := 0
	for _, message := range messages {
		if message == wanted {
			count++
		}
	}
	return count
}

func newTestWorker(t *testing.T, mode string) (*Worker, *fakePersistence, *fakeCommandRunner, *appstore.Store, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	projectID := uuid.Must(uuid.NewV7())
	appID := uuid.Must(uuid.NewV7())
	deploymentID := uuid.Must(uuid.NewV7())
	artifacts, err := appstore.New(filepath.Join(t.TempDir(), "artifacts"), 1<<20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	source := zipSource(t)
	prepared, err := artifacts.Sources.BeginUpload(context.Background(), projectID, appID, deploymentID, bytes.NewReader(source), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := artifacts.Sources.Commit(context.Background(), &prepared); err != nil {
		t.Fatal(err)
	}
	platform, err := appbuildspec.CurrentHostPlatform()
	if err != nil {
		t.Fatal(err)
	}
	sourceName := "source.zip"
	job := repository.AppBuildJob{
		App: domain.App{ID: appID.String(), ProjectID: projectID.String(), RuntimeStatus: "not_deployed"},
		Deployment: domain.AppDeployment{
			ID: deploymentID.String(), AppID: appID.String(), ProjectID: projectID.String(), Version: 1,
			Source: "upload", SourceName: &sourceName, SourceSizeBytes: prepared.Size,
			SourceChecksumSHA256: prepared.Checksum, DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: platform,
			Status: "queued", BuildStatus: "queued", WorkloadSnapshot: workloadspec.Default(), WorkloadSpecSHA256: strings.Repeat("a", 64),
		},
		SourcePath: prepared.RelativePath,
	}
	persistence := &fakePersistence{job: job}
	runner := &fakeCommandRunner{mode: mode}
	client := &BuildKitClient{Address: "tcp://buildkit:1234", Runner: runner}
	staging := filepath.Join(t.TempDir(), "staging")
	worker, err := New(persistence, artifacts, client, "worker-1", staging, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	worker.ArchiveLimit = functionrunner.ArchiveLimits{MaxBytes: 1 << 20, MaxFiles: 10, MaxEntry: 1 << 20}
	worker.BuildTimeout = time.Second
	return worker, persistence, runner, artifacts, projectID, appID, deploymentID
}

func zipSource(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	file, err := writer.Create("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "FROM scratch\nCOPY payload.txt /payload.txt\n"); err != nil {
		t.Fatal(err)
	}
	file, err = writer.Create("payload.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(file, "app payload")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func buildTestOCIArchive() (string, []byte, error) {
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	configDigest := sha256Hex(config)
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json", "digest": configDigest, "size": len(config),
		},
		"layers": []any{},
	})
	if err != nil {
		return "", nil, err
	}
	manifestDigest := sha256Hex(manifest)
	index, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"manifests": []any{map[string]any{
			"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": manifestDigest, "size": len(manifest),
		}},
	})
	if err != nil {
		return "", nil, err
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for name, data := range map[string][]byte{
		"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`),
		"index.json": index,
		"blobs/sha256/" + strings.TrimPrefix(configDigest, "sha256:"):   config,
		"blobs/sha256/" + strings.TrimPrefix(manifestDigest, "sha256:"): manifest,
	} {
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
			return "", nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return "", nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return "", nil, err
	}
	return manifestDigest, archive.Bytes(), nil
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func argumentAfter(args []string, flag string) string {
	for index, value := range args {
		if value == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func argumentValue(args []string, flag, prefix string) string {
	return strings.TrimPrefix(argumentAfter(args, flag), prefix)
}

func contains(args []string, expected string) bool {
	for _, value := range args {
		if value == expected {
			return true
		}
	}
	return false
}
