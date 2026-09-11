package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type installerScreen int

const (
	installerWelcome installerScreen = iota
	installerInstance
	installerInfrastructure
	installerNetwork
	installerGitHub
	installerReview
	installerInstalling
	installerComplete
	installerFailed
)

type installStepMessage struct {
	err error
}

type installerModel struct {
	app               *App
	ctx               context.Context
	cancel            context.CancelFunc
	checks            []SystemCheck
	plan              *InstallPlan
	repair            bool
	screen            installerScreen
	urlInput          textinput.Model
	githubInput       textinput.Model
	publicURL         string
	githubAppClientID string
	version           string
	step              int
	err               error
	width             int
}

func (a *App) runInstallerTUI(ctx context.Context, checks []SystemCheck, plan *InstallPlan, repair bool) int {
	initTerminalStyles()
	uiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := newInstallerModel(a, uiCtx, cancel, checks, plan, repair)
	program := tea.NewProgram(model, tea.WithInput(a.in), tea.WithOutput(a.out))
	finalModel, err := program.Run()
	if err != nil {
		fmt.Fprintf(a.errOut, "installer UI failed: %v\n", err)
		return 1
	}
	final, ok := finalModel.(installerModel)
	if !ok {
		return 1
	}
	if final.err != nil {
		fmt.Fprintf(a.errOut, "\nInstallation stopped: %v\n", final.err)
		if regularFile(plan.Layout.EnvFile) {
			fmt.Fprintf(a.errOut, "Configuration was preserved at: %s\n", plan.Layout.EnvFile)
		} else {
			fmt.Fprintln(a.errOut, "No configuration was written before the failure.")
		}
		fmt.Fprintln(a.errOut, "Run `stealth doctor` or `stealth install --repair` for the next step.")
		return 1
	}
	if final.screen != installerComplete {
		fmt.Fprintln(a.errOut, "Installation cancelled.")
		return 1
	}
	return 0
}

func newInstallerModel(app *App, ctx context.Context, cancel context.CancelFunc, checks []SystemCheck, plan *InstallPlan, repair bool) installerModel {
	input := textinput.New()
	input.Prompt = "Instance URL  "
	input.Placeholder = "https://stealth.example.com"
	input.CharLimit = 2048
	input.Width = 64
	githubInput := textinput.New()
	githubInput.Prompt = "GitHub App Client ID  "
	githubInput.Placeholder = "Iv1.xxxxxxxxxxxxxxxx"
	githubInput.CharLimit = 160
	githubInput.Width = 64
	model := installerModel{
		app:         app,
		ctx:         ctx,
		cancel:      cancel,
		checks:      checks,
		plan:        plan,
		repair:      repair,
		screen:      installerWelcome,
		urlInput:    input,
		githubInput: githubInput,
		version:     plan.Version,
	}
	if repair {
		model.publicURL = plan.PublicURL
		model.githubAppClientID = plan.GitHubAppClientID
		model.screen = installerReview
	}
	return model
}

func (m installerModel) Init() tea.Cmd {
	if m.screen == installerInstance {
		return m.urlInput.Focus()
	}
	if m.screen == installerGitHub {
		return m.githubInput.Focus()
	}
	return nil
}

