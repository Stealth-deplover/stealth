package repository

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appsecret"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

func TestAppRollbackRestoresWorkloadAndPreservesEnvironmentIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "rollback-main", Enabled: true, ArtifactQuotaBytes: 8192}); err != nil {
		t.Fatal(err)
	}

	first := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	completeAppDeploymentForTest(t, f, appID, first.ID, "rollback-build-one")
	changedWorkload := workloadspec.Default()
	changedWorkload.Resources.CPUMillis = 1200
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &changedWorkload}); err != nil {
		t.Fatal(err)
	}
	second := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	completeAppDeploymentForTest(t, f, appID, second.ID, "rollback-build-two")
	if _, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(second.ID), f.actor); err != nil {
		t.Fatal(err)
	}

	cipher, err := appsecret.New([]byte(strings.Repeat("k", appsecret.KeySize)))
	if err != nil {
		t.Fatal(err)
	}
	currentSecret := "CURRENT-APP-SECRET-MUST-STAY"
	variableID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, variableID, f.projectOneID, appID, f.actor, AppEnvironmentVariableInput{
		Key: "PROVIDER_TOKEN", IsSecret: true, Value: &currentSecret, Cipher: cipher,
	}); err != nil {
		t.Fatal(err)
	}
	var ciphertextBefore []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT value_ciphertext FROM app_environment_variables WHERE id=$1`, variableID).Scan(&ciphertextBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Enabled: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	var quotaBefore, usedBefore, reservedBefore int64
	if err := f.pool.QueryRow(f.ctx, `SELECT artifact_quota_bytes,artifact_used_bytes,artifact_reserved_bytes FROM project_apps WHERE id=$1`, appID).Scan(&quotaBefore, &usedBefore, &reservedBefore); err != nil {
		t.Fatal(err)
	}
	if before.DesiredDeploymentID == nil || *before.DesiredDeploymentID != second.ID {
		t.Fatalf("fixture desired release = %+v", before.DesiredDeploymentID)
	}
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE project_apps SET observed_generation=$2,runtime_status='failed',runtime_error='safe prior failure'
		WHERE project_id=$1 AND id=$3`, f.projectOneID, before.DesiredGeneration, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO app_runtime_state (app_id,project_id,applied_deployment_id,applied_generation,failure_count,next_retry_at,last_failure_at)
		VALUES ($1,$2,$3,$4,5,now()+interval '1 hour',now())
		ON CONFLICT (app_id) DO UPDATE SET applied_deployment_id=EXCLUDED.applied_deployment_id,
		  applied_generation=EXCLUDED.applied_generation,failure_count=EXCLUDED.failure_count,
		  next_retry_at=EXCLUDED.next_retry_at,last_failure_at=EXCLUDED.last_failure_at`,
		appID, f.projectOneID, uuid.MustParse(second.ID), before.DesiredGeneration); err != nil {
		t.Fatal(err)
	}
	firstBefore, err := f.repo.GetAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	firstBuildLogsBefore := appDeploymentBuildLogSnapshotForTest(t, f, first.ID)
	secondBefore, err := f.repo.GetAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(second.ID), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	secondBuildLogsBefore := appDeploymentBuildLogSnapshotForTest(t, f, second.ID)

	result, err := f.repo.RollbackAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	wantWorkload := firstBefore.WorkloadSnapshot
	wantDigest, err := workloadspec.Digest(wantWorkload)
	if err != nil {
		t.Fatal(err)
	}
	if result.App.DesiredDeploymentID == nil || *result.App.DesiredDeploymentID != first.ID ||
		result.App.DesiredGeneration != before.DesiredGeneration+1 || result.App.ObservedGeneration != before.DesiredGeneration ||
		result.App.RuntimeStatus != "pending" || result.App.RuntimeError != nil ||
		!workloadspec.Equal(result.App.Workload, wantWorkload) || result.App.WorkloadSpecSHA256 != wantDigest ||
		!result.Deployment.Selected || result.Deployment.ID != first.ID || result.App.Enabled != before.Enabled ||
		result.App.Name != before.Name || !optionalStringEqual(result.App.PlatformHostname, before.PlatformHostname) {
		t.Fatalf("rollback did not restore only desired release/workload state: app=%+v deployment=%+v", result.App, result.Deployment)
	}
	var appliedID uuid.UUID
	var appliedGeneration int64
	var failureCount int
	var nextRetryAt *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT applied_deployment_id,applied_generation,failure_count,next_retry_at FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&appliedID, &appliedGeneration, &failureCount, &nextRetryAt); err != nil {
		t.Fatal(err)
	}
	if appliedID != uuid.MustParse(second.ID) || appliedGeneration != before.DesiredGeneration || failureCount != 0 || nextRetryAt != nil {
		t.Fatalf("rollback changed worker-owned apply state or failed to reset retry state: applied=%s generation=%d failures=%d retry=%v", appliedID, appliedGeneration, failureCount, nextRetryAt)
	}
	var ciphertextAfter []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT value_ciphertext FROM app_environment_variables WHERE id=$1`, variableID).Scan(&ciphertextAfter); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ciphertextBefore, ciphertextAfter) {
		t.Fatal("rollback changed current encrypted environment value")
	}
	var quotaAfter, usedAfter, reservedAfter int64
	if err := f.pool.QueryRow(f.ctx, `SELECT artifact_quota_bytes,artifact_used_bytes,artifact_reserved_bytes FROM project_apps WHERE id=$1`, appID).Scan(&quotaAfter, &usedAfter, &reservedAfter); err != nil {
		t.Fatal(err)
	}
	if quotaAfter != quotaBefore || usedAfter != usedBefore || reservedAfter != reservedBefore {
		t.Fatalf("rollback changed App artifact quota accounting: before=%d/%d/%d after=%d/%d/%d", quotaBefore, usedBefore, reservedBefore, quotaAfter, usedAfter, reservedAfter)
	}
	firstAfter, err := f.repo.GetAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	secondAfter, err := f.repo.GetAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(second.ID), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if firstAfter.WorkloadSpecSHA256 != firstBefore.WorkloadSpecSHA256 || firstAfter.ImageDigest == nil || *firstAfter.ImageDigest != *firstBefore.ImageDigest ||
		secondAfter.WorkloadSpecSHA256 != secondBefore.WorkloadSpecSHA256 || secondAfter.ImageDigest == nil || *secondAfter.ImageDigest != *secondBefore.ImageDigest {
		t.Fatal("rollback mutated immutable deployment history")
	}
	if appDeploymentBuildLogSnapshotForTest(t, f, first.ID) != firstBuildLogsBefore || appDeploymentBuildLogSnapshotForTest(t, f, second.ID) != secondBuildLogsBefore {
		t.Fatal("rollback mutated immutable deployment build logs")
	}
	var auditMetadata string
	if err := f.pool.QueryRow(f.ctx, `SELECT metadata::text FROM audit_events WHERE target_id=$1 AND action='app_deployment.rollback' ORDER BY created_at DESC LIMIT 1`, first.ID).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{currentSecret, "ciphertext", "image_path", "container_id", "lease_token"} {
		if strings.Contains(auditMetadata, forbidden) {
			t.Fatalf("rollback audit metadata contains forbidden value %q: %s", forbidden, auditMetadata)
		}
	}
	for _, required := range []string{first.ID, second.ID, firstBefore.WorkloadSpecSHA256, secondBefore.WorkloadSpecSHA256} {
		if !strings.Contains(auditMetadata, required) {
			t.Fatalf("rollback audit metadata omitted %q: %s", required, auditMetadata)
		}
	}
}

