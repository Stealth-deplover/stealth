package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type uninstallCommandCall struct {
	dir  string
	name string
	args []string
}

type uninstallTestRunner struct {
	calls       []uninstallCommandCall
	runErr      error
	outputErr   error
	volumeNames string
	volumeList  string
}

func (r *uninstallTestRunner) Run(_ context.Context, dir string, _ io.Writer, _ io.Writer, name string, args ...string) error {
	r.calls = append(r.calls, uninstallCommandCall{dir: dir, name: name, args: append([]string(nil), args...)})
	return r.runErr
}

func (r *uninstallTestRunner) Output(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, uninstallCommandCall{dir: dir, name: name, args: append([]string(nil), args...)})
	if r.outputErr != nil {
		return nil, r.outputErr
	}
	if containsArgs(args, "config") && containsArgs(args, "--volumes") {
		if r.volumeNames != "" {
			return []byte(r.volumeNames), nil
		}
		return []byte("postgres_data\nstealth_storage\nfunction_runner_staging\n"), nil
	}
	if containsArgs(args, "volume", "ls") {
		return []byte(r.volumeList), nil
	}
	return nil, nil
}

func containsArgs(args []string, wanted ...string) bool {
	for _, value := range wanted {
		found := false
		for _, arg := range args {
			if arg == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func writeUninstallFixture(t *testing.T) InstallLayout {
	t.Helper()
	layout := newInstallLayout(filepath.Join(t.TempDir(), ".stealth"))
	values := map[string]string{
		"COMPOSE_PROJECT_NAME":            "stealth",
		"STEALTH_API_IMAGE":               imageName("stealth-api", "v1.2.3"),
		"STEALTH_WORKER_IMAGE":            imageName("stealth-worker", "v1.2.3"),
		"STEALTH_MIGRATE_IMAGE":           imageName("stealth-migrate", "v1.2.3"),
		"STEALTH_CONSOLE_IMAGE":           imageName("stealth-console", "v1.2.3"),
		"POSTGRES_DB":                     "stealth",
		"POSTGRES_USER":                   "stealth",
		"POSTGRES_PASSWORD":               "postgres-password",
		"REDIS_PASSWORD":                  "redis-password",
		"FUNCTIONS_SECRET_KEY":            "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"PUBLIC_APP_URL":                  "https://console.example.test",
		"DOCKER_GID":                      "123",
		"FUNCTIONS_RUNNER_STAGING_VOLUME": "stealth_function_runner_staging",
		"STORAGE_DRIVER":                  "local",
	}
	if err := writePrivateFile(layout.EnvFile, formatEnvFile(values)); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(layout.ComposeFile, []byte("services:\n  api:\n    image: test\nvolumes:\n  postgres_data:\n  stealth_storage:\n  function_runner_staging:\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(layout.ProxyFile, []byte("server {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(layout.VersionFile, []byte("v1.2.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestUninstallReportsNoInstallation(t *testing.T) {
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = t.TempDir()
	if got := app.run([]string{"uninstall"}); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if got := out.String(); got != "No Stealth installation was found.\n" {
		t.Fatalf("output = %q", got)
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", errOut.String())
	}
}

func TestSafeUninstallPreservesDataAndNeverUsesVolumeDeletion(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall", "--keep-data", "--yes"}); got != 0 {
		t.Fatalf("exit code = %d, stderr=%q, stdout=%q", got, errOut.String(), out.String())
	}
	for _, path := range []string{layout.EnvFile, layout.ComposeFile, layout.ProxyFile, layout.VersionFile, layout.StateDir} {
		if !pathPresent(path) {
			t.Fatalf("safe uninstall removed preserved path %s", path)
		}
	}
	for _, call := range runner.calls {
		if call.name != "docker" {
			continue
		}
		if containsArgs(call.args, "system", "prune") || containsArgs(call.args, "volume", "prune") || containsArgs(call.args, "down", "--volumes") {
			t.Fatalf("safe uninstall issued destructive Docker command: %#v", call.args)
		}
	}
	if !strings.Contains(out.String(), "Persistent data, config.env, and recovery files were preserved") {
		t.Fatalf("safe completion message missing: %q", out.String())
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("non-TTY output contains ANSI escape sequences: %q", out.String())
	}
}

func TestYesAloneSelectsSafeMode(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall", "--yes"}); got != 0 {
		t.Fatalf("exit code = %d, stderr=%q", got, errOut.String())
	}
	for _, call := range runner.calls {
		if containsArgs(call.args, "down", "--volumes") {
			t.Fatalf("--yes alone selected purge: %#v", call.args)
		}
	}
	if !pathPresent(layout.EnvFile) {
		t.Fatal("--yes alone removed config.env")
	}
}

func TestConfigurationUninstallPreservesRecoveryConfig(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	plan := buildUninstallPlan(layout, uninstallConfiguration)
	if err := executeUninstallForTest(app, plan); err != nil {
		t.Fatal(err)
	}
	if !pathPresent(layout.EnvFile) {
		t.Fatal("configuration mode removed config.env and recovery secrets")
	}
	for _, path := range []string{layout.ComposeFile, layout.ProxyFile, layout.VersionFile, layout.StateDir} {
		if pathPresent(path) {
			t.Fatalf("configuration mode preserved local runtime path %s", path)
		}
	}
	if !strings.Contains(strings.Join(plan.warnings(), "\n"), "FUNCTIONS_SECRET_KEY") {
		t.Fatal("plan did not explain recovery-secret preservation")
	}
}

func TestPurgeRemovesProjectOwnedDataAfterExactValidation(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall", "--purge", "--yes"}); got != 0 {
		t.Fatalf("exit code = %d, stderr=%q, stdout=%q", got, errOut.String(), out.String())
	}
	if pathPresent(layout.Root) {
		t.Fatal("purge left the installation directory behind")
	}
	var sawPurge bool
	for _, call := range runner.calls {
		if call.name != "docker" {
			continue
		}
		if containsArgs(call.args, "down", "--volumes", "--remove-orphans") {
			sawPurge = true
		}
		if containsArgs(call.args, "system", "prune") || containsArgs(call.args, "volume", "prune") {
			t.Fatalf("purge issued broad Docker cleanup: %#v", call.args)
		}
	}
	if !sawPurge {
		t.Fatalf("purge did not issue docker compose down --volumes; calls=%#v", runner.calls)
	}
	if !strings.Contains(out.String(), "permanently removed") {
		t.Fatalf("purge completion message missing: %q", out.String())
	}
}

func TestPurgeRefusesUnlabeledExistingVolume(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{volumeList: "stealth_storage\n"}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall", "--purge", "--yes"}); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	for _, call := range runner.calls {
		if containsArgs(call.args, "down") {
			t.Fatalf("purge targeted an unlabeled volume: %#v", call.args)
		}
	}
	if !pathPresent(layout.EnvFile) || !pathPresent(layout.Root) {
		t.Fatal("ownership validation removed local recovery state")
	}
	if !strings.Contains(errOut.String(), "not labeled") {
		t.Fatalf("missing ownership failure detail: %q", errOut.String())
	}
}

func TestPurgeDryRunMakesNoChanges(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall", "--purge", "--dry-run"}); got != 0 {
		t.Fatalf("exit code = %d, stderr=%q", got, errOut.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry-run invoked Docker: %#v", runner.calls)
	}
	if !pathPresent(layout.Root) || !pathPresent(layout.EnvFile) {
		t.Fatal("dry-run changed local installation state")
	}
	if !strings.Contains(out.String(), "This is permanent") || !strings.Contains(out.String(), "No changes were made") {
		t.Fatalf("dry-run output did not explain safety: %q", out.String())
	}
}

func TestUninstallDryRunPlanIncludesOwnedResources(t *testing.T) {
	layout := writeUninstallFixture(t)
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	if got := app.run([]string{"uninstall", "--purge", "--dry-run"}); got != 0 {
		t.Fatalf("exit code = %d, stderr=%q", got, errOut.String())
	}
	for _, expected := range []string{
		"Mode           PURGE",
		"stealth_postgres_data",
		"stealth_storage",
		"stealth_function_runner_staging",
		"config.env and local secrets",
		"This is permanent",
		"No changes were made",
	} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("dry-run plan missing %q: %s", expected, out.String())
		}
	}
	t.Log(out.String())
}

func TestNonTTYWithoutModeFailsSafely(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall"}); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if len(runner.calls) != 0 || !pathPresent(layout.Root) {
		t.Fatalf("non-TTY default uninstall changed state: calls=%#v", runner.calls)
	}
	if !strings.Contains(errOut.String(), "explicit mode") {
		t.Fatalf("missing non-TTY guidance: %q", errOut.String())
	}
}

func TestPartialInstallationIsReportedAndNotGuessed(t *testing.T) {
	layout := newInstallLayout(filepath.Join(t.TempDir(), ".stealth"))
	if err := os.MkdirAll(layout.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(layout.ComposeFile, []byte("services:\n  api:\n    image: test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &uninstallTestRunner{}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall", "--keep-data", "--yes"}); got == 0 {
		t.Fatalf("partial install unexpectedly reported complete: stdout=%q", out.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("partial install guessed Docker resources: %#v", runner.calls)
	}
	if !strings.Contains(out.String(), "Partial installation") || !strings.Contains(errOut.String(), "config.env") {
		t.Fatalf("partial installation guidance missing: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestDockerFailureDoesNotRemoveConfiguration(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{runErr: errors.New("docker unavailable")}
	var out, errOut strings.Builder
	app := NewApp(strings.NewReader(""), &out, &errOut)
	app.homeDir = filepath.Dir(layout.Root)
	app.runner = runner
	if got := app.run([]string{"uninstall", "--keep-data", "--yes"}); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !pathPresent(layout.EnvFile) || !pathPresent(layout.ComposeFile) {
		t.Fatal("Docker failure removed local configuration")
	}
	if !strings.Contains(errOut.String(), "Uninstall incomplete") || !strings.Contains(errOut.String(), "doctor") {
		t.Fatalf("failure output was not actionable: %q", errOut.String())
	}
}

func TestPurgeRequiresExactStrongConfirmation(t *testing.T) {
	layout := writeUninstallFixture(t)
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	plan := buildUninstallPlan(layout, uninstallPurge)
	model := newUninstallModel(app, context.Background(), func() {}, plan, uninstallOptions{mode: uninstallPurge, modeSet: true})
	updated, _ := model.updatePlan(tea.KeyMsg{Type: tea.KeyEnter})
	confirmation := updated.(uninstallModel)
	confirmation.confirmInput.SetValue("stealtx")
	updated, _ = confirmation.updatePurgeConfirmation(tea.KeyMsg{Type: tea.KeyEnter})
	confirmation = updated.(uninstallModel)
	if confirmation.screen != uninstallCancelled {
		t.Fatalf("wrong confirmation did not cancel: screen=%v", confirmation.screen)
	}

	model = newUninstallModel(app, context.Background(), func() {}, plan, uninstallOptions{mode: uninstallPurge, modeSet: true})
	updated, _ = model.updatePlan(tea.KeyMsg{Type: tea.KeyEnter})
	confirmation = updated.(uninstallModel)
	confirmation.confirmInput.SetValue("stealth")
	var command tea.Cmd
	updated, command = confirmation.updatePurgeConfirmation(tea.KeyMsg{Type: tea.KeyEnter})
	confirmation = updated.(uninstallModel)
	if confirmation.screen != uninstallRemoving || command == nil {
		t.Fatalf("exact confirmation did not start removal: screen=%v command=%v", confirmation.screen, command)
	}
}

func TestCancelAndCtrlCDoNotRunOperations(t *testing.T) {
	layout := writeUninstallFixture(t)
	runner := &uninstallTestRunner{}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	plan := buildUninstallPlan(layout, uninstallServices)
	model := newUninstallModel(app, context.Background(), func() {}, plan, uninstallOptions{})
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if updated.(uninstallModel).screen != uninstallCancelled || command == nil {
		t.Fatalf("escape did not cancel menu: model=%#v command=%v", model, command)
	}
	model = newUninstallModel(app, context.Background(), func() {}, plan, uninstallOptions{})
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if updated.(uninstallModel).screen != uninstallCancelled || command == nil {
		t.Fatalf("ctrl+c did not cancel safely: model=%#v command=%v", model, command)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("cancel path invoked Docker: %#v", runner.calls)
	}
}

func TestNOColorAndProgressRendering(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	initTerminalStyles()
	layout := writeUninstallFixture(t)
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	plan := buildUninstallPlan(layout, uninstallServices)
	model := newUninstallModel(app, context.Background(), func() {}, plan, uninstallOptions{mode: uninstallServices, modeSet: true})
	model.screen = uninstallRemoving
	view := model.View()
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("NO_COLOR view contains ANSI escapes: %q", view)
	}
	if !strings.Contains(view, "Removing Stealth") || !strings.Contains(view, "○") {
		t.Fatalf("progress view missing expected TUI elements: %q", view)
	}
}

func executeUninstallForTest(app *App, plan uninstallPlan) error {
	for _, operation := range app.uninstallOperations(plan) {
		if err := operation.action(context.Background()); err != nil {
			return err
		}
	}
	return nil
}
