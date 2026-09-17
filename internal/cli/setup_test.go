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
	mu              sync.Mutex
	calls           []recordedCommand
	outputs         []commandOutput
	combinedOutputs []commandOutput
	runErr          error
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

func (r *setupRunner) CombinedOutput(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(r.combinedOutputs) == 0 {
		return nil, nil
	}
	result := r.combinedOutputs[0]
	r.combinedOutputs = r.combinedOutputs[1:]
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

func TestFindQuickTunnelURL(t *testing.T) {
	tests := []struct {
		name  string
		logs  string
		want  string
		found bool
	}{
		{
			name:  "url written to stdout",
			logs:  "INF Requesting new quick Tunnel\nhttps://abc-def.trycloudflare.com\nINF Connected\n",
			want:  "https://abc-def.trycloudflare.com",
			found: true,
		},
		{
			name:  "url only visible in combined output",
			logs:  "INF Requesting new quick Tunnel\nhttps://stderr-only.trycloudflare.com\nINF Connected\n",
			want:  "https://stderr-only.trycloudflare.com",
			found: true,
		},
		{
			name:  "noisy logs with inline url",
			logs:  "INF Starting tunnel\nWRN retrying edge discovery\nINF Your quick Tunnel has been created! url=https://abc.trycloudflare.com\nINF Connected\n",
			want:  "https://abc.trycloudflare.com",
			found: true,
		},
		{name: "insecure scheme is rejected", logs: "http://abc.trycloudflare.com\n", found: false},
		{name: "unrelated host is rejected", logs: "https://example.com\n", found: false},
		{name: "lookalike suffix is rejected", logs: "https://trycloudflare.com.evil.example\n", found: false},
		{name: "no url present", logs: "INF Starting tunnel\n", found: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, found := findQuickTunnelURL([]byte(test.logs))
			if found != test.found || got != test.want {
				t.Fatalf("findQuickTunnelURL() = %q, %v; want %q, %v", got, found, test.want, test.found)
			}
		})
	}
}

func TestStartQuickTunnelUsesPinnedImageAndOnlyProxyNetwork(t *testing.T) {
	runner := &setupRunner{
		outputs: []commandOutput{
			{value: []byte("container-id\n")},
			{value: []byte("running\n")},
		},
		combinedOutputs: []commandOutput{
			{value: []byte("INF https://silent-moon.trycloudflare.com\n")},
		},
	}
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

// TestStartQuickTunnelDetectsURLFromCombinedLogs reproduces the fresh-install
// regression where cloudflared wrote the TryCloudflare URL to stderr, so the
// previous exec.Cmd.Output()-based docker logs read never observed it.
func TestStartQuickTunnelDetectsURLFromCombinedLogs(t *testing.T) {
	runner := &setupRunner{
		outputs: []commandOutput{{value: []byte("container-id\n")}},
		combinedOutputs: []commandOutput{
			{value: []byte("INF Requesting new quick Tunnel\nhttps://stderr-only.trycloudflare.com\nINF Connected\n")},
		},
	}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	app.pollAttempts = 3
	app.pollInterval = 0
	layout := newInstallLayout(t.TempDir())

	got, err := app.startQuickTunnel(context.Background(), layout, "stealth_network", "stealth-onboarding-test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://stderr-only.trycloudflare.com" {
		t.Fatalf("tunnel URL = %q, want URL from combined output", got)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("commands = %#v, want only docker run and logs", runner.calls)
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

// TestStartQuickTunnelReturnsEarlyWhenContainerExits verifies that a crashed
// cloudflared container is detected immediately instead of waiting for the
// full startup deadline, and that the collected log excerpt is sanitized.
func TestStartQuickTunnelReturnsEarlyWhenContainerExits(t *testing.T) {
	runner := &setupRunner{
		outputs: []commandOutput{
			{value: []byte("container-id\n")},
			{value: []byte("exited\n")},
		},
		combinedOutputs: []commandOutput{
			{value: []byte("WRN failed to connect to the edge\nERR token=super-secret-tunnel-token\n")},
		},
	}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	app.pollAttempts = 60
	app.pollInterval = 0
	layout := newInstallLayout(t.TempDir())

	name, err := app.startQuickTunnel(context.Background(), layout, "stealth_network", "stealth-onboarding-test")
	if err == nil || !strings.Contains(err.Error(), "exited before publishing a setup URL") {
		t.Fatalf("startQuickTunnel() = %q, %v; want early-exit error", name, err)
	}
	if !strings.Contains(err.Error(), "cloudflared:") {
		t.Fatalf("early-exit error is missing the log excerpt: %v", err)
	}
	if strings.Contains(err.Error(), "super-secret-tunnel-token") {
		t.Fatalf("early-exit error leaked a tunnel credential: %v", err)
	}
	if !strings.Contains(err.Error(), "token=[redacted]") {
		t.Fatalf("early-exit error did not sanitize the credential: %v", err)
	}
	// docker run, one docker logs, and one docker inspect: the deadline must
	// not be exhausted once the container is known to have exited.
	if len(runner.calls) != 3 {
		t.Fatalf("commands = %#v, want immediate exit after run/logs/inspect", runner.calls)
	}
}

func TestStartQuickTunnelTimesOutWithoutAURL(t *testing.T) {
	runner := &setupRunner{
		outputs: []commandOutput{
			{value: []byte("container-id\n")},
			{value: []byte("running\n")},
			{value: []byte("running\n")},
		},
		combinedOutputs: []commandOutput{
			{value: []byte("still starting\n")},
			{value: []byte("still starting\n")},
		},
	}
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
	if len(runner.calls) != 5 {
		t.Fatalf("commands = %#v, want a bounded two-iteration poll", runner.calls)
	}
}

func TestSanitizeTunnelLogsRedactsSecretsAndBoundsOutput(t *testing.T) {
	logs := "INF connected\nAuthorization: Bearer abc.def.ghi\npassword=hunter2\napi_key: \"live-key\"\nplain diagnostic line\n"
	got := sanitizeTunnelLogs([]byte(logs))
	for _, secret := range []string{"hunter2", "live-key", "abc.def.ghi"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitizeTunnelLogs() leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "plain diagnostic line") {
		t.Fatalf("sanitizeTunnelLogs() dropped useful diagnostics: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("sanitizeTunnelLogs() did not mark redactions: %q", got)
	}

	long := strings.Repeat("noise line\n", 200)
	bounded := sanitizeTunnelLogs([]byte(long))
	if lines := strings.Count(bounded, "\n") + 1; lines > maxTunnelLogLines {
		t.Fatalf("sanitizeTunnelLogs() kept %d lines, want <= %d", lines, maxTunnelLogLines)
	}
}

func TestPrintQuickTunnelFallbackExplainsSSHForwarding(t *testing.T) {
	var output strings.Builder
	app := NewApp(strings.NewReader(""), io.Discard, &output)
	app.printQuickTunnelFallback(errors.New("temporary Quick Tunnel exited before publishing a setup URL"))
	rendered := output.String()
	for _, want := range []string{
		"Quick Tunnel could not be started.",
		"still running securely on the VPS",
		"ssh -L 8081:127.0.0.1:8081 <user>@<server>",
		"http://localhost:8081/setup",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("fallback message missing %q:\n%s", want, rendered)
		}
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

func TestBootstrapCLIKeyUsesDedicatedKeyWithoutFunctionsFallback(t *testing.T) {
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
	if err == nil || key != nil || !strings.Contains(err.Error(), "dedicated bootstrap CLI key") {
		t.Fatalf("missing dedicated bootstrap key = %x, %v", key, err)
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
