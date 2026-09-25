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
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
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
	assertAppRuntimeLogSources(t, f, appID)
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
	assertAppRuntimeLogSources(t, f, appID)
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

	expectedCleanupName := currentAppRuntimeContainerName(t, f, appID)
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeCleanupContainerID(t, f, appID)
	prioritizeAppRuntimeCleanupForTest(t, f, appID)
	cleanup, err := f.repo.ClaimNextAppRuntimeCleanup(f.ctx, "cleanup-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.AppID != appID || cleanup.ContainerID == nil || *cleanup.ContainerID != strings.Repeat("a", 64) || cleanup.ContainerName != expectedCleanupName {
		var persistedContainerID string
		if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(container_id,'') FROM app_runtime_cleanup_jobs WHERE id=$1`, cleanup.ID).Scan(&persistedContainerID); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("App deletion cleanup claim disagrees with its row: expected_app_id=%s job=%+v persisted_container_id=%q", appID, cleanup, persistedContainerID)
	}
	if err := f.repo.CompleteAppRuntimeCleanup(f.ctx, cleanup); err != nil {
		t.Fatal(err)
	}
	var completed bool
	if err := f.pool.QueryRow(f.ctx, `
		SELECT EXISTS (
			SELECT 1 FROM app_runtime_cleanup_jobs
			WHERE id=$1 AND status='completed' AND completed_at IS NOT NULL
		)`, cleanup.ID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("completed App cleanup row did not retain its completion state")
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
	prioritizeAppRuntimeCleanupForTest(t, f, appID)
	cleanup, err := f.repo.ClaimNextAppRuntimeCleanup(f.ctx, "project-cleanup-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.ProjectID == nil || *cleanup.ProjectID != f.projectTwoID || cleanup.AppID != appID || cleanup.ContainerID == nil || *cleanup.ContainerID != strings.Repeat("a", 64) {
		var persistedContainerID string
		if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(container_id,'') FROM app_runtime_cleanup_jobs WHERE id=$1`, cleanup.ID).Scan(&persistedContainerID); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("project deletion cleanup claim disagrees with its row: expected_app_id=%s job=%+v persisted_container_id=%q", appID, cleanup, persistedContainerID)
	}
	if err := f.repo.CompleteAppRuntimeCleanup(f.ctx, cleanup); err != nil {
		t.Fatal(err)
	}
}