func (m installerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		return m, nil
	case installStepMessage:
		if message.err != nil {
			m.err = message.err
			m.screen = installerFailed
			return m, nil
		}
		m.step++
		if m.step >= len(installStepNames) {
			m.screen = installerComplete
			return m, nil
		}
		return m, m.runCurrentStep()
	case tea.KeyMsg:
		if message.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			m.err = context.Canceled
			m.screen = installerFailed
			return m, tea.Quit
		}
		if m.screen == installerInstalling {
			return m, nil
		}
		if (m.screen == installerInstance || m.screen == installerGitHub) && message.Type != tea.KeyEnter && message.Type != tea.KeyEscape {
			var command tea.Cmd
			if m.screen == installerInstance {
				m.urlInput, command = m.urlInput.Update(message)
			} else {
				m.githubInput, command = m.githubInput.Update(message)
			}
			return m, command
		}
		switch message.Type {
		case tea.KeyEscape:
			return m.goBack()
		case tea.KeyEnter:
			return m.advance()
		}
		if (m.screen == installerWelcome || m.screen == installerComplete || m.screen == installerFailed) && (message.String() == "q" || message.String() == "esc") {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m installerModel) advance() (tea.Model, tea.Cmd) {
	switch m.screen {
	case installerWelcome:
		if !checksPass(m.checks) {
			m.err = fmt.Errorf("preflight checks failed: %s", failedCheckSummary(m.checks))
			m.screen = installerFailed
			return m, nil
		}
		m.screen = installerInstance
		return m, m.urlInput.Focus()
	case installerInstance:
		urlValue, err := validatePublicURL(m.urlInput.Value())
		if err != nil {
			m.err = err
			return m, nil
		}
		m.publicURL = urlValue
		m.err = nil
		m.screen = installerInfrastructure
		return m, nil
	case installerInfrastructure:
		m.screen = installerNetwork
		return m, nil
	case installerNetwork:
		m.screen = installerGitHub
		return m, m.githubInput.Focus()
	case installerGitHub:
		githubAppClientID, err := validateGitHubAppClientID(m.githubInput.Value())
		if err != nil {
			m.err = err
			return m, nil
		}
		m.githubAppClientID = githubAppClientID
		m.err = nil
		m.screen = installerReview
		return m, nil
	case installerReview:
		if m.plan == nil || !m.plan.Existing {
			gid, err := dockerSocketGID("/var/run/docker.sock")
			if err != nil {
				m.err = fmt.Errorf("Docker socket is not accessible: %w", err)
				m.screen = installerFailed
				return m, nil
			}
			plan := newInstallPlan(m.appMustLayout(), m.version, m.publicURL, gid)
			plan.GitHubAppClientID = m.githubAppClientID
			m.plan = ptr(plan)
		}
		m.step = 0
		m.screen = installerInstalling
		return m, m.runCurrentStep()
	case installerComplete, installerFailed:
		return m, tea.Quit
	}
	return m, nil
}

func (m installerModel) goBack() (tea.Model, tea.Cmd) {
	switch m.screen {
	case installerWelcome:
		return m, tea.Quit
	case installerInstance:
		m.screen = installerWelcome
	case installerInfrastructure:
		m.screen = installerInstance
		return m, m.urlInput.Focus()
	case installerNetwork:
		m.screen = installerInfrastructure
	case installerGitHub:
		m.screen = installerNetwork
	case installerReview:
		if !m.repair {
			m.screen = installerGitHub
			return m, m.githubInput.Focus()
		}
	case installerComplete, installerFailed:
		return m, tea.Quit
	}
	return m, nil
}

func (m installerModel) appMustLayout() InstallLayout {
	if m.plan != nil {
		return m.plan.Layout
	}
	layout, _ := m.app.layout()
	return layout
}

func (m installerModel) runCurrentStep() tea.Cmd {
	step := m.step
	plan := *m.plan
	return func() tea.Msg {
		return installStepMessage{err: m.app.installStep(m.ctx, plan, step)}
	}
}

func ptr[T any](value T) *T {
	return &value
}

func (m installerModel) View() string {
	return renderInstallerView(m)
}

