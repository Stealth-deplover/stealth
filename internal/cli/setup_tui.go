package cli

// Setup TUI owns the interactive state machine and presentation. It invokes
// the workflow methods from setup.go through small Bubble Tea commands; it
// does not own bootstrap persistence or installation mutations.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

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
	containerName := final.containerName
	tunnelStarted := final.tunnelStarted
	if startedContainer := model.tunnelState.startedContainer(); startedContainer != "" {
		// Ctrl+C can arrive while asynchronous preparation is still in flight.
		// Shared state lets the caller clean up a container that was started but
		// whose prepared message was not rendered yet.
		containerName = startedContainer
		tunnelStarted = true
	}
	if tunnelStarted && containerName != "" && (!final.tunnelClosed || final.cleanupErr != nil) {
		if cleanupErr := a.closeQuickTunnel(cleanupContext, layout, containerName); cleanupErr != nil && final.cleanupErr == nil {
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
		builder.WriteString("○ Waiting for GitHub authorization\n\n")
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
		builder.WriteString("\nEnter the setup code on the page, then continue with GitHub to create the first Instance Owner.\n")
		builder.WriteString(m.spinner.View() + " Waiting for GitHub authorization…\n\n")
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