func TestAppRollbackRejectionsPreserveDesiredStateIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "rollback-reject", Enabled: true, ArtifactQuotaBytes: 8192}); err != nil {
		t.Fatal(err)
	}
	initial, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.RollbackAppDeployment(f.ctx, f.projectOneID, appID, uuid.Must(uuid.NewV7()), f.actor); !errors.Is(err, ErrAppRollbackNotAvailable) {
		t.Fatalf("rollback without a selected release = %v", err)
	}
	initialAfter, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil || initialAfter.DesiredGeneration != initial.DesiredGeneration || initialAfter.DesiredDeploymentID != nil || initialAfter.WorkloadSpecSHA256 != initial.WorkloadSpecSHA256 {
		t.Fatalf("rollback without a selected release mutated desired state: app=%+v err=%v", initialAfter, err)
	}

	first := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	completeAppDeploymentForTest(t, f, appID, first.ID, "rollback-reject-build-one")
	if _, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.RollbackAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor); !errors.Is(err, ErrAppDeploymentAlreadySelected) {
		t.Fatalf("rollback to current release = %v", err)
	}

	second := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	secondJob, err := f.repo.ClaimNextAppDeployment(f.ctx, "rollback-reject-build-two")
	if err != nil || secondJob.Deployment.ID != second.ID {
		t.Fatalf("claim older rollback target = %+v err=%v", secondJob, err)
	}
	third := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
	completeAppDeploymentForTest(t, f, appID, third.ID, "rollback-reject-build-three")
	if _, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(third.ID), f.actor); err != nil {
		t.Fatal(err)
	}
	assertRejected := func(targetID uuid.UUID, want error) {
		t.Helper()
		before, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		var failuresBefore int
		var retryBefore *time.Time
		if err := f.pool.QueryRow(f.ctx, `SELECT failure_count,next_retry_at FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&failuresBefore, &retryBefore); err != nil {
			t.Fatal(err)
		}
		if _, err := f.repo.RollbackAppDeployment(f.ctx, f.projectOneID, appID, targetID, f.actor); !errors.Is(err, want) {
			t.Fatalf("rollback to %s = %v, want %v", targetID, err, want)
		}
		after, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		var failuresAfter int
		var retryAfter *time.Time
		if err := f.pool.QueryRow(f.ctx, `SELECT failure_count,next_retry_at FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&failuresAfter, &retryAfter); err != nil {
			t.Fatal(err)
		}
		retryUnchanged := retryBefore == nil && retryAfter == nil || retryBefore != nil && retryAfter != nil && retryBefore.Equal(*retryAfter)
		if before.DesiredGeneration != after.DesiredGeneration || !optionalStringEqual(before.DesiredDeploymentID, after.DesiredDeploymentID) ||
			before.WorkloadSpecSHA256 != after.WorkloadSpecSHA256 || before.Enabled != after.Enabled || failuresBefore != failuresAfter || !retryUnchanged {
			t.Fatalf("rejected rollback mutated desired or retry state: before=%+v after=%+v retry=%d/%v -> %d/%v", before, after, failuresBefore, retryBefore, failuresAfter, retryAfter)
		}
	}

	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET failure_count=3,next_retry_at=now()+interval '1 hour',last_failure_at=now() WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	assertRejected(uuid.MustParse(second.ID), ErrAppDeploymentNotReady) // running build
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_deployments SET status='queued',build_status='queued',build_worker_id=NULL,build_started_at=NULL WHERE id=$1`, uuid.MustParse(second.ID)); err != nil {
		t.Fatal(err)
	}
	assertRejected(uuid.MustParse(second.ID), ErrAppDeploymentNotReady) // queued build
	secondJob, err = f.repo.ClaimNextAppDeployment(f.ctx, "rollback-reject-build-two")
	if err != nil || secondJob.Deployment.ID != second.ID {
		t.Fatalf("reclaim older rollback target = %+v err=%v", secondJob, err)
	}
	if _, err := f.repo.FailAppDeploymentBuild(f.ctx, f.projectOneID, appID, uuid.MustParse(second.ID), "rollback-reject-build-two", "expected failure fixture"); err != nil {
		t.Fatal(err)
	}
	assertRejected(uuid.MustParse(second.ID), ErrAppDeploymentNotReady) // failed build
	assertRejected(uuid.MustParse(third.ID), ErrAppDeploymentAlreadySelected)

	if _, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(first.ID), f.actor); err != nil {
		t.Fatal(err)
	}
	assertRejected(uuid.MustParse(third.ID), ErrAppRollbackNotAvailable) // newer than desired
	assertRejected(uuid.MustParse(first.ID), ErrAppDeploymentAlreadySelected)
	if _, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(third.ID), f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE project_apps SET desired_generation=$2 WHERE id=$1`, appID, int64(^uint64(0)>>1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET failure_count=3,next_retry_at=now()+interval '1 hour' WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	assertRejected(uuid.MustParse(first.ID), ErrAppRollbackGenerationLimit)

	var rollbackAuditCount int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE target_id IN ($1,$2,$3) AND action='app_deployment.rollback'`, first.ID, second.ID, third.ID).Scan(&rollbackAuditCount); err != nil {
		t.Fatal(err)
	}
	if rollbackAuditCount != 0 {
		t.Fatalf("rejected rollbacks wrote %d success audit rows", rollbackAuditCount)
	}

	pathAppID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, pathAppID, f.projectOneID, f.actor, AppInput{Name: "rollback-path", Enabled: true, ArtifactQuotaBytes: 8192}); err != nil {
		t.Fatal(err)
	}
	pathTarget := createQueuedAppDeploymentForTest(t, f, f.projectOneID, pathAppID, false)
	wrongPath := f.projectOneID.String() + "/" + pathAppID.String() + "/" + uuid.Must(uuid.NewV7()).String()
	completeAppDeploymentForTestAtPath(t, f, pathAppID, pathTarget.ID, "rollback-path-build-one", wrongPath)
	pathCurrent := createQueuedAppDeploymentForTest(t, f, f.projectOneID, pathAppID, false)
	completeAppDeploymentForTest(t, f, pathAppID, pathCurrent.ID, "rollback-path-build-two")
	if _, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, pathAppID, uuid.MustParse(pathCurrent.ID), f.actor); err != nil {
		t.Fatal(err)
	}
	pathBefore, err := f.repo.GetApp(f.ctx, f.projectOneID, pathAppID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.RollbackAppDeployment(f.ctx, f.projectOneID, pathAppID, uuid.MustParse(pathTarget.ID), f.actor); !errors.Is(err, ErrAppRollbackNotAvailable) {
		t.Fatalf("rollback to deployment with a mismatched private artifact path = %v", err)
	}
	pathAfter, err := f.repo.GetApp(f.ctx, f.projectOneID, pathAppID, f.actor)
	if err != nil || pathAfter.DesiredGeneration != pathBefore.DesiredGeneration || !optionalStringEqual(pathAfter.DesiredDeploymentID, pathBefore.DesiredDeploymentID) || pathAfter.WorkloadSpecSHA256 != pathBefore.WorkloadSpecSHA256 {
		t.Fatalf("rejected malformed artifact locator rollback changed desired state: before=%+v after=%+v err=%v", pathBefore, pathAfter, err)
	}
	var pathRollbackAuditCount int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='app_deployment.rollback'`, pathTarget.ID).Scan(&pathRollbackAuditCount); err != nil {
		t.Fatal(err)
	}
	if pathRollbackAuditCount != 0 {
		t.Fatalf("rejected malformed artifact locator wrote %d rollback audit rows", pathRollbackAuditCount)
	}
}

