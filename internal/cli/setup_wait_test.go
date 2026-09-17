package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

type setupStateSequenceSource struct {
	mu     sync.Mutex
	states []setupstate.State
	last   setupstate.State
}

func (s *setupStateSequenceSource) Load(ctx context.Context) (setupstate.State, error) {
	if err := ctx.Err(); err != nil {
		return setupstate.State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.states) > 0 {
		s.last = s.states[0]
		s.states = s.states[1:]
	}
	return s.last, nil
}

func TestShouldWaitForSetupDefaultsToHostCoordination(t *testing.T) {
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	t.Setenv("STEALTH_INSTALL_WAIT", "")
	if !app.shouldWaitForSetup() {
		t.Fatal("browser setup should remain attached by default")
	}
	t.Setenv("STEALTH_INSTALL_WAIT", "0")
	if !app.shouldWaitForSetup() {
		t.Fatal("STEALTH_INSTALL_WAIT=0 must not disable the host installer")
	}
	t.Setenv("STEALTH_INSTALL_WAIT", "1")
	if !app.shouldWaitForSetup() {
		t.Fatal("STEALTH_INSTALL_WAIT=1 should keep waiting")
	}
}

func TestNoWaitIsRejectedWithoutAHostSupervisor(t *testing.T) {
	var errorsOutput strings.Builder
	app := NewApp(strings.NewReader(""), io.Discard, &errorsOutput)
	if code := app.run([]string{"install", "--no-wait"}); code != 2 {
		t.Fatalf("--no-wait exit code = %d, want 2", code)
	}
	if !strings.Contains(errorsOutput.String(), "not supported for browser setup") {
		t.Fatalf("--no-wait guidance = %q", errorsOutput.String())
	}
}

func TestObserveBrowserSetupWaitsForRequestAndCompletion(t *testing.T) {
	collecting := setupstate.NewState()
	requested := collecting
	if err := setupstate.RequestInstallation(&requested, "run-1"); err != nil {
		t.Fatal(err)
	}
	requested.Step = "Configuration and secrets"
	installing := requested
	if err := setupstate.BeginInstallation(&installing, "run-1"); err != nil {
		t.Fatal(err)
	}
	installing.Step = "Release images"
	complete := installing
	complete.Phase = setupstate.PhaseComplete

	source := &setupStateSequenceSource{states: []setupstate.State{collecting, requested, installing, complete}, last: collecting}
	var output strings.Builder
	var errorsOutput strings.Builder
	app := NewApp(strings.NewReader(""), &output, &errorsOutput)
	app.pollInterval = time.Millisecond

	result := app.observeBrowserSetup(context.Background(), source)
	if result.exitCode != 0 || !result.installRequested || result.cancelled {
		t.Fatalf("observation result = %#v", result)
	}
	rendered := output.String()
	if strings.Count(rendered, "✓ Configuration received") != 1 {
		t.Fatalf("configuration milestone was not emitted once: %s", rendered)
	}
	for _, want := range []string{"Waiting for browser setup to complete...", "Preparing installation...", "Release images", "✓ Installation complete"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("observer output is missing %q: %s", want, rendered)
		}
	}
}

func TestObserveBrowserSetupInstallerErrorBeforeTerminalStateStopsWithoutRetry(t *testing.T) {
	requested := setupstate.NewState()
	if err := setupstate.RequestInstallation(&requested, "run-1"); err != nil {
		t.Fatal(err)
	}
	source := &setupStateSequenceSource{states: []setupstate.State{requested}, last: requested}
	var errorsOutput strings.Builder
	app := NewApp(strings.NewReader(""), io.Discard, &errorsOutput)
	app.pollInterval = time.Millisecond
	var invocations int
	installerErr := errors.New("host filesystem permission denied")

	result := app.observeBrowserSetupWithInstaller(context.Background(), source, func(context.Context, setupstate.State) error {
		invocations++
		return installerErr
	})

	if result.exitCode != 1 || result.cancelled || !result.installRequested {
		t.Fatalf("non-terminal installer failure result = %#v", result)
	}
	if !errors.Is(result.err, installerErr) {
		t.Fatalf("observer error = %v, want %v", result.err, installerErr)
	}
	if invocations != 1 {
		t.Fatalf("installer invocation count = %d, want 1", invocations)
	}
	if !strings.Contains(errorsOutput.String(), "Host installation failed before a terminal failure state was persisted.") || !strings.Contains(errorsOutput.String(), "stealth install --repair") {
		t.Fatalf("actionable failure output = %q", errorsOutput.String())
	}
}