func TestAppHealthConvergenceFencesStaleProbesAndControlsRouteSnapshotIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	var previousDomain string
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(workload_base_domain,'') FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE instance_domain_settings SET workload_base_domain='health-integration.example.test',updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if previousDomain == "" {
			_, _ = f.pool.Exec(context.Background(), `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`)
		} else {
			_, _ = f.pool.Exec(context.Background(), `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, previousDomain)
		}
	})

	workload := workloadspec.Default()
	initialDelay := 60
	workload.HealthCheck.InitialDelaySeconds = &initialDelay
	appID := uuid.Must(uuid.NewV7())
	app, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "health-convergence", Enabled: true, Workload: workload})
	if err != nil {
		t.Fatal(err)
	}
	if app.HealthStatus != "pending" || app.RouteStatus != "not_available" {
		t.Fatalf("new App health/route default = %s/%s", app.HealthStatus, app.RouteStatus)
	}
	deploymentID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	runtimeJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "health-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, runtimeJob, "running", runtimeRepositoryContainer(runtimeJob, deploymentID)); err != nil {
		t.Fatal(err)
	}
	var delayHonored bool
	if err := f.pool.QueryRow(f.ctx, `SELECT next_health_check_at > now() + interval '50 seconds' FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&delayHonored); err != nil {
		t.Fatal(err)
	}
	if !delayHonored {
		t.Fatal("configured initial health delay was not persisted")
	}
	starting, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if starting.RuntimeStatus != "running" || starting.HealthStatus != "pending" || starting.RouteStatus != "waiting_for_health" {
		t.Fatalf("running App became routable before health converged: %+v", starting)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("pending App appeared in route snapshot: routes=%+v err=%v", routes, err)
	}

	forceAppHealthCheckDue(t, f, appID)
	job, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, job, true); err != nil {
		t.Fatal(err)
	}
	healthy, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if healthy.HealthStatus != "healthy" || healthy.RouteStatus != "active" || healthy.PlatformHostname == nil {
		t.Fatalf("healthy current App did not become route eligible: %+v", healthy)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || !appRouteExists(routes, appID) {
		t.Fatalf("healthy App is absent from route snapshot: routes=%+v err=%v", routes, err)
	}
	var currentRouteIdentity, healthRouteIdentity uuid.UUID
	var currentContainerName string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT route_identity,health_route_identity,container_name
		FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&currentRouteIdentity, &healthRouteIdentity, &currentContainerName); err != nil {
		t.Fatal(err)
	}
	if currentRouteIdentity == uuid.Nil || healthRouteIdentity != currentRouteIdentity || currentContainerName != AppRuntimeContainerNameForIncarnation(appID, currentRouteIdentity) {
		t.Fatalf("healthy runtime routing identity = route:%s health:%s name:%q", currentRouteIdentity, healthRouteIdentity, currentContainerName)
	}
	wrongRouteIdentity, err := NewAppRuntimeRouteIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		query string
		args  []any
	}{
		{name: "healthy result without health route identity", query: `UPDATE app_runtime_state SET health_route_identity=NULL WHERE app_id=$1`, args: []any{appID}},
		{name: "unhealthy result without health route identity", query: `UPDATE app_runtime_state SET health_status='unhealthy',health_route_identity=NULL WHERE app_id=$1`, args: []any{appID}},
		{name: "health result for a stale route identity", query: `UPDATE app_runtime_state SET health_route_identity=$2 WHERE app_id=$1`, args: []any{appID, wrongRouteIdentity}},
		{name: "route identity rotated without rebinding health", query: `UPDATE app_runtime_state SET route_identity=$2,container_name=$3 WHERE app_id=$1`, args: []any{appID, wrongRouteIdentity, AppRuntimeContainerNameForIncarnation(appID, wrongRouteIdentity)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := f.pool.Exec(f.ctx, test.query, test.args...); err == nil {
				t.Fatal("database accepted a health result that is not bound to the current route identity")
			}
		})
	}
	stillHealthy, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if stillHealthy.RouteStatus != "active" {
		t.Fatalf("rejected stale identity writes changed the App projection: route_status=%s", stillHealthy.RouteStatus)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || !appRouteExists(routes, appID) {
		t.Fatalf("current healthy App projection disagrees with ingress after rejected stale identity writes: routes=%+v err=%v", routes, err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE project_apps SET workload_spec=jsonb_set(workload_spec,'{port}','"malformed"'::jsonb) WHERE id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("malformed App port was published or blocked a valid snapshot: routes=%+v err=%v", routes, err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE project_apps SET workload_spec=jsonb_set(workload_spec,'{port}','8080'::jsonb) WHERE id=$1`, appID); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= workload.HealthCheck.FailureThreshold; attempt++ {
		forceAppHealthCheckDue(t, f, appID)
		job, err = f.repo.ClaimNextAppHealthCheck(f.ctx, "health-worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.repo.CompleteAppHealthCheck(f.ctx, job, false); err != nil {
			t.Fatal(err)
		}
		current, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		if attempt < workload.HealthCheck.FailureThreshold {
			if current.HealthStatus != "healthy" || current.RouteStatus != "active" {
				t.Fatalf("route flapped before failure threshold %d: %+v", workload.HealthCheck.FailureThreshold, current)
			}
		} else if current.HealthStatus != "unhealthy" || current.RouteStatus != "waiting_for_health" {
			t.Fatalf("failure threshold did not withdraw route: %+v", current)
		}
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("unhealthy App remained in route snapshot: routes=%+v err=%v", routes, err)
	}

	// Hold a valid old-generation lease, then write a new desired generation.
	// The old success must not restore health or route eligibility.
	forceAppHealthCheckDue(t, f, appID)
	staleJob, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	changedWorkload := workload
	changedWorkload.Port++
	changed, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &changedWorkload})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, staleJob, true); !errors.Is(err, ErrAppRuntimeStale) {
		t.Fatalf("old generation health completion = %v, want stale", err)
	}
	current, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if current.DesiredGeneration != changed.DesiredGeneration || current.HealthStatus != "pending" || current.RouteStatus != "waiting_for_runtime" {
		t.Fatalf("stale health result authorized newer generation: %+v", current)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("new desired generation inherited old route: routes=%+v err=%v", routes, err)
	}

	newRuntimeJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "health-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppRuntime(f.ctx, newRuntimeJob, "running", runtimeRepositoryContainer(newRuntimeJob, deploymentID)); err != nil {
		t.Fatal(err)
	}
	forceAppHealthCheckDue(t, f, appID)
	newHealthJob, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, newHealthJob, true); err != nil {
		t.Fatal(err)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || !appRouteExists(routes, appID) {
		t.Fatalf("recovered current generation is absent from route snapshot: routes=%+v err=%v", routes, err)
	}
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Enabled: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.RouteStatus != "not_available" {
		t.Fatalf("disabled App remained route eligible: %+v", disabled)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("disabled App remained in route snapshot: routes=%+v err=%v", routes, err)
	}
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); err != nil {
		t.Fatal(err)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("deleted App remained in route snapshot: routes=%+v err=%v", routes, err)
	}
}

func TestAppSameContainerRestartRequiresFreshHealthBeforeRoutingIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	var previousDomain string
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(workload_base_domain,'') FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE instance_domain_settings SET workload_base_domain='restart-health.example.test',updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if previousDomain == "" {
			_, _ = f.pool.Exec(context.Background(), `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`)
		} else {
			_, _ = f.pool.Exec(context.Background(), `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, previousDomain)
		}
	})

	workload := workloadspec.Default()
	initialDelay := 60
	workload.HealthCheck.InitialDelaySeconds = &initialDelay
	appID := uuid.Must(uuid.NewV7())
	_, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "same-container-restart", Enabled: true, Workload: workload})
	if err != nil {
		t.Fatal(err)
	}
	deploymentID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	initialRuntime, err := f.repo.ClaimNextAppRuntime(f.ctx, "restart-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	container := runtimeRepositoryContainer(initialRuntime, deploymentID)
	if err := f.repo.CompleteAppRuntime(f.ctx, initialRuntime, "running", container); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeLogSources(t, f, appID, strings.Repeat("a", 64))
	if exists, err := f.repo.AppRuntimeContainerExists(f.ctx, f.projectOneID, appID, container.ID); err != nil || !exists {
		t.Fatalf("startup orphan sweep did not preserve the exact current container: exists=%v err=%v", exists, err)
	}
	if exists, err := f.repo.AppRuntimeContainerExists(f.ctx, f.projectOneID, appID, strings.Repeat("f", 64)); err != nil || exists {
		t.Fatalf("startup orphan sweep treated a duplicate App container as current: exists=%v err=%v", exists, err)
	}
	forceAppHealthCheckDue(t, f, appID)
	initialProbe, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "restart-health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, initialProbe, true); err != nil {
		t.Fatal(err)
	}
	healthy, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if healthy.RuntimeStatus != "running" || healthy.HealthStatus != "healthy" || healthy.RouteStatus != "active" {
		t.Fatalf("pre-restart App state = runtime %s health %s route %s, want running/healthy/active", healthy.RuntimeStatus, healthy.HealthStatus, healthy.RouteStatus)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || !appRouteExists(routes, appID) {
		t.Fatalf("healthy App was not routed before restart: routes=%+v err=%v", routes, err)
	}

	// A health worker is in flight when the process exits. Expire its lease and
	// reclaim the same App runtime job, as happens after a worker handoff.
	forceAppHealthCheckDue(t, f, appID)
	staleProbe, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "restart-health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET lease_expires_at=now()-interval '1 second' WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.RequeueStaleAppRuntimeLeases(f.ctx); err != nil {
		t.Fatal(err)
	}
	restartJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "restart-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oldRouteTarget := restartJob.ContainerName
	newRouteIdentity, err := NewAppRuntimeRouteIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ResetAppHealthBeforeRuntimeRestart(f.ctx, restartJob, newRouteIdentity); err != nil {
		t.Fatal(err)
	}
	restartJob.RouteIdentity = newRouteIdentity
	restartJob.ContainerName = AppRuntimeContainerNameForIncarnation(appID, newRouteIdentity)
	container.Name = restartJob.ContainerName
	if restartJob.ContainerName == oldRouteTarget {
		t.Fatal("same-container process restart reused its previous route target")
	}

	pending, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if pending.RuntimeStatus != "running" || pending.HealthStatus != "pending" || pending.RouteStatus != "waiting_for_health" {
		t.Fatalf("same-container restart did not enter pending/unroutable state: runtime=%s health=%s route=%s", pending.RuntimeStatus, pending.HealthStatus, pending.RouteStatus)
	}
	var rotatedRouteIdentity uuid.UUID
	var rotatedHealthRouteIdentity *uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT route_identity,health_route_identity FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&rotatedRouteIdentity, &rotatedHealthRouteIdentity); err != nil {
		t.Fatal(err)
	}
	if rotatedRouteIdentity != newRouteIdentity || rotatedHealthRouteIdentity != nil {
		t.Fatalf("pending restart identity = route:%s health:%v, want rotated route with no health identity", rotatedRouteIdentity, rotatedHealthRouteIdentity)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("App projection and ingress disagreed while health identity was pending: routes=%+v err=%v", routes, err)
	}
	if oldRouteTarget == restartJob.ContainerName {
		t.Fatalf("old target %q became current again during restart", oldRouteTarget)
	}
	var delayHonored bool
	var healthStatus string
	var healthFailures int
	var healthCheckedAt, healthGeneration, healthDeploymentID, healthContainerID, containerAddress *string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT health_status,health_failure_count,health_checked_at::text,health_generation::text,
		       health_deployment_id::text,health_container_id,host(container_address),
		       next_health_check_at > now() + interval '50 seconds'
		FROM app_runtime_state WHERE app_id=$1`, appID).Scan(
		&healthStatus, &healthFailures, &healthCheckedAt, &healthGeneration,
		&healthDeploymentID, &healthContainerID, &containerAddress, &delayHonored,
	); err != nil {
		t.Fatal(err)
	}
	if healthStatus != "pending" || healthFailures != 0 || healthCheckedAt != nil || healthGeneration != nil || healthDeploymentID != nil || healthContainerID != nil || containerAddress != nil || !delayHonored {
		t.Fatalf("restart reset did not clear old process health identity: status=%s failures=%d checked=%v generation=%v deployment=%v container=%v address=%v delay=%v", healthStatus, healthFailures, healthCheckedAt, healthGeneration, healthDeploymentID, healthContainerID, containerAddress, delayHonored)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, staleProbe, true); !errors.Is(err, ErrAppRuntimeLeaseLost) {
		t.Fatalf("pre-restart health completion = %v, want lost lease", err)
	}
	stillPending, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if stillPending.HealthStatus != "pending" || stillPending.RouteStatus != "waiting_for_health" {
		t.Fatalf("stale pre-restart probe restored health: health=%s route=%s", stillPending.HealthStatus, stillPending.RouteStatus)
	}

	// Docker starts the same container ID and the repository rebinds its
	// inspected runtime identity while preserving the fresh initial delay.
	if err := f.repo.CompleteAppRuntime(f.ctx, restartJob, "running", container); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeLogSources(t, f, appID, strings.Repeat("a", 64))
	started, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if started.RuntimeStatus != "running" || started.HealthStatus != "pending" || started.RouteStatus != "waiting_for_health" {
		t.Fatalf("restarted same container became routable without a fresh probe: runtime=%s health=%s route=%s", started.RuntimeStatus, started.HealthStatus, started.RouteStatus)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || appRouteExists(routes, appID) {
		t.Fatalf("same-container restart appeared in route snapshot before fresh health: routes=%+v err=%v", routes, err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT next_health_check_at > now() + interval '50 seconds' FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&delayHonored); err != nil {
		t.Fatal(err)
	}
	if !delayHonored {
		t.Fatal("runtime completion did not preserve initial delay after process restart")
	}
	var reboundContainerID, reboundAddress string
	var reboundGeneration int64
	if err := f.pool.QueryRow(f.ctx, `SELECT health_container_id,host(container_address),health_generation FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&reboundContainerID, &reboundAddress, &reboundGeneration); err != nil {
		t.Fatal(err)
	}
	if reboundContainerID != container.ID || reboundAddress != container.Address || reboundGeneration != restartJob.App.DesiredGeneration {
		t.Fatalf("runtime identity was not rebound to the restarted process: container=%s address=%s generation=%d", reboundContainerID, reboundAddress, reboundGeneration)
	}

	forceAppHealthCheckDue(t, f, appID)
	freshProbe, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "restart-health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if freshProbe.ContainerID != container.ID {
		t.Fatalf("fresh probe targets container %q, restarted container is %q", freshProbe.ContainerID, container.ID)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, freshProbe, true); err != nil {
		t.Fatal(err)
	}
	recovered, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RuntimeStatus != "running" || recovered.HealthStatus != "healthy" || recovered.RouteStatus != "active" {
		t.Fatalf("fresh post-restart probe did not restore routing: runtime=%s health=%s route=%s", recovered.RuntimeStatus, recovered.HealthStatus, recovered.RouteStatus)
	}
	if routes, err := f.repo.ListAppPlatformRoutes(f.ctx); err != nil || !appRouteExists(routes, appID) {
		t.Fatalf("freshly healthy same-container restart was not routed: routes=%+v err=%v", routes, err)
	} else if len(routes) != 1 || routes[0].RouteIdentity != newRouteIdentity.String() {
		t.Fatalf("route snapshot did not publish the restarted incarnation identity: routes=%+v want=%s", routes, newRouteIdentity)
	}

	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET next_inspection_at=now() WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	staleRestart, err := f.repo.ClaimNextAppRuntime(f.ctx, "stale-restart-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	changedWorkload := workload
	changedWorkload.Port++
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &changedWorkload}); err != nil {
		t.Fatal(err)
	}
	staleIdentity, err := NewAppRuntimeRouteIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ResetAppHealthBeforeRuntimeRestart(f.ctx, staleRestart, staleIdentity); !errors.Is(err, ErrAppRuntimeStale) {
		t.Fatalf("health reset after desired generation changed = %v, want stale", err)
	}
	changed, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if changed.DesiredGeneration != staleRestart.App.DesiredGeneration+1 || changed.RouteStatus != "waiting_for_runtime" {
		t.Fatalf("stale restart reset authorized a changed generation: desired=%d route=%s", changed.DesiredGeneration, changed.RouteStatus)
	}
}