func renderInstallerView(m installerModel) string {
	var builder strings.Builder
	builder.WriteString(renderTitle("STEALTH"))
	builder.WriteString("\n")
	builder.WriteString(renderSubtitle("Developer Cloud Control Plane"))
	builder.WriteString("\n\n")
	switch m.screen {
	case installerWelcome:
		builder.WriteString("System checks\n\n")
		for _, check := range m.checks {
			builder.WriteString(renderCheck(check))
			builder.WriteByte('\n')
		}
		builder.WriteString("\nPress Enter to continue · Esc to cancel")
	case installerInstance:
		builder.WriteString("Instance configuration\n\n")
		builder.WriteString("Choose the URL operators will use to reach the Console.\n")
		builder.WriteString(m.urlInput.View())
		builder.WriteString("\n")
		if m.err != nil {
			builder.WriteString(renderError(m.err.Error()))
			builder.WriteByte('\n')
		}
		builder.WriteString("\nEnter to continue · Esc to go back")
	case installerInfrastructure:
		builder.WriteString("Infrastructure\n\n")
		builder.WriteString("● PostgreSQL     Bundled\n")
		builder.WriteString("● Redis          Bundled\n")
		builder.WriteString("● Object storage Local persistent volume\n\n")
		builder.WriteString("Bundled infrastructure uses the versioned production Compose stack.\n")
		builder.WriteString("External providers remain operator configuration for a later setup flow.\n\n")
		builder.WriteString("Enter to continue · Esc to go back")
	case installerNetwork:
		builder.WriteString("Network\n\n")
		builder.WriteString("● Local / existing reverse proxy\n\n")
		builder.WriteString("The bundled proxy binds to 127.0.0.1:8080.\n")
		builder.WriteString("Put TLS termination or an existing reverse proxy in front of it.\n")
		builder.WriteString("Cloudflare Tunnel is not required by this installer.\n\n")
		builder.WriteString("Enter to continue · Esc to go back")
	case installerGitHub:
		builder.WriteString("GitHub authentication\n\n")
		builder.WriteString("Stealth uses a GitHub App Device Flow to verify the first Instance Owner.\n")
		builder.WriteString("Enable Device Flow in the App settings; no TryCloudflare callback URL is needed.\n\n")
		builder.WriteString(m.githubInput.View())
		builder.WriteString("\n")
		if m.err != nil {
			builder.WriteString(renderError(m.err.Error()))
			builder.WriteByte('\n')
		}
		builder.WriteString("\nEnter to continue · Esc to go back")
	case installerReview:
		builder.WriteString("Review installation\n\n")
		builder.WriteString(fmt.Sprintf("Version           %s\n", m.version))
		builder.WriteString(fmt.Sprintf("URL               %s\n", valueOr(m.publicURL, "not set")))
		builder.WriteString(fmt.Sprintf("GitHub App ID     %s\n", valueOr(m.githubAppClientID, "not set")))
		builder.WriteString("PostgreSQL        Bundled\nRedis             Bundled\nObject Storage    Local volume\n")
		builder.WriteString(fmt.Sprintf("Install directory %s\n\n", m.appMustLayout().Root))
		builder.WriteString("Secrets will be generated and kept in config.env (mode 0600).\n")
		builder.WriteString("Install Stealth? Press Enter to continue · Esc to go back")
	case installerInstalling:
		builder.WriteString("Installing Stealth\n\n")
		for index, name := range installStepNames {
			mark := "○"
			if index < m.step {
				mark = "✓"
			} else if index == m.step {
				mark = "●"
			}
			builder.WriteString(fmt.Sprintf("%s %s\n", mark, name))
		}
		builder.WriteString("\nWorking with Docker Compose. This may take a few minutes.")
	case installerComplete:
		builder.WriteString("Installation complete\n\n")
		builder.WriteString("✓ Database migrations applied\n✓ API and worker running\n✓ Console and proxy verified\n\n")
		builder.WriteString(fmt.Sprintf("Open: %s\n\n", m.publicURL))
		builder.WriteString("Run `stealth status` to inspect services.\n")
		builder.WriteString("Press Enter to exit")
	case installerFailed:
		builder.WriteString("Installation stopped\n\n")
		if m.err != nil {
			builder.WriteString(renderError(m.err.Error()))
			builder.WriteString("\n\n")
		}
		builder.WriteString("Configuration is preserved.\n")
		builder.WriteString("Run `stealth doctor` for diagnostics.\n")
		builder.WriteString("Press Enter to exit")
	}
	return constrainWidth(builder.String(), m.width)
}

func renderTitle(value string) string {
	return titleStyle.Render(value)
}

func renderSubtitle(value string) string {
	return subtitleStyle.Render(value)
}

func renderError(value string) string {
	return errorStyle.Render("✗ " + value)
}

func constrainWidth(value string, width int) string {
	if width < 40 {
		return value
	}
	return contentStyle.Copy().Width(width - 4).Render(value)
}
