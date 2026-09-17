package artifactcleanup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

type fakePersistence struct {
	job         *repository.ArtifactCleanupJob
	claimed     *repository.ArtifactCleanupJob
	claimCalls  int
	requeueCall int
	completed   []uuid.UUID
	retries     []retryRecord
	failures    []string
}

type retryRecord struct {
	id        uuid.UUID
	retryAt   time.Time
	lastError string
}

func (f *fakePersistence) ClaimNextArtifactCleanup(context.Context, string, time.Duration) (repository.ArtifactCleanupJob, error) {
	f.claimCalls++
	if f.job == nil {
		return repository.ArtifactCleanupJob{}, repository.ErrNoArtifactCleanup
	}
	job := *f.job
	f.claimed = &job
	f.job = nil
	return job, nil
}

func (f *fakePersistence) RequeueStaleArtifactCleanup(context.Context, time.Duration) (int64, error) {
	f.requeueCall++
	return 0, nil
}

func (f *fakePersistence) CompleteArtifactCleanup(_ context.Context, id uuid.UUID, _ string) error {
	f.completed = append(f.completed, id)
	return nil
}

func (f *fakePersistence) RetryArtifactCleanup(_ context.Context, id uuid.UUID, _ string, retryAt time.Time, lastError string) error {
	f.retries = append(f.retries, retryRecord{id: id, retryAt: retryAt, lastError: lastError})
	return nil
}

func (f *fakePersistence) FailArtifactCleanup(_ context.Context, _ uuid.UUID, _ string, lastError string) error {
	f.failures = append(f.failures, lastError)
	return nil
}

type fakeCleaner struct {
	relativeCalls []string
	projectCalls  []uuid.UUID
	err           error
}

func (f *fakeCleaner) RemoveRelative(path string) error {
	f.relativeCalls = append(f.relativeCalls, path)
	return f.err
}

func (f *fakeCleaner) RemoveProject(projectID uuid.UUID) error {
	f.projectCalls = append(f.projectCalls, projectID)
	return f.err
}

func testCleanupJob(t *testing.T, operation repository.ArtifactCleanupOperation) repository.ArtifactCleanupJob {
	t.Helper()
	projectID := uuid.Must(uuid.NewV7())
	path := projectID.String()
	if operation == repository.ArtifactCleanupRelative {
		path = strings.Join([]string{projectID.String(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()}, "/")
	}
	return repository.ArtifactCleanupJob{
		ID:           uuid.Must(uuid.NewV7()),
		ProjectID:    projectID,
		StoreKind:    repository.ArtifactCleanupStorage,
		Operation:    operation,
		RelativePath: path,
		Attempts:     1,
	}
}

func testWorker(t *testing.T, persistence *fakePersistence, cleaners Stores) *Worker {
	t.Helper()
	worker, err := New(persistence, cleaners, "cleanup-test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestWorkerCompletesRelativeCleanup(t *testing.T) {
	persistence := &fakePersistence{job: ptr(testCleanupJob(t, repository.ArtifactCleanupRelative))}
	cleaner := &fakeCleaner{}
	worker := testWorker(t, persistence, Stores{Storage: cleaner})

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = processed=%t err=%v", processed, err)
	}
	if len(cleaner.relativeCalls) != 1 || cleaner.relativeCalls[0] != persistence.claimed.RelativePath {
		t.Fatalf("relative cleanup calls = %#v", cleaner.relativeCalls)
	}
	if len(persistence.completed) != 1 || persistence.completed[0] != persistence.claimed.ID {
		t.Fatalf("completed jobs = %#v", persistence.completed)
	}
}

func TestWorkerTreatsMissingLocalArtifactAsSuccessfulCleanup(t *testing.T) {
	persistence := &fakePersistence{job: ptr(testCleanupJob(t, repository.ArtifactCleanupRelative))}
	store, err := storage.New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	worker := testWorker(t, persistence, Stores{Storage: store})

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed || len(persistence.completed) != 1 {
		t.Fatalf("missing artifact cleanup = processed=%t err=%v completed=%d", processed, err, len(persistence.completed))
	}
}

func TestWorkerSchedulesPhysicalFailureForDurableRetry(t *testing.T) {
	persistence := &fakePersistence{job: ptr(testCleanupJob(t, repository.ArtifactCleanupRelative))}
	cleaner := &fakeCleaner{err: errors.New("permission denied")}
	worker := testWorker(t, persistence, Stores{Storage: cleaner})

	processed, err := worker.RunOnce(context.Background())
	if !processed || err == nil {
		t.Fatalf("RunOnce() = processed=%t err=%v, want persisted failure", processed, err)
	}
	if len(persistence.retries) != 1 || !persistence.retries[0].retryAt.After(time.Now()) {
		t.Fatalf("retry records = %#v", persistence.retries)
	}
	if len(persistence.completed) != 0 || strings.Contains(persistence.retries[0].lastError, "secret") {
		t.Fatalf("unsafe or completed retry record = %#v", persistence.retries)
	}
}

func TestWorkerRequeuesStaleJobsDuringRestartableRun(t *testing.T) {
	persistence := &fakePersistence{}
	worker := testWorker(t, persistence, Stores{})
	worker.PollInterval = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if persistence.requeueCall == 0 {
		t.Fatal("worker did not invoke stale lease recovery")
	}
}

func TestArtifactCleanupValidationRejectsPathEscape(t *testing.T) {
	projectID := uuid.Must(uuid.NewV7())
	validPath := strings.Join([]string{projectID.String(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()}, "/")
	for _, path := range []string{"../outside", "/absolute", strings.Replace(validPath, "/", "//", 1), validPath + "/.."} {
		if err := repository.ValidateArtifactCleanupInput(repository.ArtifactCleanupInput{
			ProjectID: projectID, StoreKind: repository.ArtifactCleanupStorage,
			Operation: repository.ArtifactCleanupRelative, RelativePath: path,
		}); err == nil {
			t.Fatalf("path %q was accepted", path)
		}
	}
}

func ptr[T any](value T) *T { return &value }
