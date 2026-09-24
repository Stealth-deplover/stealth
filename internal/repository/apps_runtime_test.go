package repository

import (
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
)

func TestRuntimeCleanupAcceptsPersistedContainerNameForms(t *testing.T) {
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	containerID := strings.Repeat("a", 64)
	name := AppRuntimeContainerName(appID)
	for _, candidate := range []string{name, "/" + name} {
		if !validCleanupTarget(appID, containerID, candidate) {
			t.Errorf("valid persisted Docker name %q was rejected", candidate)
		}
	}
	if !validCleanupTarget(appID, "", "/"+name) {
		t.Fatal("deterministic name without a stored container ID was rejected")
	}
}

func TestRuntimeCleanupRejectsUnsafeContainerTargets(t *testing.T) {
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	containerID := strings.Repeat("a", 64)
	for _, name := range []string{"", "/", "/two/names", "../escape", "/with space"} {
		if validCleanupTarget(appID, containerID, name) {
			t.Errorf("unsafe cleanup target %q was accepted", name)
		}
	}
	if validCleanupTarget(appID, "bad-id", AppRuntimeContainerName(appID)) {
		t.Fatal("malformed container ID was accepted")
	}
}

func TestHealthStateAfterProbeRespectsFailureThresholdAndRecovery(t *testing.T) {
	for _, test := range []struct {
		name      string
		current   string
		failures  int
		succeeded bool
		threshold int
		wantState string
		wantCount int
	}{
		{name: "first pending failure", current: "pending", failures: 0, threshold: 3, wantState: "pending", wantCount: 1},
		{name: "pending reaches threshold", current: "pending", failures: 2, threshold: 3, wantState: "unhealthy", wantCount: 3},
		{name: "healthy tolerates one failure", current: "healthy", failures: 0, threshold: 3, wantState: "healthy", wantCount: 1},
		{name: "healthy removed at threshold", current: "healthy", failures: 2, threshold: 3, wantState: "unhealthy", wantCount: 3},
		{name: "unhealthy remains unhealthy before success", current: "unhealthy", failures: 1, threshold: 3, wantState: "unhealthy", wantCount: 2},
		{name: "success converges and clears failures", current: "unhealthy", failures: 8, succeeded: true, threshold: 3, wantState: "healthy", wantCount: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, failures := healthStateAfterProbe(test.current, test.failures, test.succeeded, test.threshold)
			if state != test.wantState || failures != test.wantCount {
				t.Fatalf("health transition = %s/%d, want %s/%d", state, failures, test.wantState, test.wantCount)
			}
		})
	}
}

func TestAppRouteStatusRequiresCurrentRuntimeHealthAndHostname(t *testing.T) {
	deploymentID := uuid.Must(uuid.NewV7()).String()
	hostname := "sample.apps.example.test"
	app := domain.App{
		Enabled: true, DesiredDeploymentID: &deploymentID, PlatformHostname: &hostname,
		DesiredGeneration: 4, ObservedGeneration: 4, RuntimeStatus: "running", RouteDeploymentReady: true,
	}
	if got := appRouteStatus(app, "healthy"); got != "active" {
		t.Fatalf("healthy current App route status = %q", got)
	}
	for _, test := range []struct {
		name      string
		mutate    func(*domain.App)
		health    string
		wantRoute string
	}{
		{name: "disabled", mutate: func(value *domain.App) { value.Enabled = false }, health: "healthy", wantRoute: "not_available"},
		{name: "not selected", mutate: func(value *domain.App) { value.DesiredDeploymentID = nil }, health: "healthy", wantRoute: "not_available"},
		{name: "no hostname", mutate: func(value *domain.App) { value.PlatformHostname = nil }, health: "healthy", wantRoute: "not_available"},
		{name: "deployment not ready", mutate: func(value *domain.App) { value.RouteDeploymentReady = false }, health: "healthy", wantRoute: "waiting_for_runtime"},
		{name: "stale generation", mutate: func(value *domain.App) { value.ObservedGeneration-- }, health: "healthy", wantRoute: "waiting_for_runtime"},
		{name: "stopped runtime", mutate: func(value *domain.App) { value.RuntimeStatus = "stopped" }, health: "healthy", wantRoute: "waiting_for_runtime"},
		{name: "pending health", mutate: func(*domain.App) {}, health: "pending", wantRoute: "waiting_for_health"},
		{name: "unhealthy", mutate: func(*domain.App) {}, health: "unhealthy", wantRoute: "waiting_for_health"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := app
			test.mutate(&candidate)
			if got := appRouteStatus(candidate, test.health); got != test.wantRoute {
				t.Fatalf("route status = %q, want %q", got, test.wantRoute)
			}
		})
	}
}
