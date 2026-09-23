package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/platformhostname"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type appRepositoryFixture struct {
	ctx            context.Context
	pool           *pgxpool.Pool
	repo           *Repository
	accountID      uuid.UUID
	organizationID uuid.UUID
	projectOneID   uuid.UUID
	projectTwoID   uuid.UUID
	projectOneName string
	projectTwoName string
	actor          AppActor
}

func TestAppDeploymentSnapshotSelectionAndQuotaIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	app, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{
		Name: "buildable", Enabled: true, ArtifactQuotaBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	deploymentID := uuid.Must(uuid.NewV7())
	sourcePath := f.projectOneID.String() + "/" + appID.String() + "/" + deploymentID.String()
	sourceCleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative, RelativePath: sourcePath}
	if err := f.repo.ReserveAppSourceUpload(f.ctx, f.projectOneID, appID, f.actor, 100, sourceCleanup); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ReserveAppSourceUpload(f.ctx, f.projectOneID, appID, f.actor, 1000, ArtifactCleanupInput{
		ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative,
		RelativePath: f.projectOneID.String() + "/" + appID.String() + "/" + uuid.Must(uuid.NewV7()).String(),
	}); !errors.Is(err, ErrAppArtifactQuotaExceeded) {
		t.Fatalf("source reservation beyond remaining quota = %v", err)
	}
	platform := "linux/amd64"
	definition := appbuildspec.Spec{DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: platform}
	workloadHash := app.WorkloadSpecSHA256
	sourceHash := strings.Repeat("a", 64)
	deployment, err := f.repo.CreateAppDeployment(f.ctx, deploymentID, f.projectOneID, appID, f.actor, AppDeploymentInput{
		SourceName: "source.zip", SourceSizeBytes: 100, SourceChecksum: sourceHash, SourcePath: sourcePath,
		SourceReserved: true, BuildSpec: definition, Select: true, PublishCleanup: &sourceCleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Version != 1 || deployment.BuildStatus != "queued" || deployment.WorkloadSpecSHA256 != workloadHash || deployment.Selected {
		t.Fatalf("queued deployment did not capture immutable App state: %+v", deployment)
	}

	portChange := workloadspec.Default()
	portChange.Port = 9090
	app, err = f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &portChange})
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.repo.ClaimNextAppDeployment(f.ctx, "build-worker-a")
	if err != nil || job.Deployment.ID != deployment.ID || job.Deployment.WorkloadSpecSHA256 != workloadHash || job.SourcePath != sourcePath {
		t.Fatalf("claimed job lost immutable inputs: %#v err=%v", job, err)
	}
	imageID := uuid.Must(uuid.NewV7())
	imagePath := f.projectOneID.String() + "/" + appID.String() + "/" + imageID.String()
	imageCleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppImages, Operation: ArtifactCleanupRelative, RelativePath: imagePath}
	if err := f.repo.ReserveAppImagePublish(f.ctx, f.projectOneID, appID, deploymentID, "build-worker-a", 50, imageCleanup); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	archiveHash := strings.Repeat("c", 64)
	ready, err := f.repo.CompleteAppDeploymentBuildWithCleanup(f.ctx, f.projectOneID, appID, deploymentID, "build-worker-a", digest, imagePath, archiveHash, 50, imageCleanup)
	if err != nil {
		t.Fatal(err)
	}
	app, err = f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != "ready" || !ready.Selected || app.DesiredDeploymentID == nil || *app.DesiredDeploymentID != deploymentID.String() || app.DesiredGeneration != 3 || app.ObservedGeneration != 0 || app.RuntimeStatus != "not_deployed" || app.WorkloadSpecSHA256 == workloadHash {
		t.Fatalf("successful build and selection violated runtime truth or generation semantics: App=%+v deployment=%+v", app, ready)
	}
	if !workloadspec.Equal(ready.WorkloadSnapshot, workloadspec.Default()) || ready.WorkloadSpecSHA256 != workloadHash {
		t.Fatalf("completed deployment snapshot changed after App edit: %+v", ready)
	}
	noOp, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, deploymentID, f.actor)
	if err != nil || !noOp.Selected || app.DesiredGeneration != 3 {
		t.Fatalf("selecting same deployment was not idempotent: %+v err=%v", noOp, err)
	}
	if err := f.repo.DeleteAppDeployment(f.ctx, f.projectOneID, appID, deploymentID, f.actor); !errors.Is(err, ErrAppDeploymentSelected) {
		t.Fatalf("selected deployment delete = %v", err)
	}
	var used, reserved int64
	if err := f.pool.QueryRow(f.ctx, `SELECT artifact_used_bytes,artifact_reserved_bytes FROM project_apps WHERE id=$1`, appID).Scan(&used, &reserved); err != nil {
		t.Fatal(err)
	}
	if used != 150 || reserved != 0 {
		t.Fatalf("artifact quota counters = used %d reserved %d, want 150 and 0", used, reserved)
	}
	encoded, err := json.Marshal(ready)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "source_path") || strings.Contains(string(encoded), "image_path") || strings.Contains(string(encoded), "build_worker_id") {
		t.Fatalf("public AppDeployment exposed private build data: %s", encoded)
	}
}