func TestObserveBrowserSetupInstallerPersistedFailureUsesNormalStatePath(t *testing.T) {
	requested := setupstate.NewState()
	if err := setupstate.RequestInstallation(&requested, "run-1"); err != nil {
		t.Fatal(err)
	}
	failed := requested
	if err := setupstate.FailInstallation(&failed, "run-1", "install_failed", "Docker Compose could not start"); err != nil {
		t.Fatal(err)
	}
	source := &setupStateSequenceSource{states: []setupstate.State{requested, failed}, last: requested}
	var output strings.Builder
	var errorsOutput strings.Builder
	app := NewApp(strings.NewReader(""), &output, &errorsOutput)
	app.pollInterval = time.Millisecond
	var invocations int

	result := app.observeBrowserSetupWithInstaller(context.Background(), source, func(context.Context, setupstate.State) error {
		invocations++
		return errors.New("compose execution failed")
	})

	if result.exitCode != 1 || result.cancelled || result.err != nil || result.lastObservedPhase != setupstate.PhaseFailed {
		t.Fatalf("persisted failure result = %#v", result)
	}
	if invocations != 1 {
		t.Fatalf("installer invocation count = %d, want 1", invocations)
	}
	if !strings.Contains(errorsOutput.String(), "Installation failed.") || !strings.Contains(errorsOutput.String(), "Docker Compose could not start") {
		t.Fatalf("normal failure output = %q", errorsOutput.String())
	}
	if strings.Contains(errorsOutput.String(), "Host installation failed before a terminal failure state was persisted.") {
		t.Fatalf("duplicate observer failure output = %q", errorsOutput.String())
	}
}

func TestObserveBrowserSetupInstallerLockConflictStopsWithoutBusyLoop(t *testing.T) {
	requested := setupstate.NewState()
	if err := setupstate.RequestInstallation(&requested, "run-1"); err != nil {
		t.Fatal(err)
	}
	source := &setupStateSequenceSource{states: []setupstate.State{requested}, last: requested}
	var errorsOutput strings.Builder
	app := NewApp(strings.NewReader(""), io.Discard, &errorsOutput)
	app.pollInterval = time.Millisecond
	var invocations int

	result := app.observeBrowserSetupWithInstaller(context.Background(), source, func(context.Context, setupstate.State) error {
		invocations++
		return installengine.ErrOperationInProgress
	})

	if result.exitCode != 1 || result.cancelled || !errors.Is(result.err, installengine.ErrOperationInProgress) {
		t.Fatalf("lock conflict result = %#v", result)
	}
	if invocations != 1 {
		t.Fatalf("installer invocation count = %d, want 1", invocations)
	}
	for _, want := range []string{"Another host installer already owns this installation run.", "No second installation was started", "stealth install --repair --wait"} {
		if !strings.Contains(errorsOutput.String(), want) {
			t.Fatalf("lock conflict output missing %q: %q", want, errorsOutput.String())
		}
	}
}

func TestObserveBrowserSetupCtrlCBeforeRequestReturnsCancelableResult(t *testing.T) {
	state := setupstate.NewState()
	source := &setupStateSequenceSource{last: state}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output strings.Builder
	var errorsOutput strings.Builder
	app := NewApp(strings.NewReader(""), &output, &errorsOutput)
	app.pollInterval = time.Millisecond

	result := app.observeBrowserSetup(ctx, source)
	if result.exitCode != 0 || !result.cancelled || result.installRequested {
		t.Fatalf("cancel-before-request result = %#v", result)
	}
	if !strings.Contains(errorsOutput.String(), "Stopped waiting for browser setup.") {
		t.Fatalf("cancel output = %q", errorsOutput.String())
	}
}

