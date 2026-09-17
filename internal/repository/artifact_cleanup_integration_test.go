package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestArtifactCleanupQueueRetryAndLeaseRecoveryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	projectID := uuid.Must(uuid.NewV7())
	path := projectID.String() + "/" + uuid.Must(uuid.NewV7()).String() + "/" + uuid.Must(uuid.NewV7()).String()
	jobID := uuid.Must(uuid.NewV7())
	_, err = pool.Exec(ctx, `
		INSERT INTO artifact_cleanup_jobs (id,project_id,store_kind,operation,relative_path)
		VALUES ($1,$2,'storage','relative',$3)`, jobID, projectID, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM artifact_cleanup_jobs WHERE id=$1`, jobID)
	})

	repo := New(pool)
	job, err := repo.ClaimNextArtifactCleanup(ctx, "worker-a", time.Minute)
	if err != nil || job.ID != jobID || job.Attempts != 1 {
		t.Fatalf("first claim = %#v err=%v", job, err)
	}
	if _, err := repo.ClaimNextArtifactCleanup(ctx, "worker-b", time.Minute); !errors.Is(err, ErrNoArtifactCleanup) {
		t.Fatalf("concurrent claim error = %v, want ErrNoArtifactCleanup", err)
	}
	if err := repo.RetryArtifactCleanup(ctx, job.ID, "worker-a", time.Now().UTC().Add(-time.Second), "physical removal failed"); err != nil {
		t.Fatal(err)
	}
	job, err = repo.ClaimNextArtifactCleanup(ctx, "worker-b", time.Minute)
	if err != nil || job.ID != jobID || job.Attempts != 2 {
		t.Fatalf("retry claim = %#v err=%v", job, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE artifact_cleanup_jobs SET leased_at=now()-interval '2 minutes' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.RequeueStaleArtifactCleanup(ctx, time.Minute); err != nil || count != 1 {
		t.Fatalf("stale lease recovery = count=%d err=%v", count, err)
	}
	job, err = repo.ClaimNextArtifactCleanup(ctx, "worker-c", time.Minute)
	if err != nil || job.ID != jobID || job.Attempts != 3 {
		t.Fatalf("recovered claim = %#v err=%v", job, err)
	}
	if err := repo.CompleteArtifactCleanup(ctx, job.ID, "worker-c"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimNextArtifactCleanup(ctx, "worker-d", time.Minute); !errors.Is(err, ErrNoArtifactCleanup) {
		t.Fatalf("post-completion claim error = %v, want ErrNoArtifactCleanup", err)
	}
}