func TestAppDeploymentConcurrentVersionsAndLeaseFencingIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{
		Name: "lease-fencing", Enabled: true, ArtifactQuotaBytes: 4096,
	}); err != nil {
		t.Fatal(err)
	}
	type createdDeployment struct {
		item domain.AppDeployment
		err  error
	}
	created := make(chan createdDeployment, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			defer ready.Done()
			<-start
			deploymentID := uuid.Must(uuid.NewV7())
			sourcePath := f.projectOneID.String() + "/" + appID.String() + "/" + deploymentID.String()
			cleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative, RelativePath: sourcePath}
			if err := f.repo.ReserveAppSourceUpload(f.ctx, f.projectOneID, appID, f.actor, 100, cleanup); err != nil {
				created <- createdDeployment{err: err}
				return
			}
			platform, err := appbuildspec.HostPlatform("amd64")
			if err != nil {
				created <- createdDeployment{err: err}
				return
			}
			item, err := f.repo.CreateAppDeployment(f.ctx, deploymentID, f.projectOneID, appID, f.actor, AppDeploymentInput{
				SourceName: "source.zip", SourceSizeBytes: 100,
				SourceChecksum: strings.Repeat("a", 64), SourcePath: sourcePath, SourceReserved: true,
				BuildSpec:      appbuildspec.Spec{DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: platform},
				PublishCleanup: &cleanup,
			})
			created <- createdDeployment{item: item, err: err}
		}()
	}
	close(start)
	ready.Wait()
	close(created)
	versions := make([]int64, 0, 2)
	for result := range created {
		if result.err != nil {
			t.Fatal(result.err)
		}
		versions = append(versions, result.item.Version)
	}
	if len(versions) != 2 || !((versions[0] == 1 && versions[1] == 2) || (versions[0] == 2 && versions[1] == 1)) {
		t.Fatalf("concurrent App deployment versions = %v, want distinct monotonic versions 1 and 2", versions)
	}

	lockTx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var versionOneID uuid.UUID
	if err := lockTx.QueryRow(f.ctx, `SELECT id FROM app_deployments WHERE app_id=$1 AND version=1 FOR UPDATE`, appID).Scan(&versionOneID); err != nil {
		t.Fatal(err)
	}
	first, err := f.repo.ClaimNextAppDeployment(f.ctx, "lease-first")
	if err != nil {
		t.Fatal(err)
	}
	if first.Deployment.Version != 2 {
		t.Fatalf("claim did not skip a locked oldest build: got version %d, want 2", first.Deployment.Version)
	}
	if err := lockTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	other, err := f.repo.ClaimNextAppDeployment(f.ctx, "lease-other")
	if err != nil {
		t.Fatal(err)
	}
	if other.Deployment.ID != versionOneID.String() {
		t.Fatalf("claim after releasing lock = %q, want version-one deployment %q", other.Deployment.ID, versionOneID)
	}
	reservedImageID := uuid.Must(uuid.NewV7())
	reservedImagePath := f.projectOneID.String() + "/" + appID.String() + "/" + reservedImageID.String()
	reservedImageCleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppImages, Operation: ArtifactCleanupRelative, RelativePath: reservedImagePath}
	if err := f.repo.ReserveAppImagePublish(f.ctx, f.projectOneID, appID, uuid.MustParse(first.Deployment.ID), first.WorkerID, 50, reservedImageCleanup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE artifact_cleanup_jobs SET updated_at=now()-interval '48 hours' WHERE project_id=$1 AND store_kind='app_images' AND relative_path=$2`, f.projectOneID, reservedImagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.RequeueStaleArtifactCleanup(f.ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	var activeReservationStatus string
	if err := f.pool.QueryRow(f.ctx, `SELECT status FROM artifact_cleanup_jobs WHERE project_id=$1 AND store_kind='app_images' AND relative_path=$2`, f.projectOneID, reservedImagePath).Scan(&activeReservationStatus); err != nil {
		t.Fatal(err)
	}
	if activeReservationStatus != "reserved" {
		t.Fatalf("cleanup requeued an image reservation for an active build: %q", activeReservationStatus)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_deployments SET build_started_at=now()-interval '1 hour' WHERE id=$1`, first.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	requeued, err := f.repo.RequeueStaleAppDeployments(f.ctx, time.Minute)
	if err != nil || requeued != 1 {
		t.Fatalf("stale build recovery = %d, %v; want one requeued lease", requeued, err)
	}
	var reservationStatus string
	var reservationAppID *uuid.UUID
	var reservationBytes int64
	if err := f.pool.QueryRow(f.ctx, `SELECT status,quota_app_id,quota_reserved_bytes FROM artifact_cleanup_jobs WHERE project_id=$1 AND store_kind='app_images' AND relative_path=$2`, f.projectOneID, reservedImagePath).Scan(&reservationStatus, &reservationAppID, &reservationBytes); err != nil {
		t.Fatal(err)
	}
	if reservationStatus != "pending" || reservationAppID != nil || reservationBytes != 0 {
		t.Fatalf("stale image publish reservation was not converted to cleanup: status=%s app=%v bytes=%d", reservationStatus, reservationAppID, reservationBytes)
	}
	second, err := f.repo.ClaimNextAppDeployment(f.ctx, "lease-second")
	if err != nil {
		t.Fatal(err)
	}
	if second.Deployment.ID != first.Deployment.ID || first.WorkerID == second.WorkerID {
		t.Fatalf("reclaimed lease did not receive a distinct fence: first=%+v second=%+v", first, second)
	}
	firstID := uuid.MustParse(first.Deployment.ID)
	secondID := uuid.MustParse(second.Deployment.ID)
	if _, err := f.repo.FailAppDeploymentBuild(f.ctx, f.projectOneID, appID, firstID, first.WorkerID, "stale worker"); !errors.Is(err, ErrAppBuildNotOwned) {
		t.Fatalf("stale worker completion error = %v, want ErrAppBuildNotOwned", err)
	}
	if _, err := f.repo.FailAppDeploymentBuild(f.ctx, f.projectOneID, appID, secondID, second.WorkerID, "expected integration failure"); err != nil {
		t.Fatalf("current lease could not complete its failure transition: %v", err)
	}
	if _, err := f.repo.FailAppDeploymentBuild(f.ctx, f.projectOneID, appID, versionOneID, other.WorkerID, "expected integration failure"); err != nil {
		t.Fatalf("second claimed build could not complete its failure transition: %v", err)
	}
}