func TestAppRollbackConcurrentRequestsSerializeIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "rollback-race", Enabled: true, ArtifactQuotaBytes: 8192}); err != nil {
		t.Fatal(err)
	}
	var deploymentIDs []string
	for index, workerID := range []string{"rollback-race-build-one", "rollback-race-build-two", "rollback-race-build-three"} {
		deployment := createQueuedAppDeploymentForTest(t, f, f.projectOneID, appID, false)
		completeAppDeploymentForTest(t, f, appID, deployment.ID, workerID)
		deploymentIDs = append(deploymentIDs, deployment.ID)
		if index == 0 {
			continue
		}
		workload, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		workload.Workload.Resources.CPUMillis += 50
		if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &workload.Workload}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.repo.SelectAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(deploymentIDs[2]), f.actor); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan struct {
		id  string
		err error
	}, 2)
	var workers sync.WaitGroup
	for _, id := range deploymentIDs[:2] {
		workers.Add(1)
		go func(deploymentID string) {
			defer workers.Done()
			<-start
			_, rollbackErr := f.repo.RollbackAppDeployment(f.ctx, f.projectOneID, appID, uuid.MustParse(deploymentID), f.actor)
			results <- struct {
				id  string
				err error
			}{id: deploymentID, err: rollbackErr}
		}(id)
	}
	close(start)
	workers.Wait()
	close(results)
	successes := make(map[string]bool)
	for result := range results {
		if result.err == nil {
			successes[result.id] = true
		} else if !errors.Is(result.err, ErrAppRollbackNotAvailable) {
			t.Fatalf("concurrent rollback to %s returned unexpected error: %v", result.id, result.err)
		}
	}
	if len(successes) == 0 {
		t.Fatal("both concurrent rollbacks were rejected")
	}
	app, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if app.DesiredGeneration != before.DesiredGeneration+int64(len(successes)) || app.DesiredDeploymentID == nil || !successes[*app.DesiredDeploymentID] {
		t.Fatalf("concurrent rollback lost an update or failed to re-evaluate desired state: successes=%v before=%+v after=%+v", successes, before, app)
	}
}

