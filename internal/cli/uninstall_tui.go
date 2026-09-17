package cli

// Uninstall TUI owns guided selection, purge confirmation, progress state,
// and rendering. The safety plan and effects remain in uninstall.go and are
// shared with plain/non-TTY execution.

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type uninstallScreen int

const (
	uninstallMenu uninstallScreen = iota
	uninstallPlanScreen
	uninstallPurgeConfirmation
	uninstallRemoving
	uninstallComplete
	uninstallCancelled
	uninstallFailed
)

type uninstallStepMessage struct {
	err error
}

type uninstallModel struct {
	app          *App
	ctx          context.Context
	cancel       context.CancelFunc
	plan         uninstallPlan
	screen       uninstallScreen
	option       int
	step         int
	spinner      spinner.Model
	confirmInput textinput.Model
	err          error
	width        int
	options      uninstallOptions
}

func (a *App) runUninstallTUI(ctx context.Context, plan uninstallPlan, options uninstallOptions) int {
	uiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if options.modeSet && options.yes && !options.dryRun {
		printUninstallPlan(a.out, plan)
	}
	model := newUninstallModel(a, uiCtx, cancel, plan, options)
	program := tea.NewProgram(model, tea.WithInput(a.in), tea.WithOutput(a.out))
	finalModel, err := program.Run()
	if err != nil {
		fmt.Fprintf(a.errOut, "uninstall UI failed: %v\n", err)
		return 1
	}
	final, ok := finalModel.(uninstallModel)
	if !ok {
		return 1
	}
	switch final.screen {
	case uninstallComplete:
		return 0
	case uninstallCancelled:
		fmt.Fprintln(a.errOut, "Uninstall cancelled. No further changes were made.")
		return 0
	case uninstallFailed:
		a.printUninstallFailure(final.plan, final.err)
		return 1
	default:
		fmt.Fprintln(a.errOut, "Uninstall cancelled. No changes were made.")
		return 0
	}
}

func newUninstallModel(app *App, ctx context.Context, cancel context.CancelFunc, plan uninstallPlan, options uninstallOptions) uninstallModel {
	confirm := textinput.New()
	confirm.Prompt = "> "
	confirm.CharLimit = len("stealth")
	confirm.Width = 24
	spin := spinner.New()
	model := uninstallModel{
		app:          app,
		ctx:          ctx,
		cancel:       cancel,
		plan:         plan,
		screen:       uninstallMenu,
		spinner:      spin,
		confirmInput: confirm,
		options:      options,
	}
	if options.modeSet {
		model.plan.mode = options.mode
		model.screen = uninstallPlanScreen
		if options.dryRun {
			return model
		}
		if options.yes {
			model.screen = uninstallRemoving
		}
	}
	return model
}

func (m uninstallModel) Init() tea.Cmd {
	if m.screen == uninstallPurgeConfirmation {
		return m.confirmInput.Focus()
	}
	if m.screen == uninstallRemoving {
		return tea.Batch(m.spinner.Tick, m.runCurrentStep())
	}
	return nil
}

func (m uninstallModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		return m, nil
	case spinner.TickMsg:
		if m.screen != uninstallRemoving {
			return m, nil
		}
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		return m, command
	case uninstallStepMessage:
		if message.err != nil {
			m.err = message.err
			m.screen = uninstallFailed
			return m, nil
		}
		m.step++
		operations := m.app.uninstallOperations(m.plan)
		if m.step >= len(operations) {
			m.screen = uninstallComplete
			return m, nil
		}
		return m, m.runCurrentStep()
	case tea.KeyMsg:
		if message.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			m.screen = uninstallCancelled
			return m, tea.Quit
		}
		switch m.screen {
		case uninstallMenu:
			return m.updateMenu(message)
		case uninstallPlanScreen:
			return m.updatePlan(message)
		case uninstallPurgeConfirmation:
			return m.updatePurgeConfirmation(message)
		case uninstallComplete, uninstallCancelled, uninstallFailed:
			if message.Type == tea.KeyEnter || message.String() == "q" || message.String() == "esc" {
				return m, tea.Quit
			}
		}
	}
	if m.screen == uninstallPurgeConfirmation {
		var command tea.Cmd
		m.confirmInput, command = m.confirmInput.Update(message)
		return m, command
	}
	return m, nil
}