func TestAppDeploymentSelectionRollbackQuotaAndDeletionCleanupIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{
		Name: "immutable-deployments", Enabled: true, ArtifactQuotaBytes: 4096,
	}); err != nil {
		t.Fatal(err)
	}

	first := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	second := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	firstReady := completeAppDeploymentForTest(t, f, appID, first.ID, "build-selection-one")
	secondReady := completeAppDeploymentForTest(t, f, appID, second.ID, "build-selection-two")
	if firstReady.BuildStatus != "succeeded" || secondReady.BuildStatus != "succeeded" {
		t.Fatalf("test builds did not complete successfully: first=%+v second=%+v", firstReady, secondReady)
	}

	for index, item := range []domain.AppDeployment{firstReady, secondReady, firstReady} {
		selected, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(item.ID), f.actor)
		if err != nil || !selected.Selected {
			t.Fatalf("selecting immutable deployment %s: item=%+v err=%v", item.ID, selected, err)
		}
		app, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		wantGeneration := int64(index + 2)
		if app.DesiredGeneration != wantGeneration || app.ObservedGeneration != 0 || app.RuntimeStatus != "not_deployed" || app.DesiredDeploymentID == nil || *app.DesiredDeploymentID != item.ID {
			t.Fatalf("selection %d violated desired/runtime state: App=%+v selected=%+v", index, app, selected)
		}
	}
	noOp, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor)
	if err != nil || !noOp.Selected {
		t.Fatalf("same-deployment select was not idempotent: %+v err=%v", noOp, err)
	}
	app, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if app.DesiredGeneration != 4 || app.ObservedGeneration != 0 || app.RuntimeStatus != "not_deployed" {
		t.Fatalf("no-op selection advanced generation or runtime state: %+v", app)
	}
	if err := f.repo.DeleteAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor); !errors.Is(err, ErrAppDeploymentSelected) {
		t.Fatalf("selected deployment deletion error = %v, want ErrAppDeploymentSelected", err)
	}
	secondSourcePath, secondImagePath := appArtifactPathsForTest(t, f, f.projectOneID, second.ID)
	if err := f.repo.DeleteAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(second.ID), f.actor); err != nil {
		t.Fatalf("unselected deployment deletion failed: %v", err)
	}
	assertArtifactCleanupJob(t, f, f.projectOneID, ArtifactCleanupAppSources, secondSourcePath)
	assertArtifactCleanupJob(t, f, f.projectOneID, ArtifactCleanupAppImages, secondImagePath)
	firstSourcePath, firstImagePath := appArtifactPathsForTest(t, f, f.projectOneID, first.ID)
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); err != nil {
		t.Fatal(err)
	}
	assertArtifactCleanupJob(t, f, f.projectOneID, ArtifactCleanupAppSources, firstSourcePath)
	assertArtifactCleanupJob(t, f, f.projectOneID, ArtifactCleanupAppImages, firstImagePath)
	if err := assertAppDeploymentArtifactsDeleted(t, f, appID); err != nil {
		t.Fatal(err)
	}

	quotaAppID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, quotaAppID, f.projectOneID, f.actor, AppInput{
		Name: "quota-race", Enabled: true, ArtifactQuotaBytes: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	failingBuild := createQueuedAppDeploymentForTest(t, f, f.projectOneID, quotaAppID, false)
	failingJob, err := f.repo.ClaimNextAppDeployment(f.ctx, "failed-image-publish")
	if err != nil || failingJob.Deployment.ID != failingBuild.ID {
		t.Fatalf("claiming failure-fixture deployment: job=%+v err=%v", failingJob, err)
	}
	failingImageID := uuid.Must(uuid.NewV7())
	failingImagePath := f.projectOneID.String() + "/" + quotaAppID.String() + "/" + failingImageID.String()
	failingImageCleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppImages, Operation: ArtifactCleanupRelative, RelativePath: failingImagePath}
	if err := f.repo.ReserveAppImagePublish(f.ctx, f.projectOneID, quotaAppID, uuid.MustParse(failingBuild.ID), failingJob.WorkerID, 200, failingImageCleanup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.FailAppDeploymentBuild(f.ctx, f.projectOneID, quotaAppID, uuid.MustParse(failingBuild.ID), failingJob.WorkerID, "expected test failure"); err != nil {
		t.Fatal(err)
	}
	assertArtifactCleanupJob(t, f, f.projectOneID, ArtifactCleanupAppImages, failingImagePath)
	var failedImageStatus string
	var failedImageAppID *uuid.UUID
	var failedImageReserved int64
	if err := f.pool.QueryRow(f.ctx, `SELECT status,quota_app_id,quota_reserved_bytes FROM artifact_cleanup_jobs WHERE project_id=$1 AND store_kind='app_images' AND relative_path=$2`, f.projectOneID, failingImagePath).Scan(&failedImageStatus, &failedImageAppID, &failedImageReserved); err != nil {
		t.Fatal(err)
	}
	if failedImageStatus != "pending" || failedImageAppID != nil || failedImageReserved != 0 {
		t.Fatalf("failed image publish reservation did not release cleanup/quota: status=%s app=%v bytes=%d", failedImageStatus, failedImageAppID, failedImageReserved)
	}
	type reservation struct {
		cleanup ArtifactCleanupInput
		err     error
	}
	start := make(chan struct{})
	reservations := make(chan reservation, 2)
	var reservers sync.WaitGroup
	for range 2 {
		reservers.Add(1)
		go func() {
			defer reservers.Done()
			<-start
			cleanup := ArtifactCleanupInput{
				ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative,
				RelativePath: f.projectOneID.String() + "/" + quotaAppID.String() + "/" + uuid.Must(uuid.NewV7()).String(),
			}
			err := f.repo.ReserveAppSourceUpload(f.ctx, f.projectOneID, quotaAppID, f.actor, 600, cleanup)
			reservations <- reservation{cleanup: cleanup, err: err}
		}()
	}
	close(start)
	reservers.Wait()
	close(reservations)
	reservedCount, quotaErrors := 0, 0
	var accepted ArtifactCleanupInput
	for result := range reservations {
		switch {
		case result.err == nil:
			reservedCount++
			accepted = result.cleanup
		case errors.Is(result.err, ErrAppArtifactQuotaExceeded):
			quotaErrors++
		default:
			t.Fatalf("concurrent source quota reservation error = %v", result.err)
		}
	}
	if reservedCount != 1 || quotaErrors != 1 {
		t.Fatalf("concurrent quota reservations: accepted=%d quota errors=%d, want 1/1", reservedCount, quotaErrors)
	}
	if err := f.repo.AbandonAppSourceUpload(f.ctx, accepted); err != nil {
		t.Fatal(err)
	}
	var reservedBytes int64
	if err := f.pool.QueryRow(f.ctx, `SELECT artifact_reserved_bytes FROM project_apps WHERE id=$1`, quotaAppID).Scan(&reservedBytes); err != nil {
		t.Fatal(err)
	}
	if reservedBytes != 0 {
		t.Fatalf("abandoned source reservation did not release quota: %d", reservedBytes)
	}
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, quotaAppID, f.actor); err != nil {
		t.Fatal(err)
	}

	projectAppID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, projectAppID, f.projectTwoID, f.actor, AppInput{Name: "project-cleanup", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	queued := createQueuedAppDeploymentForTest(t, f, f.projectTwoID, projectAppID, false)
	if _, err := f.repo.GetAppDeployment(f.ctx, f.projectTwoID, projectAppID, uuid.MustParse(queued.ID), f.actor); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteProject(f.ctx, f.projectTwoID, f.accountID, f.projectTwoName); err != nil {
		t.Fatal(err)
	}
	if err := assertAppDeploymentArtifactsDeleted(t, f, projectAppID); err != nil {
		t.Fatal(err)
	}
	for _, storeKind := range []ArtifactCleanupStoreKind{ArtifactCleanupAppSources, ArtifactCleanupAppImages} {
		var count int
		if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM artifact_cleanup_jobs WHERE project_id=$1 AND store_kind=$2 AND operation='project'`, f.projectTwoID, storeKind).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("project deletion cleanup jobs for %s = %d, want 1", storeKind, count)
		}
	}
}

func TestAppAndProjectDeletionWaitForArtifactPublishReservationsIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "reserved-upload", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	appSourcePath := f.projectOneID.String() + "/" + appID.String() + "/" + uuid.Must(uuid.NewV7()).String()
	appSourceCleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative, RelativePath: appSourcePath}
	if err := f.repo.ReserveAppSourceUpload(f.ctx, f.projectOneID, appID, f.actor, 64, appSourceCleanup); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); !errors.Is(err, ErrAppArtifactPublishInProgress) {
		t.Fatalf("delete App with an active source publication reservation = %v", err)
	}
	var appCount int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM project_apps WHERE project_id=$1 AND id=$2`, f.projectOneID, appID).Scan(&appCount); err != nil {
		t.Fatal(err)
	}
	if appCount != 1 {
		t.Fatal("App was deleted while a source artifact publication was reserved")
	}
	if err := f.repo.AbandonAppSourceUpload(f.ctx, appSourceCleanup); err != nil {
		t.Fatal(err)
	}
	deployment := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	buildJob, err := f.repo.ClaimNextAppDeployment(f.ctx, "delete-race-worker")
	if err != nil || buildJob.Deployment.ID != deployment.ID {
		t.Fatalf("claim build for deletion race: job=%+v err=%v", buildJob, err)
	}
	imagePath := f.projectOneID.String() + "/" + appID.String() + "/" + uuid.Must(uuid.NewV7()).String()
	imageCleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppImages, Operation: ArtifactCleanupRelative, RelativePath: imagePath}
	if err := f.repo.ReserveAppImagePublish(f.ctx, f.projectOneID, appID, uuid.MustParse(deployment.ID), buildJob.WorkerID, 128, imageCleanup); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); !errors.Is(err, ErrAppArtifactPublishInProgress) {
		t.Fatalf("delete App with an active OCI publication reservation = %v", err)
	}
	if _, err := f.repo.FailAppDeploymentBuild(f.ctx, f.projectOneID, appID, uuid.MustParse(deployment.ID), buildJob.WorkerID, "expected deletion race fixture"); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); err != nil {
		t.Fatalf("delete App after reservation release: %v", err)
	}

	projectAppID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, projectAppID, f.projectTwoID, f.actor, AppInput{Name: "reserved-project-upload", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	projectSourcePath := f.projectTwoID.String() + "/" + projectAppID.String() + "/" + uuid.Must(uuid.NewV7()).String()
	projectSourceCleanup := ArtifactCleanupInput{ProjectID: f.projectTwoID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative, RelativePath: projectSourcePath}
	if err := f.repo.ReserveAppSourceUpload(f.ctx, f.projectTwoID, projectAppID, f.actor, 64, projectSourceCleanup); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteProject(f.ctx, f.projectTwoID, f.accountID, f.projectTwoName); !errors.Is(err, ErrAppArtifactPublishInProgress) {
		t.Fatalf("delete project with an active App artifact reservation = %v", err)
	}
	var projectCount int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM projects WHERE id=$1`, f.projectTwoID).Scan(&projectCount); err != nil {
		t.Fatal(err)
	}
	if projectCount != 1 {
		t.Fatal("project was deleted while an App source artifact publication was reserved")
	}
	if err := f.repo.AbandonAppSourceUpload(f.ctx, projectSourceCleanup); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteProject(f.ctx, f.projectTwoID, f.accountID, f.projectTwoName); err != nil {
		t.Fatalf("delete project after reservation release: %v", err)
	}
}

func createQueuedAppDeploymentForTest(t *testing.T, f appRepositoryFixture, projectID, appID uuid.UUID, selectAfterBuild bool) domain.AppDeployment {
	t.Helper()
	deploymentID := uuid.Must(uuid.NewV7())
	sourcePath := projectID.String() + "/" + appID.String() + "/" + deploymentID.String()
	cleanup := ArtifactCleanupInput{ProjectID: projectID, StoreKind: ArtifactCleanupAppSources, Operation: ArtifactCleanupRelative, RelativePath: sourcePath}
	if err := f.repo.ReserveAppSourceUpload(f.ctx, projectID, appID, f.actor, 64, cleanup); err != nil {
		t.Fatal(err)
	}
	platform, err := appbuildspec.HostPlatform("amd64")
	if err != nil {
		t.Fatal(err)
	}
	item, err := f.repo.CreateAppDeployment(f.ctx, deploymentID, projectID, appID, f.actor, AppDeploymentInput{
		SourceName: "source.tar", SourceSizeBytes: 64, SourceChecksum: strings.Repeat("a", 64), SourcePath: sourcePath,
		SourceReserved: true, BuildSpec: appbuildspec.Spec{DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: platform},
		Select: selectAfterBuild, PublishCleanup: &cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func completeAppDeploymentForTest(t *testing.T, f appRepositoryFixture, appID uuid.UUID, deploymentID, workerID string) domain.AppDeployment {
	t.Helper()
	job, err := f.repo.ClaimNextAppDeployment(f.ctx, workerID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Deployment.ID != deploymentID {
		t.Fatalf("claimed deployment %q, want %q", job.Deployment.ID, deploymentID)
	}
	imageID := uuid.Must(uuid.NewV7())
	imagePath := f.projectOneID.String() + "/" + appID.String() + "/" + imageID.String()
	cleanup := ArtifactCleanupInput{ProjectID: f.projectOneID, StoreKind: ArtifactCleanupAppImages, Operation: ArtifactCleanupRelative, RelativePath: imagePath}
	if err := f.repo.ReserveAppImagePublish(f.ctx, f.projectOneID, appID, uuid.MustParse(deploymentID), workerID, 128, cleanup); err != nil {
		t.Fatal(err)
	}
	item, err := f.repo.CompleteAppDeploymentBuildWithCleanup(f.ctx, f.projectOneID, appID, uuid.MustParse(deploymentID), workerID,
		"sha256:"+strings.Repeat("b", 64), imagePath, strings.Repeat("c", 64), 128, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func appArtifactPathsForTest(t *testing.T, f appRepositoryFixture, projectID uuid.UUID, deploymentID string) (string, string) {
	t.Helper()
	var sourcePath, imagePath string
	if err := f.pool.QueryRow(f.ctx, `SELECT source_path,image_path FROM app_deployments WHERE project_id=$1 AND id=$2`, projectID, uuid.MustParse(deploymentID)).Scan(&sourcePath, &imagePath); err != nil {
		t.Fatal(err)
	}
	if sourcePath == "" || imagePath == "" {
		t.Fatalf("test deployment artifact metadata is incomplete: %q %q", sourcePath, imagePath)
	}
	return sourcePath, imagePath
}

func assertArtifactCleanupJob(t *testing.T, f appRepositoryFixture, projectID uuid.UUID, kind ArtifactCleanupStoreKind, relativePath string) {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM artifact_cleanup_jobs WHERE project_id=$1 AND store_kind=$2 AND operation='relative' AND relative_path=$3`, projectID, kind, relativePath).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("cleanup queue for %s %q = %d, want 1", kind, relativePath, count)
	}
}

