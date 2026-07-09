// Package tui implements the gtl dashboard terminal UI.
package tui

import (
	"charm.land/lipgloss/v2"
)

var (
	subtle    = lipgloss.Color("#30363d")
	highlight = lipgloss.Color("#22c55e")
	accentLt  = lipgloss.Color("#4ade80")
	danger    = lipgloss.Color("#FF6666")
	dimmed    = lipgloss.Color("#8b949e")

	statusRunning = lipgloss.Color("#22c55e")
	statusStopped = lipgloss.Color("#FF6666")
	statusIdle    = lipgloss.Color("#FFAA33")

	panelBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle)

	panelBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(highlight)

	panelTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight).
			PaddingLeft(1)

	projectHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EEEEEE"))

	selectedRow = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight)

	normalRow = lipgloss.NewStyle()

	portStyle = lipgloss.NewStyle().
			Foreground(dimmed)

	detailLabel = lipgloss.NewStyle().
			Bold(true).
			Width(14)

	detailValue = lipgloss.NewStyle()

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			Background(lipgloss.Color("#151d28")).
			PaddingLeft(1).
			PaddingRight(1)

	helpKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentLt)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8b949e"))

	linkIndicatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFAA33"))
)
