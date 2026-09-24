package repository

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAppRuntimeLeaseFencingFailureRecoveryAndConvergenceIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	appID := uuid.Must(uuid.NewV7())
	_, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "runtime-reconcile", Enabled: true, ArtifactQuotaBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	deploymentID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	selected, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if selected.DesiredDeploymentID == nil || *selected.DesiredDeploymentID != deploymentID.String() || selected.RuntimeStatus != "pending" || selected.ObservedGeneration != 0 {
		t.Fatalf("selected App did not enter pending runtime state: %+v", selected)
	}

	job, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if job.App.DesiredGeneration != selected.DesiredGeneration || job.App.DesiredDeploymentID == nil || *job.App.DesiredDeploymentID != deploymentID.String() || job.Deployment.ID != deploymentID.String() || job.ImagePath == "" {
		t.Fatalf("runtime claim lost selected immutable deployment: %+v", job)
	}
	if _, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-b", time.Minute); !errors.Is(err, ErrNoAppRuntimeJob) {
		t.Fatalf("leased App was claimed twice: %v", err)
	}
	if err := f.repo.RenewAppRuntimeLease(f.ctx, appID, job.WorkerID, job.LeaseToken, time.Minute); err != nil {
		t.Fatalf("lease renewal failed: %v", err)
	}
	if err := f.repo.FailAppRuntime(f.ctx, job, "failed", "container ownership conflict", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	failed, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if failed.RuntimeStatus != "failed" || failed.RuntimeError == nil || *failed.RuntimeError != "container ownership conflict" || failed.ObservedGeneration != 0 {
		t.Fatalf("runtime failure advanced observed state or lost safe error: %+v", failed)
	}
	if _, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-b", time.Minute); !errors.Is(err, ErrNoAppRuntimeJob) {
		t.Fatalf("runtime failure ignored its bounded retry time: %v", err)
	}

	changedWorkload := failed.Workload
	changedWorkload.Resources.CPUMillis = 750
	workingDirectory := "/srv/current"
	changedWorkload.WorkingDirectory = &workingDirectory
	changed, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &changedWorkload})
	if err != nil {
		t.Fatal(err)
	}
	if changed.DesiredGeneration != failed.DesiredGeneration+1 || changed.RuntimeStatus != "pending" || changed.RuntimeError != nil {
		t.Fatalf("desired update did not wake and clear failed runtime: %+v", changed)
	}
	changedJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if changedJob.App.Workload.Resources.CPUMillis != 750 || changedJob.App.Workload.WorkingDirectory == nil || *changedJob.App.Workload.WorkingDirectory != "/srv/current" || changedJob.Deployment.WorkloadSnapshot.Resources.CPUMillis != 500 {
		t.Fatalf("runtime claim did not preserve current mutable WorkloadSpec and immutable snapshot: %+v", changedJob)
	}

	// A user edit after claim fences completion even if the old worker finished
	// its Docker action. The newer generation remains eligible for a fresh claim.
	newerWorkload := changedJob.App.Workload
	newerWorkload.Resources.CPUMillis = 1000
	newer, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &newerWorkload})
	if err != nil {
		t.Fatal(err)
	}
	container := runtimeRepositoryContainer(changedJob, deploymentID)
	if err := f.repo.CompleteAppRuntime(f.ctx, changedJob, "running", container); !errors.Is(err, ErrAppRuntimeStale) {
		t.Fatalf("old generation completion = %v, want ErrAppRuntimeStale", err)
	}
	stillUnobserved, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if stillUnobserved.ObservedGeneration != 0 || stillUnobserved.DesiredGeneration != newer.DesiredGeneration {
		t.Fatalf("stale completion advanced desired observation: %+v", stillUnobserved)
	}

	currentJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-c", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if currentJob.App.DesiredGeneration != newer.DesiredGeneration || currentJob.App.Workload.Resources.CPUMillis != 1000 {
		t.Fatalf("new generation was not eligible after stale completion: %+v", currentJob.App)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET lease_expires_at=now()-interval '1 second' WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	if count, err := f.repo.RequeueStaleAppRuntimeLeases(f.ctx); err != nil || count != 1 {
		t.Fatalf("expired runtime lease requeue count=%d err=%v", count, err)
	}
	if err := f.repo.RenewAppRuntimeLease(f.ctx, appID, currentJob.WorkerID, currentJob.LeaseToken, time.Minute); !errors.Is(err, ErrAppRuntimeLeaseLost) {
		t.Fatalf("expired lease renewal = %v, want ErrAppRuntimeLeaseLost", err)
	}
	newLeaseJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-d", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if newLeaseJob.LeaseToken == currentJob.LeaseToken {
		t.Fatal("recovered runtime claim reused the expired lease token")
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, currentJob, "running", runtimeRepositoryContainer(currentJob, deploymentID)); !errors.Is(err, ErrAppRuntimeLeaseLost) {
		t.Fatalf("expired worker completion = %v, want ErrAppRuntimeLeaseLost", err)
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, newLeaseJob, "running", runtimeRepositoryContainer(newLeaseJob, deploymentID)); err != nil {
		t.Fatal(err)
	}
	running, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if running.RuntimeStatus != "running" || running.ObservedGeneration != running.DesiredGeneration || running.DesiredGeneration != newer.DesiredGeneration {
		t.Fatalf("successful runtime completion did not advance observed generation: %+v", running)
	}
	assertAppRuntimeContainerID(t, f, appID)

	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeCleanupContainerID(t, f, appID)
	cleanup, err := f.repo.ClaimNextAppRuntimeCleanup(f.ctx, "cleanup-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.AppID != appID || cleanup.ContainerID == nil || *cleanup.ContainerID != strings.Repeat("a", 64) || cleanup.ContainerName != AppRuntimeContainerName(appID) {
		t.Fatalf("App deletion did not persist runtime identity for cleanup: %+v", cleanup)
	}
	if err := f.repo.CompleteAppRuntimeCleanup(f.ctx, cleanup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ClaimNextAppRuntimeCleanup(f.ctx, "cleanup-worker-2", time.Minute); !errors.Is(err, ErrNoAppRuntimeCleanup) {
		t.Fatalf("completed App cleanup was claimed again: %v", err)
	}
}