func assertAppDeploymentArtifactsDeleted(t *testing.T, f appRepositoryFixture, appID uuid.UUID) error {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) FROM app_deployments WHERE app_id=$1`,
		`SELECT count(*) FROM app_build_logs WHERE app_id=$1`,
	} {
		var count int
		if err := f.pool.QueryRow(f.ctx, query, appID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("App deletion left %d rows for query %q", count, query)
		}
	}
	return nil
}

func newAppRepositoryFixture(t *testing.T) appRepositoryFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID := uuid.Must(uuid.NewV7())
	organizationID := uuid.Must(uuid.NewV7())
	projectOneID := uuid.Must(uuid.NewV7())
	projectTwoID := uuid.Must(uuid.NewV7())
	suffix := strings.ToLower(accountID.String()[:8])
	projectOneName := "apps-primary-" + suffix
	projectTwoName := "apps-secondary-" + suffix
	organizationSlug := "apps-repository-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,'test-hash')`, accountID, fmt.Sprintf("apps-%s@example.test", accountID)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Apps repository integration',$2)`, organizationID, organizationSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, organizationID, accountID); err != nil {
		t.Fatal(err)
	}
	for _, project := range []struct {
		id   uuid.UUID
		name string
	}{
		{id: projectOneID, name: projectOneName},
		{id: projectTwoID, name: projectTwoName},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3)`, project.id, organizationID, project.name); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, organizationID, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, accountID)
	})
	return appRepositoryFixture{
		ctx:            ctx,
		pool:           pool,
		repo:           New(pool),
		accountID:      accountID,
		organizationID: organizationID,
		projectOneID:   projectOneID,
		projectTwoID:   projectTwoID,
		projectOneName: projectOneName,
		projectTwoName: projectTwoName,
		actor:          AppActor{Kind: AppConsoleActor, AccountID: accountID},
	}
}

func TestAppsPersistenceGenerationAndPlanLimitIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)

	workload := workloadspec.Default()
	workingDirectory := "/srv/app"
	workload.WorkingDirectory = &workingDirectory
	firstID := uuid.Must(uuid.NewV7())
	first, err := f.repo.CreateApp(f.ctx, firstID, f.projectOneID, f.actor, AppInput{
		Name: "backend", Enabled: true, Workload: workload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.DesiredGeneration != 1 || first.ObservedGeneration != 0 || first.RuntimeStatus != "not_deployed" || first.RuntimeError != nil {
		t.Fatalf("initial App runtime state = %+v", first)
	}
	wantDigest, err := workloadspec.Digest(workload)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkloadSpecSHA256 != wantDigest {
		t.Fatalf("initial spec digest = %q, want %q", first.WorkloadSpecSHA256, wantDigest)
	}
	var internalFields struct {
		PlatformLabel *string `json:"platform_label"`
	}
	encoded, err := jsonMarshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal(encoded, &internalFields); err != nil {
		t.Fatal(err)
	}
	if internalFields.PlatformLabel != nil {
		t.Fatal("public App DTO exposed the persisted platform label")
	}

	newName := "backend-service"
	renamed, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.DesiredGeneration != 1 || renamed.WorkloadSpecSHA256 != first.WorkloadSpecSHA256 {
		t.Fatalf("rename changed runtime generation or digest: %+v", renamed)
	}
	equivalent, err := workloadspec.Decode([]byte(`{"working_directory":"/srv//app/../app"}`))
	if err != nil {
		t.Fatal(err)
	}
	noOp, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &equivalent})
	if err != nil {
		t.Fatal(err)
	}
	if noOp.DesiredGeneration != 1 || noOp.WorkloadSpecSHA256 != first.WorkloadSpecSHA256 || !workloadspec.Equal(noOp.Workload, first.Workload) {
		t.Fatalf("semantically equivalent spec created runtime work: %+v", noOp)
	}
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &equivalent}); err != nil {
		t.Fatal(err)
	}
	var auditCount int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='app.update' AND target_id=$2`, f.organizationID, firstID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("no-op workload patch emitted audit event; app.update count=%d, want rename only", auditCount)
	}
	var specLeaked bool
	if err := f.pool.QueryRow(f.ctx, `SELECT metadata ? 'workload' OR metadata ? 'workload_spec' FROM audit_events WHERE organization_id=$1 AND action='app.create' AND target_id=$2`, f.organizationID, firstID).Scan(&specLeaked); err != nil {
		t.Fatal(err)
	}
	if specLeaked {
		t.Fatal("App audit metadata included WorkloadSpec")
	}

	disabled := false
	updated, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredGeneration != 2 || updated.ObservedGeneration != 0 || updated.RuntimeStatus != "not_deployed" {
		t.Fatalf("enabled-state change violated generation truth: %+v", updated)
	}
	portChange := workloadspec.Default()
	portChange.Port = 9090
	updated, err = f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &portChange})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredGeneration != 3 || updated.ObservedGeneration != 0 || updated.WorkloadSpecSHA256 == first.WorkloadSpecSHA256 {
		t.Fatalf("workload change violated generation or digest semantics: %+v", updated)
	}
	updated, err = f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &portChange})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredGeneration != 3 {
		t.Fatalf("repeated identical workload incremented generation to %d", updated.DesiredGeneration)
	}

	second, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectTwoID, f.actor, AppInput{Name: "frontend", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Workload.Port != workloadspec.DefaultPort || second.Workload.HealthCheck.Protocol != "tcp" {
		t.Fatalf("repository did not persist shared WorkloadSpec defaults: %+v", second.Workload)
	}

	type createResult struct {
		item domain.App
		err  error
	}
	start := make(chan struct{})
	results := make(chan createResult, 2)
	var writers sync.WaitGroup
	for _, name := range []string{"worker-one", "worker-two"} {
		writers.Add(1)
		go func(name string) {
			defer writers.Done()
			<-start
			item, createErr := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, f.actor, AppInput{Name: name, Enabled: true})
			results <- createResult{item: item, err: createErr}
		}(name)
	}
	close(start)
	writers.Wait()
	close(results)
	succeeded, limitErrors := 0, 0
	for result := range results {
		if result.err == nil {
			succeeded++
		} else if errors.Is(result.err, ErrPlanLimitExceeded) {
			limitErrors++
		} else {
			t.Fatalf("concurrent App create error = %v", result.err)
		}
	}
	if succeeded != 1 || limitErrors != 1 {
		t.Fatalf("concurrent final-slot results: succeeded=%d plan-limited=%d, want 1 each", succeeded, limitErrors)
	}
	if _, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, f.actor, AppInput{Name: "fourth-app", Enabled: true}); !errors.Is(err, ErrPlanLimitExceeded) {
		t.Fatalf("free plan fourth App error = %v, want plan limit", err)
	}

	plan, err := f.repo.OrganizationPlan(f.ctx, f.organizationID, f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Limits.Apps != 3 || plan.Usage.Apps != 3 {
		t.Fatalf("free plan Apps projection = limit %d, usage %d; want 3/3", plan.Limits.Apps, plan.Usage.Apps)
	}
	page, next, _, err := f.repo.ListApps(f.ctx, f.projectOneID, f.actor, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || next == "" {
		t.Fatalf("App first page = %d items, cursor %q; want one item and next cursor", len(page), next)
	}
	cursor := uuid.MustParse(next)
	secondPage, _, _, err := f.repo.ListApps(f.ctx, f.projectOneID, f.actor, 1, &cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage) != 1 || secondPage[0].ID == page[0].ID {
		t.Fatalf("App pagination second page = %+v", secondPage)
	}

	readKeyID := uuid.Must(uuid.NewV7())
	writeKeyID := uuid.Must(uuid.NewV7())
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO project_api_keys (id,project_id,name,prefix,secret_hash,scopes) VALUES ($1,$2,'Apps read','stl_key_readapps',$3,$4),($5,$2,'Apps write','stl_key_writeapp',$6,$7)`, readKeyID, f.projectOneID, bytesOfZeroes(32), []string{"apps.read"}, writeKeyID, bytesOfOnes(32), []string{"apps.write"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.repo.ListApps(f.ctx, f.projectOneID, AppActor{Kind: AppAPIKeyActor, APIKeyID: readKeyID, APIKeyScopes: []string{"apps.read"}}, 10, nil); err != nil {
		t.Fatalf("apps.read API key could not list Apps: %v", err)
	}
	if _, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, AppActor{Kind: AppAPIKeyActor, APIKeyID: readKeyID, APIKeyScopes: []string{"apps.read"}}, AppInput{Name: "read-only", Enabled: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("apps.read API key write error = %v, want forbidden", err)
	}
	writeActor := AppActor{Kind: AppAPIKeyActor, APIKeyID: writeKeyID, APIKeyScopes: []string{"apps.write"}}
	if _, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, writeActor, AppInput{Name: "write-scope-limited", Enabled: true}); !errors.Is(err, ErrPlanLimitExceeded) {
		t.Fatalf("apps.write API key was not authorized before plan enforcement: %v", err)
	}
	if _, err := f.repo.GetApp(f.ctx, f.projectOneID, firstID, AppActor{Kind: AppConsoleActor, AccountID: uuid.Must(uuid.NewV7())}); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-account App read error = %v, want hidden denial", err)
	}
}

func TestAppsSharedHostnameNamespaceConcurrencyAndSiteRouteIsolationIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_plans (organization_id,plan_key) VALUES ($1,'enterprise') ON CONFLICT (organization_id) DO UPDATE SET plan_key='enterprise'`, f.organizationID); err != nil {
		t.Fatal(err)
	}

	var previousWorkloadDomain *string
	if err := f.pool.QueryRow(f.ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousWorkloadDomain); err != nil {
		t.Fatal(err)
	}
	workloadDomain := "apps.example.com"
	if _, err := f.pool.Exec(f.ctx, `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, workloadDomain); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		var restore any
		if previousWorkloadDomain != nil {
			restore = *previousWorkloadDomain
		}
		_, _ = f.pool.Exec(cleanupCtx, `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, restore)
	})

	siteActor := SiteActor{Kind: SiteConsoleActor, AccountID: f.accountID}
	site, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: "backend", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	siteRoutesBefore, err := f.repo.ListPlatformRoutes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	appID := uuid.Must(uuid.NewV7())
	app, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "backend", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if app.PlatformHostname == nil || *app.PlatformHostname == "backend.apps.example.com" {
		t.Fatalf("Site/App same-name claim did not allocate unique reserved App hostname: %v", app.PlatformHostname)
	}
	siteRoutesAfter, err := f.repo.ListPlatformRoutes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(siteRoutesAfter, siteRoutesBefore) {
		t.Fatalf("creating App changed static Site route snapshot: before=%+v after=%+v", siteRoutesBefore, siteRoutesAfter)
	}
	if site.PlatformHostname == nil || *site.PlatformHostname != "backend.apps.example.com" {
		t.Fatalf("Site hostname changed after App claim: %v", site.PlatformHostname)
	}

	duplicateSiteID := uuid.Must(uuid.NewV7())
	duplicateSiteCandidate := platformhostname.Candidates("backend", duplicateSiteID)[1]
	if _, err := f.repo.CreateSite(f.ctx, duplicateSiteID, f.projectOneID, siteActor, SiteInput{
		Name: "backend", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Site create error = %v, want conflict", err)
	}
	var failedSiteClaim int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label=$1`, duplicateSiteCandidate).Scan(&failedSiteClaim); err != nil {
		t.Fatal(err)
	}
	if failedSiteClaim != 0 {
		t.Fatalf("failed Site insert left a hostname claim for %q", duplicateSiteCandidate)
	}
	duplicateAppID := uuid.Must(uuid.NewV7())
	duplicateAppCandidate := platformhostname.AppCandidates("backend", duplicateAppID)[1]
	if _, err := f.repo.CreateApp(f.ctx, duplicateAppID, f.projectOneID, f.actor, AppInput{Name: "backend", Enabled: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate App create error = %v, want conflict", err)
	}
	var failedAppClaim int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label=$1`, duplicateAppCandidate).Scan(&failedAppClaim); err != nil {
		t.Fatal(err)
	}
	if failedAppClaim != 0 {
		t.Fatalf("failed App insert left a hostname claim for %q", duplicateAppCandidate)
	}

	appLabel := ""
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_apps WHERE id=$1`, appID).Scan(&appLabel); err != nil {
		t.Fatal(err)
	}
	newName := "backend-service"
	renamed, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.PlatformHostname == nil || *renamed.PlatformHostname != *app.PlatformHostname {
		t.Fatalf("App rename changed hostname from %q to %v", *app.PlatformHostname, renamed.PlatformHostname)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT label FROM platform_hostname_claims WHERE resource_type='app' AND resource_id=$1`, appID).Scan(&newName); err != nil {
		t.Fatal(err)
	}
	if newName != appLabel {
		t.Fatalf("App rename changed persisted claim from %q to %q", appLabel, newName)
	}

	layout, err := f.repo.ReplaceProjectServiceLayout(f.ctx, f.projectOneID, f.accountID, []ProjectServiceLayoutInput{{ResourceType: "app", ResourceID: appID, X: 12, Y: 34}})
	if err != nil {
		t.Fatal(err)
	}
	if len(layout) != 1 || layout[0].ResourceType != "app" {
		t.Fatalf("App missing from service layout: %+v", layout)
	}
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); err != nil {
		t.Fatal(err)
	}
	var remainingClaim, remainingLayout int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label=$1`, appLabel).Scan(&remainingClaim); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM project_service_layouts WHERE project_id=$1 AND resource_type='app' AND resource_id=$2`, f.projectOneID, appID).Scan(&remainingLayout); err != nil {
		t.Fatal(err)
	}
	if remainingClaim != 0 || remainingLayout != 0 {
		t.Fatalf("App deletion left claim/layout rows: %d/%d", remainingClaim, remainingLayout)
	}
	reuse, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: appLabel, Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("Site could not claim label after App deletion: %v", err)
	}
	var reusedLabel string
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, uuid.MustParse(reuse.ID)).Scan(&reusedLabel); err != nil {
		t.Fatal(err)
	}
	if reusedLabel != appLabel {
		t.Fatalf("released App label was not reusable by Site: got %q want %q", reusedLabel, appLabel)
	}

	type raceResult struct {
		label string
		err   error
	}
	start := make(chan struct{})
	results := make(chan raceResult, 2)
	crossOrganizationID := uuid.Must(uuid.NewV7())
	crossProjectID := uuid.Must(uuid.NewV7())
	crossSlug := "apps-race-" + crossOrganizationID.String()[:8]
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)`, crossOrganizationID, "Apps namespace race", crossSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, crossOrganizationID, f.accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3)`, crossProjectID, crossOrganizationID, crossSlug+"-project"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = f.pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1`, crossOrganizationID)
		_, _ = f.pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, crossOrganizationID)
	})
	var writers sync.WaitGroup
	siteID := uuid.Must(uuid.NewV7())
	appRaceID := uuid.Must(uuid.NewV7())
	writers.Add(2)
	go func() {
		defer writers.Done()
		<-start
		created, createErr := f.repo.CreateSite(f.ctx, siteID, f.projectOneID, siteActor, SiteInput{Name: "racing", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
		if createErr != nil {
			results <- raceResult{err: createErr}
			return
		}
		var label string
		createErr = f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, siteID).Scan(&label)
		results <- raceResult{label: label, err: createErr}
		_ = created
	}()
	go func() {
		defer writers.Done()
		<-start
		created, createErr := f.repo.CreateApp(f.ctx, appRaceID, crossProjectID, f.actor, AppInput{Name: "racing", Enabled: true})
		if createErr != nil {
			results <- raceResult{err: createErr}
			return
		}
		var label string
		createErr = f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_apps WHERE id=$1`, appRaceID).Scan(&label)
		results <- raceResult{label: label, err: createErr}
		_ = created
	}()
	close(start)
	writers.Wait()
	close(results)
	labels := make([]string, 0, 2)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Site/App allocation failed: %v", result.err)
		}
		labels = append(labels, result.label)
	}
	if len(labels) != 2 || labels[0] == labels[1] {
		t.Fatalf("concurrent Site/App claim labels = %v, want two distinct labels", labels)
	}
	var duplicateLabels int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label IN ($1,$2)`, labels[0], labels[1]).Scan(&duplicateLabels); err != nil {
		t.Fatal(err)
	}
	if duplicateLabels != 2 {
		t.Fatalf("concurrent Site/App global claims = %d, want 2", duplicateLabels)
	}

	extraAppID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, extraAppID, f.projectTwoID, f.actor, AppInput{Name: "cascade-app", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ReplaceProjectServiceLayout(f.ctx, f.projectTwoID, f.accountID, []ProjectServiceLayoutInput{{ResourceType: "app", ResourceID: extraAppID, X: 3, Y: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteProject(f.ctx, f.projectTwoID, f.accountID, f.projectTwoName); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM project_apps WHERE project_id=$1`,
		`SELECT count(*) FROM platform_hostname_claims WHERE project_id=$1`,
		`SELECT count(*) FROM project_service_layouts WHERE project_id=$1`,
	} {
		var count int
		if err := f.pool.QueryRow(f.ctx, query, f.projectTwoID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("project deletion left %d rows for query %q", count, query)
		}
	}

	releaseSite, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: "release-claim", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseLabel string
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, uuid.MustParse(releaseSite.ID)).Scan(&releaseLabel); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DeleteSite(f.ctx, f.projectOneID, uuid.MustParse(releaseSite.ID), siteActor); err != nil {
		t.Fatal(err)
	}
	recreated, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: "release-claim", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var recreatedLabel string
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, uuid.MustParse(recreated.ID)).Scan(&recreatedLabel); err != nil {
		t.Fatal(err)
	}
	if recreatedLabel != releaseLabel {
		t.Fatalf("Site delete did not release stable platform claim: old=%q new=%q", releaseLabel, recreatedLabel)
	}
	if len(siteRoutesAfter) == 0 {
		t.Fatal("route snapshot test did not include the created static Site")
	}
}

func bytesOfZeroes(length int) []byte { return make([]byte, length) }

func bytesOfOnes(length int) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = 1
	}
	return result
}

func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }

func jsonUnmarshal(value []byte, target any) error { return json.Unmarshal(value, target) }