func TestAppRuntimeLogSourcesRetainVerifiedContainerHistoryIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "runtime-log-history", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	deploymentID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	firstJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-log-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := runtimeRepositoryContainer(firstJob, deploymentID)
	if err := f.repo.CompleteAppRuntime(f.ctx, firstJob, "running", first); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeLogSources(t, f, appID, first.ID)

	// Re-observing the same trusted Moby ID is idempotent even after identity
	// rotation; the runtime incarnation name changes but the log source does not.
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET next_inspection_at=now() WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	sameJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-log-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sameContainer := runtimeRepositoryContainer(sameJob, deploymentID)
	if err := f.repo.CompleteAppRuntime(f.ctx, sameJob, "running", sameContainer); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeLogSources(t, f, appID, first.ID)
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM app_runtime_log_sources WHERE app_id=$1 AND container_id=$2`, appID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET next_inspection_at=now() WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	recoveryJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-log-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	recoveredContainer := runtimeRepositoryContainer(recoveryJob, deploymentID)
	if err := f.repo.CompleteAppRuntime(f.ctx, recoveryJob, "running", recoveredContainer); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeLogSources(t, f, appID, first.ID)

	changed := workloadspec.Default()
	changed.Port++
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Workload: &changed}); err != nil {
		t.Fatal(err)
	}
	replacementJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "runtime-log-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	replacement := runtimeRepositoryContainer(replacementJob, deploymentID)
	replacement.ID = strings.Repeat("b", 64)
	if err := f.repo.CompleteAppRuntime(f.ctx, replacementJob, "running", replacement); err != nil {
		t.Fatal(err)
	}
	assertAppRuntimeLogSources(t, f, appID, strings.Repeat("a", 64), strings.Repeat("b", 64))
}

func TestAppRuntimeAddressValidityProjectionAndIngressParityIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cleanupAppRuntimeIntegrationRows(t, f)
	var previousDomain string
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(workload_base_domain,'') FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE instance_domain_settings SET workload_base_domain='address-parity.example.test',updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if previousDomain == "" {
			_, _ = f.pool.Exec(context.Background(), `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`)
		} else {
			_, _ = f.pool.Exec(context.Background(), `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, previousDomain)
		}
	})

	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{
		Name: "runtime-address-parity", Enabled: true, Workload: workloadspec.Default(),
	}); err != nil {
		t.Fatal(err)
	}
	deploymentID := createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	runtimeJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "address-parity-runtime-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	container := runtimeRepositoryContainer(runtimeJob, deploymentID)
	if err := f.repo.CompleteAppRuntime(f.ctx, runtimeJob, "running", container); err != nil {
		t.Fatal(err)
	}
	forceAppHealthCheckDue(t, f, appID)
	healthJob, err := f.repo.ClaimNextAppHealthCheck(f.ctx, "address-parity-health-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteAppHealthCheck(f.ctx, healthJob, true); err != nil {
		t.Fatal(err)
	}

	privateAddressState, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if privateAddressState.HealthStatus != "healthy" || privateAddressState.RouteStatus != "active" {
		t.Fatalf("valid private runtime address did not permit API route eligibility: health=%s route=%s", privateAddressState.HealthStatus, privateAddressState.RouteStatus)
	}
	var privateAddress string
	if err := f.pool.QueryRow(f.ctx, `SELECT host(container_address) FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&privateAddress); err != nil {
		t.Fatal(err)
	}
	if !validPrivateRuntimeAddress(privateAddress) {
		t.Fatalf("test runtime address %q is not a valid private IPv4 address", privateAddress)
	}
	privateRoutes, err := f.repo.ListAppPlatformRoutes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !appRouteExists(privateRoutes, appID) {
		t.Fatalf("API reported active for a valid private address but ingress omitted the App: routes=%+v", privateRoutes)
	}

	// 8.8.8.8 is valid IPv4 but is not in an RFC1918 private range. Changing
	// only this persisted evidence must make both projections fail closed.
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET container_address='8.8.8.8'::inet WHERE app_id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	var persistedAddress string
	if err := f.pool.QueryRow(f.ctx, `SELECT host(container_address) FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&persistedAddress); err != nil {
		t.Fatal(err)
	}
	if persistedAddress != "8.8.8.8" || validPrivateRuntimeAddress(persistedAddress) {
		t.Fatalf("test non-private runtime address = %q, unexpectedly accepted", persistedAddress)
	}
	nonPrivateAddressState, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if nonPrivateAddressState.HealthStatus != "healthy" || nonPrivateAddressState.RouteStatus != "waiting_for_health" {
		t.Fatalf("non-private runtime address did not make the API projection fail closed: health=%s route=%s", nonPrivateAddressState.HealthStatus, nonPrivateAddressState.RouteStatus)
	}
	nonPrivateRoutes, err := f.repo.ListAppPlatformRoutes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if appRouteExists(nonPrivateRoutes, appID) {
		t.Fatalf("API reported route_status=%s for a non-private address but ingress still included the App: routes=%+v", nonPrivateAddressState.RouteStatus, nonPrivateRoutes)
	}
}

