package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

type hostInstallFixture struct {
	app    *App
	layout InstallLayout
	store  *setupstate.FileStore
	state  setupstate.State
	values map[string]string
	server *httptest.Server
}

func newHostInstallFixture(t *testing.T) *hostInstallFixture {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/setup/handoff/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"pending":false}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	port := strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)
	root := filepath.Join(t.TempDir(), "install")
	layout, err := installengine.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, strings.Join([]string{
		"STEALTH_API_IMAGE=ghcr.io/stealth-deplover/stealth-api:v1.2.3",
		"API_HOST_PORT=" + port,
		"CONSOLE_HOST_PORT=" + port,
		"PROXY_HTTP_PORT=" + port,
		"SETUP_API_HOST_PORT=" + port,
		"BOOTSTRAP_CLI_KEY=" + encodeTestSecret([]byte("01234567890123456789012345678901")),
		"FUNCTIONS_SECRET_KEY=" + encodeTestSecret([]byte("abcdefghijklmnopqrstuvwxyz123456")),
	}, "\n")+"\n"); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		layout.ComposeFile:      "services:\n",
		layout.SetupComposeFile: "services:\n",
		layout.ProxyFile:        "server {\n}\n",
	} {
		if err := installengine.WriteAtomic(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := functionsecret.New([]byte("abcdefghijklmnopqrstuvwxyz123456"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(filepath.Join(root, "state", "setup-state.enc"), cipher)
	if err != nil {
		t.Fatal(err)
	}
	state := setupstate.NewState()
	state.Draft.PublicURL = server.URL
	state.Draft.NetworkMode = "local_only"
	state.GitHub.Connected = true
	state.GitHub.ClientID = "Iv1.setup-client"
	if err := setupstate.RequestInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	state.Step = "Configuration and secrets"
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	values, err := readEnvFile(layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = &setupRunner{}
	app.httpClient = server.Client()
	app.pollAttempts = 1
	app.pollInterval = time.Millisecond
	return &hostInstallFixture{app: app, layout: layout, store: store, state: state, values: values, server: server}
}

func TestHostInstallerOwnsRequestAndCompletesHandoff(t *testing.T) {
	fixture := newHostInstallFixture(t)
	if err := fixture.app.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1"); err != nil {
		t.Fatalf("executeHostInstallation() error = %v", err)
	}
	state, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseComplete || state.Step != "Complete" || state.InstallRunID != "run-1" {
		t.Fatalf("completed state = %#v", state)
	}
	if state.LastEventID < 8 {
		t.Fatalf("host progress did not advance durable event ID: %d", state.LastEventID)
	}
	runner := fixture.app.runner.(*setupRunner)
	if len(runner.calls) != 6 {
		t.Fatalf("host Docker calls = %#v, want production steps plus setup cleanup", runner.calls)
	}
	if got := runner.command(len(runner.calls) - 1).args; !equalStrings(got, []string{"compose", "--env-file", fixture.layout.EnvFile, "-f", fixture.layout.SetupComposeFile, "rm", "-sf", "setup", "setup-console", "setup-proxy"}) {
		t.Fatalf("setup cleanup command = %#v", got)
	}
	values, err := readEnvFile(fixture.layout.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["SETUP_MODE"] != "false" {
		t.Fatalf("production env SETUP_MODE = %q, want false", values["SETUP_MODE"])
	}
}

func TestHostInstallerRepairResumesHandoffWithoutReinstalling(t *testing.T) {
	fixture := newHostInstallFixture(t)
	state := fixture.state
	if err := setupstate.BeginInstallation(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := setupstate.MarkInstallationHandoff(&state, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if err := fixture.app.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1"); err != nil {
		t.Fatalf("handoff repair error = %v", err)
	}
	completed, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != setupstate.PhaseComplete {
		t.Fatalf("handoff repair state = %#v", completed)
	}
	calls := fixture.app.runner.(*setupRunner).calls
	if len(calls) != 1 || !equalStrings(calls[0].args, []string{"compose", "--env-file", fixture.layout.EnvFile, "-f", fixture.layout.SetupComposeFile, "rm", "-sf", "setup", "setup-console", "setup-proxy"}) {
		t.Fatalf("handoff repair Docker calls = %#v", calls)
	}
}

type cleanupFailureRunner struct {
	*setupRunner
}

func (r *cleanupFailureRunner) Run(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.setupRunner.mu.Lock()
	r.setupRunner.calls = append(r.setupRunner.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	r.setupRunner.mu.Unlock()
	if containsArgs(args, "rm") {
		return errors.New("temporary setup cleanup failed")
	}
	return nil
}

func TestHostInstallerKeepsHandoffRepairableWhenCleanupFails(t *testing.T) {
	fixture := newHostInstallFixture(t)
	runner := &cleanupFailureRunner{setupRunner: &setupRunner{}}
	fixture.app.runner = runner
	if err := fixture.app.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1"); err == nil {
		t.Fatal("cleanup failure unexpectedly succeeded")
	}
	state, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseHandoff || state.ErrorCode != "setup_cleanup_failed" || state.Step != "Cleanup" {
		t.Fatalf("cleanup failure state = %#v", state)
	}

	fixture.app.runner = &setupRunner{}
	if err := fixture.app.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1"); err != nil {
		t.Fatalf("handoff cleanup retry error = %v", err)
	}
	state, err = fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseComplete {
		t.Fatalf("cleanup retry state = %#v", state)
	}
}

func TestHostInstallerFailsInvalidFinalizedStateWithoutDocker(t *testing.T) {
	fixture := newHostInstallFixture(t)
	fixture.state.GitHub.Connected = false
	if err := fixture.store.Save(context.Background(), fixture.state); err != nil {
		t.Fatal(err)
	}
	err := fixture.app.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1")
	if err == nil || !strings.Contains(err.Error(), "GitHub") {
		t.Fatalf("invalid finalized state error = %v", err)
	}
	state, loadErr := fixture.store.Load(context.Background())
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.Phase != setupstate.PhaseFailed || state.ErrorCode != "install_validation_failed" {
		t.Fatalf("invalid state result = %#v", state)
	}
	if len(fixture.app.runner.(*setupRunner).calls) != 0 {
		t.Fatalf("invalid state invoked host Docker: %#v", fixture.app.runner.(*setupRunner).calls)
	}
}

func TestHostInstallerLeavesRequestOwnedByExistingLock(t *testing.T) {
	fixture := newHostInstallFixture(t)
	lock, err := installengine.AcquireProcessLock(fixture.layout.StateDir, "install.lock", "installation")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := fixture.app.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1"); !errors.Is(err, installengine.ErrOperationInProgress) {
		t.Fatalf("locked host installer error = %v, want ErrOperationInProgress", err)
	}
	state, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseInstallRequested || state.InstallRunID != "run-1" {
		t.Fatalf("locked state = %#v", state)
	}
	if len(fixture.app.runner.(*setupRunner).calls) != 0 {
		t.Fatalf("locked installer invoked Docker: %#v", fixture.app.runner.(*setupRunner).calls)
	}
}

func TestHostInstallerFailureIsDurableAndProgressIsSafe(t *testing.T) {
	fixture := newHostInstallFixture(t)
	fixture.app.runner = &setupRunner{runErr: errors.New("docker compose pull failed: secret must not be copied")}
	err := fixture.app.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1")
	if err == nil {
		t.Fatal("host installer unexpectedly succeeded")
	}
	state, loadErr := fixture.store.Load(context.Background())
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.Phase != setupstate.PhaseFailed || state.ErrorCode != "install_failed" {
		t.Fatalf("failed host state = %#v", state)
	}
	if strings.Contains(state.ErrorMessage, "secret must not be copied") {
		t.Fatalf("unsafe runner detail reached setup state: %q", state.ErrorMessage)
	}
}

type blockingHostRunner struct {
	mu      sync.Mutex
	calls   []recordedCommand
	started chan struct{}
}

func (r *blockingHostRunner) Run(ctx context.Context, _ string, _, _ io.Writer, name string, args ...string) error {
	r.mu.Lock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	first := len(r.calls) == 1
	r.mu.Unlock()
	if first {
		close(r.started)
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (r *blockingHostRunner) Output(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	return nil, nil
}

func (r *blockingHostRunner) CombinedOutput(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	return r.Output(ctx, dir, name, args...)
}

func TestHostInstallerRestartResumesInstallingRun(t *testing.T) {
	fixture := newHostInstallFixture(t)
	blocking := &blockingHostRunner{started: make(chan struct{})}
	fixture.app.runner = blocking
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- fixture.app.executeHostInstallation(ctx, fixture.layout, fixture.values, fixture.store, "run-1")
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("host installer did not reach Docker runner")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled host installer unexpectedly succeeded")
	}
	state, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseInstalling || state.InstallRunID != "run-1" {
		t.Fatalf("crash/restart state = %#v", state)
	}

	resumed := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	resumed.runner = &setupRunner{}
	resumed.httpClient = fixture.server.Client()
	resumed.pollAttempts = 1
	resumed.pollInterval = time.Millisecond
	if err := resumed.executeHostInstallation(context.Background(), fixture.layout, fixture.values, fixture.store, "run-1"); err != nil {
		t.Fatalf("resumed host installer error = %v", err)
	}
	state, err = fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != setupstate.PhaseComplete {
		t.Fatalf("resumed state = %#v", state)
	}
}

func TestProjectHostPreflightOmitsNoisyValues(t *testing.T) {
	checks := projectHostPreflight([]SystemCheck{{Name: "Docker", Detail: "daemon ready", OK: true, Required: true}, {Name: "Disk", Detail: strings.Repeat("x", 300), OK: false}})
	if len(checks) != 2 || checks[0].Name != "Docker" || len(checks[1].Detail) != 240 {
		t.Fatalf("projected host checks = %#v", checks)
	}
}

func TestHostInstallErrorDoesNotExposeCommandOutput(t *testing.T) {
	message := safeHostInstallError(fmt.Errorf("docker output\nsecret-token\r\n"))
	if strings.ContainsAny(message, "\r\n") || strings.Contains(message, "secret-token") {
		t.Fatalf("sanitized host error = %q", message)
	}
}
