package cli

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var (
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	subtitleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	secondaryStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	successStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warningStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	destructiveStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	cyanStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
	contentStyle     = lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
	panelStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("205")).Padding(0, 1)
	dangerPanelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("196")).Padding(0, 1)
)

func initTerminalStyles() {
	if os.Getenv("NO_COLOR") != "" || dumbTerminal() {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

func dumbTerminal() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb")
}
