// Package style provides terminal color styles for CLI output.
// Uses ANSI base colors so output respects the user's terminal theme.
// Lipgloss auto-detects capability; colors are stripped when piped.
// Respects NO_COLOR env var out of the box.
package style

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

var (
	Bold   = lipgloss.NewStyle().Bold(true)                 // standalone headings ("Next steps:")
	Action = lipgloss.NewStyle().Bold(true)                 // ==> action prefix (same visual, distinct semantic)
	Command = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	Warn    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	Err     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	Success = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	Dim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	URL     = lipgloss.NewStyle().Underline(true).Foreground(lipgloss.Color("14"))
)

// ActionPrefix renders the "==>" prefix in the Action style.
func ActionPrefix() string {
	return Action.Render("==>")
}

// Actionf formats and prints an action line: "==> message".
func Actionf(format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	return ActionPrefix() + " " + msg
}

// Warnf formats a warning line: "Warning: message" with colored prefix.
func Warnf(format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	return Warn.Render("Warning:") + " " + msg
}

// Errf formats an error line: "Error: message" with colored prefix.
func Errf(format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	return Err.Render("Error:") + " " + msg
}

// Successf formats a success line with green styling.
func Successf(format string, args ...any) string {
	return Success.Render(fmt.Sprintf(format, args...))
}

// Dimf formats a dim/subordinate line.
func Dimf(format string, args ...any) string {
	return Dim.Render(fmt.Sprintf(format, args...))
}

// Cmd renders a command name in cyan bold for use in instructions.
func Cmd(s string) string {
	return Command.Render(s)
}

// Link renders a URL with underline.
func Link(s string) string {
	return URL.Render(s)
}