func TestOrchestrateBrowserSetupCtrlCAfterRequestDoesNotCleanUp(t *testing.T) {
	root := t.TempDir()
	key := []byte("01234567890123456789012345678901")
	cipher, err := functionsecret.New(key)
	if err != nil {
		t.Fatal(err)
	}
	statePath := root + "/state/setup-state.enc"
	store, err := setupstate.NewFileStore(statePath, cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	if err := setupstate.RequestInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	runner := &setupRunner{}
	var output strings.Builder
	var errorsOutput strings.Builder
	app := NewApp(strings.NewReader(""), &output, &errorsOutput)
	app.runner = runner
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	layout := newInstallLayout(root + "/install")
	values := map[string]string{
		"STEALTH_SETUP_STATE_FILE": statePath,
		"FUNCTIONS_SECRET_KEY":     encodeTestSecret(key),
	}
	if code := app.orchestrateBrowserSetup(ctx, layout, values, "stealth-onboarding-test"); code != 0 {
		t.Fatalf("post-request Ctrl+C exit code = %d", code)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("post-request Ctrl+C cleaned up resources: %#v", runner.calls)
	}
	if !strings.Contains(errorsOutput.String(), "stealth install --repair --wait") {
		t.Fatalf("post-request Ctrl+C guidance = %q", errorsOutput.String())
	}
}

func TestCleanupUnrequestedSetupRemovesOnlyTemporaryResources(t *testing.T) {
	root := t.TempDir()
	cipher, err := functionsecret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(root+"/state/setup-state.enc", cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.QuickTunnel = "stealth-onboarding-test"
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	runner := &setupRunner{outputs: []commandOutput{{value: []byte("stealth-onboarding-test\n")}}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	layout := newInstallLayout(root + "/install")

	if err := app.cleanupUnrequestedSetup(context.Background(), layout, store, ""); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("cleanup commands = %#v, want tunnel lookup, tunnel removal, and setup service removal", runner.calls)
	}
	if !equalStrings(runner.command(1).args, []string{"rm", "--force", "stealth-onboarding-test"}) {
		t.Fatalf("tunnel cleanup command = %#v", runner.command(1))
	}
	if !equalStrings(runner.command(2).args, []string{"compose", "--env-file", layout.EnvFile, "-f", layout.SetupComposeFile, "rm", "-sf", "setup", "setup-console", "setup-proxy"}) {
		t.Fatalf("setup service cleanup command = %#v", runner.command(2))
	}
}

func TestCleanupUnrequestedSetupDoesNotRollbackRacedRequest(t *testing.T) {
	root := t.TempDir()
	cipher, err := functionsecret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(root+"/state/setup-state.enc", cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	if err := setupstate.RequestInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	runner := &setupRunner{}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner

	if err := app.cleanupUnrequestedSetup(context.Background(), newInstallLayout(root+"/install"), store, "stealth-onboarding-test"); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("raced request was rolled back with commands: %#v", runner.calls)
	}
}

func TestCleanupUnrequestedSetupReportsRunnerFailure(t *testing.T) {
	root := t.TempDir()
	cipher, err := functionsecret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(root+"/state/setup-state.enc", cipher)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), setupstate.NewState()); err != nil {
		t.Fatal(err)
	}
	runner := &setupRunner{runErr: errors.New("docker cleanup failed")}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	if err := app.cleanupUnrequestedSetup(context.Background(), newInstallLayout(root+"/install"), store, ""); err == nil || !strings.Contains(err.Error(), "docker cleanup failed") {
		t.Fatalf("cleanup failure = %v", err)
	}
}
