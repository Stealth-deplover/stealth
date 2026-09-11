package cli

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	subtitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	cyanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
	contentStyle  = lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
)

func initTerminalStyles() {
	if os.Getenv("NO_COLOR") != "" || dumbTerminal() {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

func dumbTerminal() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb")
}