func (m uninstallModel) updateMenu(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	const optionCount = 4
	if message.Type == tea.KeyEscape {
		m.screen = uninstallCancelled
		return m, tea.Quit
	}
	switch message.Type {
	case tea.KeyUp:
		m.option = (m.option + optionCount - 1) % optionCount
	case tea.KeyDown:
		m.option = (m.option + 1) % optionCount
	case tea.KeyEnter:
		if m.option == optionCount-1 {
			m.screen = uninstallCancelled
			return m, tea.Quit
		}
		m.plan.mode = uninstallMode(m.option)
		m.screen = uninstallPlanScreen
	}
	return m, nil
}

func (m uninstallModel) updatePlan(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.options.dryRun {
		if message.Type == tea.KeyEnter || message.Type == tea.KeyEscape || message.String() == "q" {
			m.screen = uninstallCancelled
			return m, tea.Quit
		}
		return m, nil
	}
	if message.Type == tea.KeyEscape || message.String() == "n" || message.String() == "N" {
		m.screen = uninstallCancelled
		return m, tea.Quit
	}
	if m.plan.mode == uninstallPurge {
		if message.Type == tea.KeyEnter {
			m.screen = uninstallPurgeConfirmation
			return m, m.confirmInput.Focus()
		}
		return m, nil
	}
	if message.Type == tea.KeyEnter {
		m.screen = uninstallCancelled
		return m, tea.Quit
	}
	if message.String() == "y" || message.String() == "Y" {
		return m.startRemoval()
	}
	return m, nil
}

func (m uninstallModel) updatePurgeConfirmation(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	if message.Type == tea.KeyEscape {
		m.screen = uninstallCancelled
		return m, tea.Quit
	}
	if message.Type == tea.KeyEnter {
		if m.confirmInput.Value() != "stealth" {
			m.screen = uninstallCancelled
			return m, tea.Quit
		}
		return m.startRemoval()
	}
	var command tea.Cmd
	m.confirmInput, command = m.confirmInput.Update(message)
	return m, command
}

func (m uninstallModel) startRemoval() (tea.Model, tea.Cmd) {
	if m.plan.mode == uninstallPurge && !m.plan.canPurge() {
		m.err = fmt.Errorf("purge is not safe for this partial or unrecognized installation")
		m.screen = uninstallFailed
		return m, nil
	}
	m.step = 0
	m.screen = uninstallRemoving
	return m, tea.Batch(m.spinner.Tick, m.runCurrentStep())
}

func (m uninstallModel) runCurrentStep() tea.Cmd {
	operations := m.app.uninstallOperations(m.plan)
	step := m.step
	return func() tea.Msg {
		if step >= len(operations) {
			return uninstallStepMessage{}
		}
		return uninstallStepMessage{err: operations[step].action(m.ctx)}
	}
}

func (m uninstallModel) View() string {
	var builder strings.Builder
	builder.WriteString(renderTitle("STEALTH"))
	builder.WriteString("\n")
	builder.WriteString(renderSubtitle("Developer Cloud Control Plane"))
	builder.WriteString("\n\n")
	switch m.screen {
	case uninstallMenu:
		builder.WriteString(renderUninstallMenu(m))
	case uninstallPlanScreen:
		builder.WriteString(renderUninstallPlan(m))
	case uninstallPurgeConfirmation:
		builder.WriteString(renderUninstallPurgeConfirmation(m))
	case uninstallRemoving:
		builder.WriteString(renderUninstallProgress(m))
	case uninstallComplete:
		builder.WriteString(renderUninstallComplete(m))
	case uninstallCancelled:
		builder.WriteString("Uninstall cancelled\n\nNo changes were made.\n\nPress Enter to exit")
	case uninstallFailed:
		builder.WriteString("Uninstall incomplete\n\n")
		if m.err != nil {
			builder.WriteString(renderError(m.err.Error()))
			builder.WriteString("\n\n")
		}
		builder.WriteString("No further destructive cleanup was attempted.\n")
		builder.WriteString("Run `stealth doctor` for diagnostics.\n\nPress Enter to exit")
	}
	return constrainWidth(builder.String(), m.width)
}

func renderUninstallMenu(m uninstallModel) string {
	var builder strings.Builder
	builder.WriteString(renderPanel("Uninstall Stealth", "Choose what you want to remove", false))
	builder.WriteString("\n\n")
	builder.WriteString(fmt.Sprintf("Instance       %s\n\n", m.plan.layout.Root))
	options := []struct {
		name   string
		detail string
	}{
		{"Remove services only", "Preserve database, storage, configuration, and secrets"},
		{"Remove services + local configuration", "Preserve data; keep config.env for recovery"},
		{"Purge everything", "Permanently delete project-owned data and secrets"},
		{"Cancel", "Leave the Stealth installation unchanged"},
	}
	for index, option := range options {
		marker := "  "
		name := option.name
		if index == m.option {
			marker = "› "
			name = secondaryStyle.Render(name)
		}
		if index == 2 {
			name = destructiveStyle.Render(name)
		}
		builder.WriteString(marker + name + "\n")
		builder.WriteString("    " + mutedStyle.Render(option.detail) + "\n")
	}
	builder.WriteString("\n↑/↓ to choose · Enter to continue · Esc to cancel")
	return builder.String()
}