func TestProjectDeletionQueuesRuntimeCleanupBeforeAppCascadeIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	appID := uuid.Must(uuid.NewV7())
	_, err := f.repo.CreateApp(f.ctx, appID, f.projectTwoID, f.actor, AppInput{Name: "project-runtime", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	deploymentID := createReadySelectedRuntimeDeployment(t, f, f.projectTwoID, appID)
	job, err := f.repo.ClaimNextAppRuntime(f.ctx, "project-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, job, "running", runtimeRepositoryContainer(job, deploymentID)); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeContainerID(t, f, appID)
	if err := f.repo.DeleteProject(f.ctx, f.projectTwoID, f.accountID, f.projectTwoName); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeCleanupContainerID(t, f, appID)
	cleanup, err := f.repo.ClaimNextAppRuntimeCleanup(f.ctx, "project-cleanup-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.ProjectID == nil || *cleanup.ProjectID != f.projectTwoID || cleanup.AppID != appID || cleanup.ContainerID == nil || *cleanup.ContainerID != strings.Repeat("a", 64) {
		t.Fatalf("project deletion lost runtime ownership cleanup data: %+v", cleanup)
	}
	if err := f.repo.CompleteAppRuntimeCleanup(f.ctx, cleanup); err != nil {
		t.Fatal(err)
	}
}

func assertAppRuntimeContainerID(t *testing.T, f appRepositoryFixture, appID uuid.UUID) {
	t.Helper()
	var containerID string
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(container_id,'') FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&containerID); err != nil {
		t.Fatal(err)
	}
	if containerID != strings.Repeat("a", 64) {
		t.Fatalf("successful runtime completion did not persist the inspected container ID: %v", containerID)
	}
}

func assertAppRuntimeCleanupContainerID(t *testing.T, f appRepositoryFixture, appID uuid.UUID) {
	t.Helper()
	var containerID string
	err := f.pool.QueryRow(f.ctx, `
		SELECT COALESCE(container_id,'')
		FROM app_runtime_cleanup_jobs
		WHERE app_id=$1 AND container_name=$2 AND status='pending'
		ORDER BY created_at DESC LIMIT 1`, appID, AppRuntimeContainerName(appID)).Scan(&containerID)
	if err != nil {
		t.Fatal(err)
	}
	if containerID != strings.Repeat("a", 64) {
		t.Fatalf("App runtime cleanup job did not persist the container ID: %v", containerID)
	}
}

func createReadySelectedRuntimeDeployment(t *testing.T, f appRepositoryFixture, projectID, appID uuid.UUID) uuid.UUID {
	t.Helper()
	deploymentID := uuid.Must(uuid.NewV7())
	sourcePath := projectID.String() + "/" + appID.String() + "/" + deploymentID.String()
	sourceCleanup := ArtifactCleanupInput{ProjectID: projectID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative, RelativePath: sourcePath}
	if err := f.repo.ReserveAppSourceUpload(f.ctx, projectID, appID, f.actor, 100, sourceCleanup); err != nil {
		t.Fatal(err)
	}
	platform, err := appbuildspec.HostPlatform(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.CreateAppDeployment(f.ctx, deploymentID, projectID, appID, f.actor, AppDeploymentInput{
		SourceName: "runtime-source.zip", SourceSizeBytes: 100, SourceChecksum: strings.Repeat("a", 64),
		SourcePath: sourcePath, SourceReserved: true,
		BuildSpec: appbuildspec.Spec{DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: platform},
		Select:    true, PublishCleanup: &sourceCleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	build, err := f.repo.ClaimNextAppDeployment(f.ctx, "runtime-build-worker")
	if err != nil || build.Deployment.ID != deploymentID.String() {
		t.Fatalf("runtime test deployment claim = %+v err=%v", build, err)
	}
	imagePath := projectID.String() + "/" + appID.String() + "/" + deploymentID.String()
	imageCleanup := ArtifactCleanupInput{ProjectID: projectID, StoreKind: ArtifactCleanupAppImages, Operation: ArtifactCleanupRelative, RelativePath: imagePath}
	if err := f.repo.ReserveAppImagePublish(f.ctx, projectID, appID, deploymentID, "runtime-build-worker", 100, imageCleanup); err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.CompleteAppDeploymentBuildWithCleanup(f.ctx, projectID, appID, deploymentID, "runtime-build-worker",
		"sha256:"+strings.Repeat("d", 64), imagePath, strings.Repeat("e", 64), 100, imageCleanup)
	if err != nil {
		t.Fatal(err)
	}
	return deploymentID
}

func runtimeRepositoryContainer(job AppRuntimeJob, deploymentID uuid.UUID) *AppRuntimeContainer {
	return &AppRuntimeContainer{
		ID: strings.Repeat("a", 64), Name: AppRuntimeContainerName(uuid.MustParse(job.App.ID)),
		ImageID: "sha256:" + strings.Repeat("b", 64), ImageDigest: "sha256:" + strings.Repeat("d", 64),
		RuntimeTag: "stealth-app/" + deploymentID.String() + ":runtime",
	}
}

func TestAppRuntimeMigrationCanBeAppliedWithoutLegacyRuntimeStateIntegration(t *testing.T) {
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
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('app_runtime_state','app_runtime_cleanup_jobs')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("App runtime migration created %d required tables", count)
	}
}

func cleanupAppRuntimeIntegrationRows(t *testing.T, f appRepositoryFixture) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = f.pool.Exec(ctx, `DELETE FROM app_runtime_cleanup_jobs WHERE project_id IN ($1,$2)`, f.projectOneID, f.projectTwoID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM artifact_cleanup_jobs WHERE project_id IN ($1,$2)`, f.projectOneID, f.projectTwoID)
	})
}
