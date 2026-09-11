package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type recordedCommand struct {
	name string
	args []string
}

type commandOutput struct {
	value []byte
	err   error
}

type setupRunner struct {
	mu      sync.Mutex
	calls   []recordedCommand
	outputs []commandOutput
	runErr  error
}

func (r *setupRunner) Run(_ context.Context, _ string, _, _ io.Writer, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	return r.runErr
}

func (r *setupRunner) Output(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(r.outputs) == 0 {
		return nil, nil
	}
	result := r.outputs[0]
	r.outputs = r.outputs[1:]
	return result.value, result.err
}

func (r *setupRunner) command(index int) recordedCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[index]
}

func TestParseQuickTunnelURL(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "cloudflared log", output: "INF Your quick Tunnel has been created! https://Silent-Moon.trycloudflare.com\n", want: "https://silent-moon.trycloudflare.com"},
		{name: "noise around URL", output: "connect=ok\nhttps://worker-7.trycloudflare.com\nready\n", want: "https://worker-7.trycloudflare.com"},
		{name: "path is ignored", output: "https://not-safe.trycloudflare.com/setup\n", want: "https://not-safe.trycloudflare.com"},
		{name: "query is ignored", output: "https://not-safe.trycloudflare.com?code=secret\n", want: "https://not-safe.trycloudflare.com"},
		{name: "sentence punctuation", output: "Tunnel URL: https://not-safe.trycloudflare.com.\n", want: "https://not-safe.trycloudflare.com"},
		{name: "lookalike suffix", output: "https://not-safe.trycloudflare.com.evil.example\n", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseQuickTunnelURL([]byte(test.output)); got != test.want {
				t.Fatalf("parseQuickTunnelURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStartQuickTunnelUsesPinnedImageAndOnlyProxyNetwork(t *testing.T) {
	runner := &setupRunner{outputs: []commandOutput{
		{value: []byte("container-id\n")},
		{value: []byte("INF https://silent-moon.trycloudflare.com\n")},
	}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	app.pollAttempts = 2
	app.pollInterval = 0
	layout := newInstallLayout(t.TempDir())

	got, err := app.startQuickTunnel(context.Background(), layout, "stealth_network", "stealth-onboarding-test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://silent-moon.trycloudflare.com" {
		t.Fatalf("tunnel URL = %q", got)
	}
	if call := runner.command(0); call.name != "docker" || !equalStrings(call.args, []string{
		"run", "--detach", "--name", "stealth-onboarding-test", "--network", "stealth_network", "--pull=missing",
		quickTunnelCloudflaredImage, "tunnel", "--no-autoupdate", "--url", "http://proxy:80",
	}) {
		t.Fatalf("docker run command = %#v", call)
	}
	if call := runner.command(1); call.name != "docker" || !equalStrings(call.args, []string{"logs", "stealth-onboarding-test"}) {
		t.Fatalf("docker logs command = %#v", call)
	}
}

func TestStartQuickTunnelCleansUpAtCallerOnStartupFailure(t *testing.T) {
	runner := &setupRunner{outputs: []commandOutput{{err: errors.New("docker unavailable")}}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	layout := newInstallLayout(t.TempDir())

	name, err := app.startQuickTunnel(context.Background(), layout, "stealth_network", "stealth-onboarding-test")
	if err == nil || !strings.Contains(err.Error(), "start temporary onboarding tunnel") {
		t.Fatalf("startQuickTunnel() = %q, %v; want startup error", name, err)
	}
	if name != "stealth-onboarding-test" {
		t.Fatalf("failed startup name = %q, want cleanup handle", name)
	}
}

func TestStartQuickTunnelTimesOutWithoutAURL(t *testing.T) {
	runner := &setupRunner{outputs: []commandOutput{
		{value: []byte("container-id\n")},
		{value: []byte("still starting\n")},
		{value: []byte("still starting\n")},
	}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	app.pollAttempts = 2
	app.pollInterval = 0
	layout := newInstallLayout(t.TempDir())

	name, err := app.startQuickTunnel(context.Background(), layout, "stealth_network", "stealth-onboarding-test")
	if err == nil || !strings.Contains(err.Error(), "did not publish a TryCloudflare URL") {
		t.Fatalf("startQuickTunnel() = %q, %v; want bounded timeout", name, err)
	}
	if name != "stealth-onboarding-test" {
		t.Fatalf("timeout name = %q, want cleanup handle", name)
	}
}

func TestCloseQuickTunnelUsesExactGeneratedContainer(t *testing.T) {
	runner := &setupRunner{outputs: []commandOutput{{value: []byte("stealth-onboarding-test\n")}}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	layout := newInstallLayout(t.TempDir())

	if err := app.closeQuickTunnel(context.Background(), layout, "stealth-onboarding-test"); err != nil {
		t.Fatal(err)
	}
	if call := runner.command(0); call.name != "docker" || !equalStrings(call.args, []string{"ps", "--all", "--filter", "name=^stealth-onboarding-test$", "--format", "{{.Names}}"}) {
		t.Fatalf("docker presence check command = %#v", call)
	}
	call := runner.command(1)
	if call.name != "docker" || !equalStrings(call.args, []string{"rm", "--force", "stealth-onboarding-test"}) {
		t.Fatalf("docker cleanup command = %#v", call)
	}
}

func TestCloseQuickTunnelIgnoresAlreadyAbsentContainer(t *testing.T) {
	runner := &setupRunner{outputs: []commandOutput{{value: []byte("")}}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	layout := newInstallLayout(t.TempDir())

	if err := app.closeQuickTunnel(context.Background(), layout, "stealth-onboarding-test"); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("already absent cleanup commands = %#v, want only presence check", runner.calls)
	}
}

func TestFormatSetupCountdownUsesActualRemainingDuration(t *testing.T) {
	for _, test := range []struct {
		remaining time.Duration
		want      string
	}{
		{remaining: 14*time.Minute + 59*time.Second, want: "14:59"},
		{remaining: 3*time.Second + 900*time.Millisecond, want: "00:03"},
		{remaining: 0, want: "00:00"},
	} {
		if got := formatSetupCountdown(test.remaining); got != test.want {
			t.Fatalf("formatSetupCountdown(%s) = %q, want %q", test.remaining, got, test.want)
		}
	}
}

func TestSetupModelClosesTunnelAfterOwnerCompletion(t *testing.T) {
	runner := &setupRunner{outputs: []commandOutput{{value: []byte("stealth-onboarding-test\n")}}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newSetupModel(app, ctx, cancel, newInstallLayout(t.TempDir()), nil, "http://127.0.0.1:18080", "http://127.0.0.1:8080/setup", &setupTunnelState{})
	model.phase = setupWaiting
	model.containerName = "stealth-onboarding-test"
	model.tunnelStarted = true

	updated, command := model.Update(setupStatusMessage{setupRequired: false})
	closing := updated.(setupModel)
	if closing.phase != setupClosing || command == nil {
		t.Fatalf("owner completion transition = phase %d, command %v; want closing and cleanup", closing.phase, command != nil)
	}
	cleanupMessage := command()
	updated, _ = closing.Update(cleanupMessage)
	complete := updated.(setupModel)
	if complete.phase != setupComplete || !complete.tunnelClosed || complete.cleanupErr != nil {
		t.Fatalf("cleanup transition = %#v; want completed closed tunnel", complete)
	}
	if len(runner.calls) != 2 || runner.command(1).name != "docker" || !equalStrings(runner.command(1).args, []string{"rm", "--force", "stealth-onboarding-test"}) {
		t.Fatalf("owner completion cleanup commands = %#v", runner.calls)
	}
}

func TestSetupModelCtrlCCancelsWithoutDeletingInstallation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := setupModel{ctx: ctx, cancel: cancel, phase: setupWaiting}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if command == nil {
		t.Fatal("Ctrl+C did not quit the setup UI")
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("Ctrl+C context error = %v, want canceled", ctx.Err())
	}
	if updated.(setupModel).phase != setupWaiting {
		t.Fatal("Ctrl+C unexpectedly changed setup state")
	}
}

func TestPrepareSetupReusesExistingTunnelWhenRefreshingExpiredCode(t *testing.T) {
	code := "STEALTH-ABCD-2345-EFGH"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/bootstrap/status":
			_, _ = writer.Write([]byte(`{"setup_required":true}`))
		case "/v1/bootstrap/sessions":
			_ = json.NewEncoder(writer).Encode(bootstrapSessionPayload{SetupCode: code, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	runner := &setupRunner{}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	key := bytes.Repeat([]byte{0x31}, 32)
	message := app.prepareSetup(context.Background(), newInstallLayout(t.TempDir()), map[string]string{"BOOTSTRAP_CLI_KEY": encodeTestSecret(key)}, server.URL, "http://127.0.0.1:8080/setup", "stealth-onboarding-test", "https://silent-moon.trycloudflare.com", nil)
	if message.err != nil || message.tunnelURL != "https://silent-moon.trycloudflare.com" || message.session.SetupCode != code {
		t.Fatalf("refreshed setup session = %#v", message)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("refresh unexpectedly started or inspected Docker tunnel: %#v", runner.calls)
	}
}

func TestBootstrapCLIKeyUsesDedicatedKeyAndLegacyFallback(t *testing.T) {
	dedicated := bytes.Repeat([]byte{0x11}, 32)
	legacy := bytes.Repeat([]byte{0x22}, 32)
	values := map[string]string{
		"BOOTSTRAP_CLI_KEY":    encodeTestSecret(dedicated),
		"FUNCTIONS_SECRET_KEY": encodeTestSecret(legacy),
	}
	key, err := bootstrapCLIKey(values)
	if err != nil || !bytes.Equal(key, dedicated) {
		t.Fatalf("dedicated bootstrap key = %x, %v", key, err)
	}
	delete(values, "BOOTSTRAP_CLI_KEY")
	key, err = bootstrapCLIKey(values)
	if err != nil || !bytes.Equal(key, legacy) {
		t.Fatalf("legacy bootstrap key = %x, %v", key, err)
	}
}

func TestSetupHelpDoesNotRequireAtty(t *testing.T) {
	var output strings.Builder
	app := NewApp(strings.NewReader(""), &output, &output)
	if code := app.runSetup([]string{"--help"}); code != 0 {
		t.Fatalf("setup --help exit code = %d, want 0", code)
	}
	if !strings.Contains(output.String(), "Resume first-run Instance Owner setup") {
		t.Fatalf("setup help = %q", output.String())
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func encodeTestSecret(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}