func TestAppRollbackFencesStaleRuntimeWorkerIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "rollback-fence", Enabled: true, ArtifactQuotaBytes: 8192}); err != nil {
		t.Fatal(err)
	}
	firstID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	staleJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "rollback-stale-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secondID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	if _, err := f.repo.RollbackAppDeployment(f.ctx, f.projectOneID, appID, firstID, f.actor); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, staleJob, "running", runtimeRepositoryContainer(staleJob, firstID)); !errors.Is(err, ErrAppRuntimeStale) {
		t.Fatalf("pre-rollback worker completion = %v, want ErrAppRuntimeStale", err)
	}
	current, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if current.DesiredDeploymentID == nil || *current.DesiredDeploymentID != firstID.String() ||
		current.DesiredGeneration != staleJob.App.DesiredGeneration+2 || current.ObservedGeneration != 0 || current.RuntimeStatus != "pending" {
		t.Fatalf("stale worker changed post-rollback desired state: old=%+v current=%+v newer=%s", staleJob.App, current, secondID)
	}
}

func TestAppDiagnosticsProjectionStatesIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "diagnostics-state", Enabled: true, ArtifactQuotaBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	diagnostics, err := f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.ConvergenceStatus != "not_deployed" || diagnostics.DesiredDeployment != nil || diagnostics.AppliedDeployment != nil || !hasAppDiagnosticIssue(diagnostics.Issues, "not_deployed") {
		t.Fatalf("fresh App diagnostics = %+v", diagnostics)
	}
	deploymentID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	diagnostics, err = f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.ConvergenceStatus != "reconciling" || diagnostics.DesiredDeployment == nil || diagnostics.DesiredDeployment.Version != 1 ||
		diagnostics.AppliedDeployment != nil || diagnostics.DesiredGeneration <= diagnostics.ObservedGeneration || !diagnostics.DesiredArtifactReady ||
		!hasAppDiagnosticIssue(diagnostics.Issues, "generation_pending") {
		t.Fatalf("selected App diagnostics = %+v", diagnostics)
	}
	runtimeJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "diagnostics-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, runtimeJob, "running", runtimeRepositoryContainer(runtimeJob, deploymentID)); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.HealthStatus != "pending" || !hasAppDiagnosticIssue(diagnostics.Issues, "health_pending") {
		t.Fatalf("running App without fresh health diagnostics = %+v", diagnostics)
	}
	forceAppHealthCheckDue(t, f, appID)
	healthJob, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "diagnostics-health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, healthJob, false); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.HealthStatus != "unhealthy" || diagnostics.ConvergenceStatus != "degraded" || !hasAppDiagnosticIssue(diagnostics.Issues, "health_unhealthy") {
		t.Fatalf("unhealthy App diagnostics = %+v", diagnostics)
	}
	forceAppHealthCheckDue(t, f, appID)
	healthJob, err = f.repo.ClaimNextAppHealthCheck(f.ctx, "diagnostics-health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, healthJob, true); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.ConvergenceStatus != "converged" || diagnostics.DesiredDeployment == nil || diagnostics.AppliedDeployment == nil ||
		diagnostics.DesiredDeployment.ID != diagnostics.AppliedDeployment.ID || diagnostics.AppliedGeneration == nil ||
		*diagnostics.AppliedGeneration != diagnostics.DesiredGeneration || diagnostics.HealthStatus != "healthy" {
		t.Fatalf("healthy diagnostics did not converge with applied release: %+v", diagnostics)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE project_apps SET runtime_status='failed',runtime_error='safe runtime error' WHERE id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET failure_count=2,next_retry_at=now()+interval '1 minute',last_failure_at=now() WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.ConvergenceStatus != "failed" || diagnostics.RuntimeError == nil || *diagnostics.RuntimeError != "safe runtime error" ||
		diagnostics.FailureCount != 2 || diagnostics.NextRetryAt == nil || diagnostics.LastFailureAt == nil ||
		!hasAppDiagnosticIssue(diagnostics.Issues, "runtime_failed") || !hasAppDiagnosticIssue(diagnostics.Issues, "retry_scheduled") {
		t.Fatalf("failed/retry diagnostics = %+v", diagnostics)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE project_apps SET runtime_status='degraded' WHERE id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.ConvergenceStatus != "degraded" || !hasAppDiagnosticIssue(diagnostics.Issues, "runtime_degraded") {
		t.Fatalf("degraded diagnostics = %+v", diagnostics)
	}
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Enabled: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	stopJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "diagnostics-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, stopJob, "stopped", nil); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = f.repo.GetAppDiagnostics(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.ConvergenceStatus != "stopped" || !hasAppDiagnosticIssue(diagnostics.Issues, "disabled") {
		t.Fatalf("disabled/stopped diagnostics = %+v", diagnostics)
	}
}

func hasAppDiagnosticIssue(issues []domain.AppDiagnosticIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func appDeploymentBuildLogSnapshotForTest(t *testing.T, f appRepositoryFixture, deploymentID string) string {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, `SELECT sequence,level,message,created_at FROM app_build_logs WHERE deployment_id=$1 ORDER BY sequence`, uuid.MustParse(deploymentID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries []string
	for rows.Next() {
		var sequence int64
		var level, message string
		var createdAt time.Time
		if err := rows.Scan(&sequence, &level, &message, &createdAt); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, fmt.Sprintf("%d|%s|%s|%s", sequence, level, message, createdAt.UTC().Format(time.RFC3339Nano)))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(entries, "\n")
}