func forceAppHealthCheckDue(t *testing.T, f appRepositoryFixture, appID uuid.UUID) {
	t.Helper()
	result, err := f.pool.Exec(f.ctx, `UPDATE app_runtime_state SET next_health_check_at=now() WHERE app_id=$1`, appID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("updated %d health rows, want one", result.RowsAffected())
	}
}

func appRouteExists(routes []domain.AppPlatformRoute, appID uuid.UUID) bool {
	for _, route := range routes {
		if route.AppID == appID.String() {
			return true
		}
	}
	return false
}

func boolPointer(value bool) *bool { return &value }

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

func assertAppRuntimeLogSources(t *testing.T, f appRepositoryFixture, appID uuid.UUID, want ...string) {
	t.Helper()
	got, err := f.repo.ListAppRuntimeLogSources(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("runtime log source IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("runtime log source IDs = %v, want %v", got, want)
		}
	}
}

func assertAppRuntimeCleanupContainerID(t *testing.T, f appRepositoryFixture, appID uuid.UUID) {
	t.Helper()
	var containerID string
	err := f.pool.QueryRow(f.ctx, `
		SELECT COALESCE(container_id,'')
		FROM app_runtime_cleanup_jobs
		WHERE app_id=$1 AND status='pending'
		ORDER BY created_at DESC LIMIT 1`, appID).Scan(&containerID)
	if err != nil {
		t.Fatal(err)
	}
	if containerID != strings.Repeat("a", 64) {
		t.Fatalf("App runtime cleanup job did not persist the container ID: %v", containerID)
	}
}

func prioritizeAppRuntimeCleanupForTest(t *testing.T, f appRepositoryFixture, appID uuid.UUID) {
	t.Helper()
	result, err := f.pool.Exec(f.ctx, `
		UPDATE app_runtime_cleanup_jobs
		SET next_attempt_at='epoch'::timestamptz
		WHERE app_id=$1 AND status='pending'`, appID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("updated %d target App runtime cleanup jobs, want exactly one", result.RowsAffected())
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
		ID: strings.Repeat("a", 64), Name: job.ContainerName,
		ImageID: "sha256:" + strings.Repeat("b", 64), ImageDigest: "sha256:" + strings.Repeat("d", 64),
		RuntimeTag: "stealth-app/" + deploymentID.String() + ":runtime", Address: "172.22.0.5",
	}
}

func currentAppRuntimeContainerName(t *testing.T, f appRepositoryFixture, appID uuid.UUID) string {
	t.Helper()
	var name string
	if err := f.pool.QueryRow(f.ctx, `SELECT container_name FROM app_runtime_state WHERE app_id=$1`, appID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
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
