package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

const (
	// Keep this image pinned. Quick Tunnels are a temporary onboarding
	// transport, not part of the production Compose stack.
	quickTunnelCloudflaredImage = "cloudflare/cloudflared:2026.9.0"
	quickTunnelNetwork          = "stealth_network"
	quickTunnelContainerPrefix  = "stealth-onboarding-"
	setupPollInterval           = 2 * time.Second
)

var quickTunnelURLPattern = regexp.MustCompile(`(?i)https://[a-z0-9][a-z0-9-]*\.trycloudflare\.com`)

type bootstrapHTTPError struct {
	status int
}

func (e *bootstrapHTTPError) Error() string {
	return fmt.Sprintf("bootstrap API returned HTTP %d", e.status)
}

type bootstrapStatusPayload struct {
	SetupRequired bool `json:"setup_required"`
}

type bootstrapSessionPayload struct {
	SetupCode string    `json:"setup_code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type setupPreparedMessage struct {
	session       bootstrapSessionPayload
	localURL      string
	tunnelURL     string
	containerName string
	warning       string
	complete      bool
	err           error
}

type setupStatusMessage struct {
	setupRequired bool
	err           error
}

type setupCleanupMessage struct {
	err error
}

type setupTickMessage time.Time

type setupPhase int

const (
	setupPreparing setupPhase = iota
	setupWaiting
	setupExpired
	setupClosing
	setupComplete
	setupFailed
)

type setupTunnelState struct {
	mu            sync.Mutex
	containerName string
}

func (s *setupTunnelState) markStarted(containerName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.containerName = containerName
	s.mu.Unlock()
}

func (s *setupTunnelState) startedContainer() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.containerName
}

type setupModel struct {
	app           *App
	ctx           context.Context
	cancel        context.CancelFunc
	layout        InstallLayout
	values        map[string]string
	apiURL        string
	localURL      string
	containerName string
	pollInterval  time.Duration
	phase         setupPhase
	session       bootstrapSessionPayload
	tunnelURL     string
	warning       string
	pollError     error
	err           error
	cleanupErr    error
	tunnelClosed  bool
	tunnelStarted bool
	tunnelState   *setupTunnelState
	spinner       spinner.Model
	width         int
}

func (a *App) runSetup(args []string) int {
	fs := flag.NewFlagSet("stealth setup", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	fs.Usage = func() {
		fmt.Fprintln(a.errOut, "Usage: stealth setup")
		fmt.Fprintln(a.errOut)
		fmt.Fprintln(a.errOut, "Resume first-run Instance Owner setup for an installed Stealth instance.")
		fmt.Fprintln(a.errOut, "The setup code expires after 15 minutes and the temporary onboarding tunnel is closed after use.")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.errOut, "setup does not accept positional arguments")
		return 2
	}
	if !a.hasInteractiveTerminal() {
		fmt.Fprintln(a.errOut, "First-run owner setup requires an interactive TTY.")
		fmt.Fprintln(a.errOut, "Run `stealth setup` from a terminal; the installation was left unchanged.")
		return 1
	}
	ctx, stop := signalContext()
	defer stop()
	return a.runSetupWithContext(ctx)
}

func (a *App) runSetupWithContext(ctx context.Context) int {
	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	if !installationExists(layout) {
		if partialInstallationExists(layout) {
			fmt.Fprintf(a.errOut, "A partial Stealth installation was found at %s, but config.env is missing.\n", layout.Root)
			fmt.Fprintln(a.errOut, "The installation was left unchanged. Run `stealth install --repair` or `stealth doctor` to recover it.")
			return 1
		}
		fmt.Fprintln(a.errOut, "No Stealth installation was found. Run `stealth install` first.")
		return 1
	}
	values, err := readEnvFile(layout.EnvFile)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read installation configuration: %v\n", err)
		return 1
	}
	ports := portsFromConfig(values)
	apiURL := "http://127.0.0.1:" + ports.API
	status, err := a.bootstrapStatus(ctx, apiURL+"/v1/bootstrap/status")
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read first-run setup state: %v\n", err)
		fmt.Fprintln(a.errOut, "The installation was left unchanged. Run `stealth doctor` for diagnostics.")
		return 1
	}
	if !status.SetupRequired {
		fmt.Fprintln(a.out, "Instance setup has already been completed.")
		return 0
	}
	if !a.hasInteractiveTerminal() {
		fmt.Fprintln(a.errOut, "First-run owner setup requires an interactive TTY.")
		return 1
	}
	return a.runSetupTUI(ctx, layout, values, apiURL, "http://127.0.0.1:"+ports.Proxy+"/setup")
}

// signalContext mirrors the install command's signal handling while keeping
// the setup flow independently resumable after a user cancellation.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func (a *App) runSetupTUI(ctx context.Context, layout InstallLayout, values map[string]string, apiURL, localURL string) int {
	initTerminalStyles()
	uiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := newSetupModel(a, uiCtx, cancel, layout, values, apiURL, localURL, &setupTunnelState{})
	program := tea.NewProgram(model, tea.WithInput(a.in), tea.WithOutput(a.out))
	finalModel, err := program.Run()
	if err != nil {
		fmt.Fprintf(a.errOut, "setup UI failed: %v\n", err)
		if containerName := model.tunnelState.startedContainer(); containerName != "" {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			cleanupErr := a.closeQuickTunnel(cleanupContext, layout, containerName)
			cleanupCancel()
			if cleanupErr != nil {
				fmt.Fprintf(a.errOut, "temporary onboarding tunnel cleanup failed: %v\n", cleanupErr)
				fmt.Fprintln(a.errOut, "The Stealth installation and its data were left intact. Remove only the temporary onboarding container before retrying.")
			}
		}
		return 1
	}
	final, ok := finalModel.(setupModel)
	if !ok {
		return 1
	}
	cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cleanupCancel()
	// The container name is generated locally and is never reused for an
	// unrelated resource. A final best-effort rm -f closes the race where Ctrl+C
	// arrives while docker is still creating the temporary container.
	if final.tunnelStarted && final.containerName != "" && (!final.tunnelClosed || final.cleanupErr != nil) {
		if cleanupErr := a.closeQuickTunnel(cleanupContext, layout, final.containerName); cleanupErr != nil && final.cleanupErr == nil {
			final.cleanupErr = cleanupErr
		}
	}
	if final.cleanupErr != nil {
		fmt.Fprintf(a.errOut, "temporary onboarding tunnel cleanup failed: %v\n", final.cleanupErr)
		fmt.Fprintln(a.errOut, "The Stealth installation and its data were left intact. Remove the temporary container before retrying.")
		return 1
	}
	if final.phase == setupComplete {
		fmt.Fprintln(a.out, "\nStealth owner setup is complete. The CLI remains installed.")
		return 0
	}
	if final.err != nil && !errors.Is(final.err, context.Canceled) {
		fmt.Fprintf(a.errOut, "\nFirst-run setup stopped: %v\n", final.err)
		fmt.Fprintln(a.errOut, "The installation and data were left intact. Run `stealth setup` to resume.")
		return 1
	}
	fmt.Fprintln(a.out, "\nFirst-run setup cancelled. The installation and data were left intact.")
	return 1
}

func newSetupModel(app *App, ctx context.Context, cancel context.CancelFunc, layout InstallLayout, values map[string]string, apiURL, localURL string, tunnelState *setupTunnelState) setupModel {
	model := setupModel{
		app:           app,
		ctx:           ctx,
		cancel:        cancel,
		layout:        layout,
		values:        values,
		apiURL:        apiURL,
		localURL:      localURL,
		containerName: newSetupContainerName(),
		pollInterval:  app.pollInterval,
		phase:         setupPreparing,
		tunnelState:   tunnelState,
		spinner:       spinner.New(),
	}
	model.spinner.Spinner = spinner.Dot
	model.spinner.Style = subtitleStyle
	return model
}

func (m setupModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.prepareCmd())
}

func (m setupModel) prepareCmd() tea.Cmd {
	return func() tea.Msg {
		return m.app.prepareSetup(m.ctx, m.layout, m.values, m.apiURL, m.localURL, m.containerName, m.tunnelURL, m.tunnelState)
	}
}

func (m setupModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		return m, nil
	case spinner.TickMsg:
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		return m, command
	case setupPreparedMessage:
		if message.err != nil {
			m.err = message.err
			m.phase = setupFailed
			return m, nil
		}
		if message.complete {
			if m.tunnelStarted && !m.tunnelClosed {
				m.phase = setupClosing
				return m, m.cleanupCmd()
			}
			m.phase = setupComplete
			m.tunnelClosed = true
			return m, nil
		}
		m.session = message.session
		m.localURL = message.localURL
		m.tunnelURL = message.tunnelURL
		m.containerName = message.containerName
		m.tunnelStarted = message.containerName != ""
		m.warning = message.warning
		m.pollError = nil
		m.phase = setupWaiting
		return m, tea.Batch(m.waitForOwnerCmd(), m.expirationCmd())
	case setupStatusMessage:
		if message.err != nil {
			m.pollError = message.err
			return m, m.waitForOwnerCmd()
		}
		m.pollError = nil
		if message.setupRequired {
			return m, m.waitForOwnerCmd()
		}
		m.phase = setupClosing
		return m, m.cleanupCmd()
	case setupCleanupMessage:
		m.cleanupErr = message.err
		m.tunnelClosed = message.err == nil
		if message.err != nil {
			m.phase = setupFailed
			m.err = fmt.Errorf("close temporary onboarding tunnel: %w", message.err)
			return m, nil
		}
		m.phase = setupComplete
		return m, nil
	case setupTickMessage:
		if m.phase != setupWaiting {
			return m, nil
		}
		if !time.Now().Before(m.session.ExpiresAt) {
			m.phase = setupExpired
			return m, nil
		}
		return m, m.expirationCmd()
	case tea.KeyMsg:
		if message.String() == "ctrl+c" || message.String() == "q" || message.String() == "esc" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		if m.phase == setupExpired && strings.EqualFold(message.String(), "r") {
			if m.containerName == "" {
				m.containerName = newSetupContainerName()
			}
			m.phase = setupPreparing
			m.err = nil
			m.warning = ""
			return m, m.prepareCmd()
		}
		if m.phase == setupComplete && (message.Type == tea.KeyEnter || message.String() == "q") {
			return m, tea.Quit
		}
		if m.phase == setupFailed && message.String() == "r" {
			if m.containerName == "" {
				m.containerName = newSetupContainerName()
			}
			m.phase = setupPreparing
			m.err = nil
			return m, m.prepareCmd()
		}
	}
	return m, nil
}

func (m setupModel) waitForOwnerCmd() tea.Cmd {
	interval := m.pollInterval
	if interval <= 0 {
		interval = setupPollInterval
	}
	return tea.Tick(interval, func(time.Time) tea.Msg {
		status, err := m.app.bootstrapStatus(m.ctx, m.apiURL+"/v1/bootstrap/status")
		return setupStatusMessage{setupRequired: status.SetupRequired, err: err}
	})
}

func (m setupModel) expirationCmd() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg { return setupTickMessage(now) })
}

func (m setupModel) cleanupCmd() tea.Cmd {
	return func() tea.Msg {
		return setupCleanupMessage{err: m.app.closeQuickTunnel(m.ctx, m.layout, m.containerName)}
	}
}

func newSetupContainerName() string {
	return quickTunnelContainerPrefix + strings.ToLower(uuid.NewString())
}

func (m setupModel) View() string {
	var builder strings.Builder
	builder.WriteString(renderTitle("STEALTH"))
	builder.WriteString("\n")
	builder.WriteString(renderSubtitle("First-run instance owner setup"))
	builder.WriteString("\n\n")
	switch m.phase {
	case setupPreparing:
		builder.WriteString("Preparing secure onboarding\n\n")
		builder.WriteString(m.spinner.View() + " Creating a temporary setup session\n")
		builder.WriteString("○ Temporary onboarding tunnel\n")
		builder.WriteString("○ Waiting for Instance Owner\n\n")
		builder.WriteString("The local installation remains unchanged while setup starts.")
	case setupWaiting:
		builder.WriteString("Create your Stealth owner\n\n")
		if m.tunnelURL != "" {
			builder.WriteString("Temporary onboarding tunnel\n")
			builder.WriteString("  " + cyanStyle.Render(m.tunnelURL+"/setup") + "\n\n")
		} else {
			builder.WriteString("Temporary tunnel unavailable; use the local setup page\n")
			builder.WriteString("  " + cyanStyle.Render(m.localURL) + "\n\n")
		}
		builder.WriteString("Setup code\n")
		builder.WriteString("  " + successStyle.Render(m.session.SetupCode) + "\n")
		builder.WriteString(fmt.Sprintf("\nExpires in %s\n", formatSetupCountdown(time.Until(m.session.ExpiresAt))))
		if m.warning != "" {
			builder.WriteString("\n" + warningStyle.Render("! "+m.warning) + "\n")
		}
		if m.pollError != nil {
			builder.WriteString("\n" + warningStyle.Render("! Waiting for the API; retrying automatically") + "\n")
		}
		builder.WriteString("\nEnter the code on the setup page to create the first Instance Owner.\n")
		builder.WriteString(m.spinner.View() + " Waiting for Instance Owner…\n\n")
		builder.WriteString(renderSubtitle("Ctrl+C cancels safely; data and configuration are preserved."))
	case setupExpired:
		builder.WriteString(warningStyle.Render("Setup code expired") + "\n\n")
		builder.WriteString("The expired code can no longer create an owner.\n")
		builder.WriteString("Press R to create a fresh 15-minute setup session, or Q to leave the installation intact.")
	case setupClosing:
		builder.WriteString("Instance Owner created\n\n")
		builder.WriteString("✓ Instance Owner created\n")
		builder.WriteString(m.spinner.View() + " Closing temporary onboarding tunnel\n")
	case setupComplete:
		builder.WriteString(successStyle.Render("✓ Instance Owner created") + "\n")
		builder.WriteString(successStyle.Render("✓ Temporary onboarding tunnel closed") + "\n\n")
		builder.WriteString("How should Stealth be exposed?\n\n")
		builder.WriteString("› Configure later\n")
		builder.WriteString("  Existing reverse proxy\n")
		builder.WriteString("  Cloudflare Tunnel\n")
		builder.WriteString("  Local only\n\n")
		builder.WriteString(renderSubtitle("Quick Tunnel was temporary onboarding only, not production ingress."))
		builder.WriteString("\n\nPress Enter to exit")
	case setupFailed:
		builder.WriteString("Setup stopped\n\n")
		if m.err != nil {
			builder.WriteString(renderError(m.err.Error()))
			builder.WriteString("\n\n")
		}
		builder.WriteString("No database, configuration, or owner data was deleted.\n")
		builder.WriteString("Press R to retry, or Q to exit and run `stealth setup` later.")
	}
	return constrainWidth(builder.String(), m.width)
}

func formatSetupCountdown(remaining time.Duration) string {
	if remaining <= 0 {
		return "00:00"
	}
	minutes := int(remaining / time.Minute)
	seconds := int((remaining % time.Minute) / time.Second)
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func (a *App) prepareSetup(ctx context.Context, layout InstallLayout, values map[string]string, apiURL, localURL, containerName, existingTunnelURL string, tunnelState *setupTunnelState) setupPreparedMessage {
	status, err := a.bootstrapStatus(ctx, apiURL+"/v1/bootstrap/status")
	if err != nil {
		if isBootstrapComplete(err) {
			return setupPreparedMessage{complete: true}
		}
		return setupPreparedMessage{err: err}
	}
	if !status.SetupRequired {
		return setupPreparedMessage{complete: true}
	}
	key, err := bootstrapCLIKey(values)
	if err != nil {
		return setupPreparedMessage{err: err}
	}
	session, err := a.createBootstrapSession(ctx, apiURL+"/v1/bootstrap/sessions", key)
	if err != nil {
		if isBootstrapComplete(err) {
			return setupPreparedMessage{complete: true}
		}
		return setupPreparedMessage{err: err}
	}
	message := setupPreparedMessage{session: session, localURL: localURL, containerName: containerName}
	if existingTunnelURL != "" {
		message.tunnelURL = existingTunnelURL
		return message
	}
	network := strings.TrimSpace(values["STEALTH_NETWORK_NAME"])
	if network == "" {
		network = quickTunnelNetwork
	}
	if !safeDockerName(network) {
		message.warning = "the temporary tunnel could not use the configured Docker network; continue with the local setup URL"
		message.containerName = ""
		return message
	}
	tunnelName, tunnelErr := a.startQuickTunnel(ctx, layout, network, containerName)
	if tunnelErr != nil {
		if tunnelName != "" {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cleanupErr := a.closeQuickTunnel(cleanupContext, layout, tunnelName)
			cancel()
			if cleanupErr != nil {
				return setupPreparedMessage{err: fmt.Errorf("temporary onboarding tunnel failed and could not be cleaned up: %w", cleanupErr)}
			}
		}
		message.warning = "the temporary tunnel was unavailable; continue with the local setup URL"
		message.containerName = ""
		return message
	}
	if tunnelState != nil {
		tunnelState.markStarted(containerName)
	}
	message.tunnelURL = tunnelName
	return message
}

func (a *App) bootstrapStatus(ctx context.Context, endpoint string) (bootstrapStatusPayload, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return bootstrapStatusPayload{}, err
	}
	response, err := a.httpClient.Do(request)
	if err != nil {
		return bootstrapStatusPayload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return bootstrapStatusPayload{}, &bootstrapHTTPError{status: response.StatusCode}
	}
	var payload bootstrapStatusPayload
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return bootstrapStatusPayload{}, fmt.Errorf("decode bootstrap status: %w", err)
	}
	return payload, nil
}

func (a *App) createBootstrapSession(ctx context.Context, endpoint string, key []byte) (bootstrapSessionPayload, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return bootstrapSessionPayload{}, err
	}
	request.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(key))
	response, err := a.httpClient.Do(request)
	if err != nil {
		return bootstrapSessionPayload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return bootstrapSessionPayload{}, &bootstrapHTTPError{status: response.StatusCode}
	}
	var payload bootstrapSessionPayload
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return bootstrapSessionPayload{}, fmt.Errorf("decode bootstrap session: %w", err)
	}
	if !bootstrap.ValidCode(payload.SetupCode) || !payload.ExpiresAt.After(time.Now().UTC()) {
		return bootstrapSessionPayload{}, fmt.Errorf("bootstrap API returned an invalid setup session")
	}
	return payload, nil
}

func bootstrapCLIKey(values map[string]string) ([]byte, error) {
	raw := strings.TrimSpace(values["BOOTSTRAP_CLI_KEY"])
	if raw == "" {
		raw = strings.TrimSpace(values["FUNCTIONS_SECRET_KEY"])
	}
	if raw == "" {
		return nil, fmt.Errorf("installation config has no bootstrap CLI key; preserve config.env and upgrade the installation before retrying")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("installation bootstrap CLI key is invalid")
	}
	return key, nil
}

func isBootstrapComplete(err error) bool {
	var statusErr *bootstrapHTTPError
	return errors.As(err, &statusErr) && statusErr.status == http.StatusGone
}

func (a *App) startQuickTunnel(ctx context.Context, layout InstallLayout, network, containerName string) (string, error) {
	if !isQuickTunnelContainerName(containerName) {
		return "", fmt.Errorf("temporary tunnel container name is invalid")
	}
	if _, err := a.runner.Output(ctx, layout.Root, "docker", "run", "--detach", "--name", containerName, "--network", network, "--pull=missing", quickTunnelCloudflaredImage, "tunnel", "--no-autoupdate", "--url", "http://proxy:80"); err != nil {
		return containerName, fmt.Errorf("start temporary onboarding tunnel: %w", err)
	}
	for attempt := 0; attempt < positiveAttempts(a.pollAttempts); attempt++ {
		if err := ctx.Err(); err != nil {
			return containerName, err
		}
		logs, err := a.runner.Output(ctx, layout.Root, "docker", "logs", containerName)
		if err == nil {
			if tunnelURL := parseQuickTunnelURL(logs); tunnelURL != "" {
				return tunnelURL, nil
			}
		}
		if attempt+1 < positiveAttempts(a.pollAttempts) {
			if err := waitSetupPoll(ctx, a.pollInterval); err != nil {
				return containerName, err
			}
		}
	}
	return containerName, fmt.Errorf("temporary onboarding tunnel did not publish a TryCloudflare URL in time")
}

func positiveAttempts(value int) int {
	if value < 1 {
		return 1
	}
	return value
}

func waitSetupPoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseQuickTunnelURL(output []byte) string {
	text := string(output)
	for _, indexes := range quickTunnelURLPattern.FindAllStringIndex(text, -1) {
		if end := indexes[1]; end < len(text) && isURLHostContinuation(text, end) {
			continue
		}
		match := text[indexes[0]:indexes[1]]
		parsed, err := url.Parse(match)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if strings.HasSuffix(host, ".trycloudflare.com") && strings.TrimSuffix(host, ".trycloudflare.com") != "" {
			return "https://" + host
		}
	}
	return ""
}

func isURLHostContinuation(text string, start int) bool {
	character := text[start]
	if character != '.' && character != '-' && (character < '0' || character > '9') && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
		return false
	}
	// A period followed by whitespace or punctuation can be ordinary sentence
	// punctuation. A period followed by another hostname character is a
	// lookalike suffix such as .evil and must not be silently truncated.
	if character == '.' && (start+1 == len(text) || !isURLHostContinuationCharacter(text[start+1])) {
		return false
	}
	return true
}

func isURLHostContinuationCharacter(character byte) bool {
	return character == '.' || character == '-' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func (a *App) closeQuickTunnel(ctx context.Context, layout InstallLayout, containerName string) error {
	if !isQuickTunnelContainerName(containerName) {
		return nil
	}
	containers, err := a.runner.Output(ctx, layout.Root, "docker", "ps", "--all", "--filter", "name=^"+containerName+"$", "--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("check temporary onboarding tunnel: %w", err)
	}
	found := false
	for _, candidate := range strings.Split(string(containers), "\n") {
		if strings.TrimSpace(candidate) == containerName {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	return a.runner.Run(ctx, layout.Root, io.Discard, io.Discard, "docker", "rm", "--force", containerName)
}

func isQuickTunnelContainerName(value string) bool {
	return strings.HasPrefix(value, quickTunnelContainerPrefix) && safeDockerName(value)
}

func safeDockerName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			if index == 0 && (character == '.' || character == '_' || character == '-') {
				return false
			}
			continue
		}
		return false
	}
	return true
}