func renderUninstallPlan(m uninstallModel) string {
	destructive := m.plan.mode == uninstallPurge
	var builder strings.Builder
	builder.WriteString(renderPanel("Removal plan", uninstallModeName(m.plan.mode), destructive))
	builder.WriteString("\n\n")
	builder.WriteString(fmt.Sprintf("Instance       %s\n", m.plan.layout.Root))
	if m.plan.partial {
		builder.WriteString(warningStyle.Render("! Partial installation detected") + "\n")
	}
	builder.WriteString("\nWill remove\n")
	for _, item := range m.plan.removalItems() {
		mark := successStyle.Render("✓")
		if destructive {
			mark = destructiveStyle.Render("✗")
		}
		builder.WriteString(fmt.Sprintf("%s %s\n", mark, item))
	}
	builder.WriteString("\nWill preserve\n")
	for _, item := range m.plan.preservedItems() {
		builder.WriteString(fmt.Sprintf("%s %s\n", successStyle.Render("✓"), item))
	}
	if warnings := m.plan.warnings(); len(warnings) > 0 {
		builder.WriteString("\n")
		for _, warning := range warnings {
			builder.WriteString(warningStyle.Render("! " + warning))
			builder.WriteByte('\n')
		}
	}
	if m.options.dryRun {
		builder.WriteString("\n")
		builder.WriteString(secondaryStyle.Render("Dry run: no changes will be made."))
		builder.WriteString("\n\nPress Enter to exit")
	} else if destructive {
		builder.WriteString("\n")
		builder.WriteString(destructiveStyle.Render("Permanent deletion requires a second confirmation."))
		builder.WriteString("\n\nPress Enter to continue · Esc to cancel")
	} else {
		builder.WriteString("\nContinue? [y/N]  ")
		builder.WriteString(mutedStyle.Render("Press y to continue · Esc to cancel"))
	}
	return builder.String()
}

func renderUninstallPurgeConfirmation(m uninstallModel) string {
	body := "This will permanently delete this Stealth instance\nand all locally stored project-owned data.\n\nType \"stealth\" to confirm:\n" + m.confirmInput.View()
	return renderPanel("Permanent data deletion", body, true)
}

func renderUninstallProgress(m uninstallModel) string {
	var builder strings.Builder
	title := "Removing Stealth"
	if m.plan.mode == uninstallPurge {
		title = "Purging Stealth"
	}
	builder.WriteString(renderPanel(title, "Applying the confirmed removal plan", m.plan.mode == uninstallPurge))
	builder.WriteString("\n\n")
	operations := m.app.uninstallOperations(m.plan)
	for index, operation := range operations {
		mark := mutedStyle.Render("○")
		if index < m.step {
			mark = successStyle.Render("✓")
		} else if index == m.step {
			mark = secondaryStyle.Render(m.spinner.View())
		}
		builder.WriteString(fmt.Sprintf("%s %s\n", mark, operation.name))
	}
	builder.WriteString(fmt.Sprintf("\n%d / %d", min(m.step+1, len(operations)), len(operations)))
	return builder.String()
}

func renderUninstallComplete(m uninstallModel) string {
	var builder strings.Builder
	builder.WriteString(renderPanel("Uninstall complete", "The confirmed removal plan finished successfully", false))
	builder.WriteString("\n\n")
	builder.WriteString(successStyle.Render("✓ ") + "Services and requested local resources removed\n")
	if m.plan.mode == uninstallPurge {
		builder.WriteString(successStyle.Render("✓ ") + "Project-owned persistent data deleted\n")
	} else {
		builder.WriteString(successStyle.Render("✓ ") + "Persistent data preserved\n")
	}
	builder.WriteString("\nThe Stealth CLI is still installed.\nPress Enter to exit")
	return builder.String()
}

func renderPanel(title, body string, destructive bool) string {
	style := panelStyle
	if destructive {
		style = dangerPanelStyle
		title = destructiveStyle.Render(title)
	} else {
		title = titleStyle.Render(title)
	}
	return style.Render(title + "\n" + body)
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
