package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	listPanelRatio   = 0.40
	borderChrome     = 2 // left + right border
	statusBarHeight  = 2
	minContentHeight = 4
)

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if !m.ready {
		v := tea.NewView(panelTitle.Render("  loading dashboard..."))
		v.AltScreen = true
		return v
	}

	contentHeight := m.height - statusBarHeight - borderChrome
	if contentHeight < minContentHeight {
		contentHeight = minContentHeight
	}

	leftWidth := int(float64(m.width) * listPanelRatio)
	rightWidth := m.width - leftWidth
	innerLeft := leftWidth - borderChrome
	innerRight := rightWidth - borderChrome
	if innerLeft < 10 {
		innerLeft = 10
	}
	if innerRight < 10 {
		innerRight = 10
	}

	left := m.renderListPanel(innerLeft, contentHeight)
	right := m.renderDetailPanel(innerRight, contentHeight)

	leftBorder := panelBorder
	rightBorder := panelBorder
	if m.focus == paneList {
		leftBorder = panelBorderActive
	} else {
		rightBorder = panelBorderActive
	}

	leftBox := leftBorder.Width(innerLeft).Height(contentHeight).Render(left)
	rightBox := rightBorder.Width(innerRight).Height(contentHeight).Render(right)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftBox, rightBox)
	bar := m.renderStatusBar(m.width)

	content := lipgloss.JoinVertical(lipgloss.Left, panels, bar)

	if m.showHelp {
		content = m.renderHelpOverlay()
	}
	if m.confirmKind != "" {
		content = m.renderConfirmOverlay()
	}
	if m.inputMode != "" {
		content = m.renderInputOverlay()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// --- Left panel: worktree list ---

func (m *Model) renderListPanel(width, height int) string {
	var b strings.Builder
	title := panelTitle.Render("WORKTREES")
	if m.filterMode {
		title = panelTitle.Render(fmt.Sprintf("WORKTREES (/%s▌)", m.filterText))
	} else if m.filterText != "" {
		title = panelTitle.Render(fmt.Sprintf("WORKTREES [%s]", m.filterText))
	}
	b.WriteString(title)
	b.WriteString("\n")

	visibleRows := height - 1 // subtract title line
	rendered := 0

	for i := m.scrollOffset; i < len(m.flatList) && rendered < visibleRows; i++ {
		entry := m.flatList[i]
		if entry.projectHeader {
			b.WriteString("\n")
			b.WriteString(projectHeader.Render(truncate(entry.project, width)))
			b.WriteString("\n")
			rendered += 2
			continue
		}
		wt := entry.wt
		dot := statusDot(wt)
		port := portStyle.Render(fmt.Sprintf(":%d", primaryPort(wt)))
		linkBadge := ""
		if len(wt.Links) > 0 {
			linkBadge = " " + linkIndicatorStyle.Render("⇄")
		}
		label := truncate(wt.Branch, width-10)
		line := fmt.Sprintf("  %s %s %s%s", dot, port, label, linkBadge)

		if i == m.cursor {
			b.WriteString(selectedRow.Render(line))
		} else {
			b.WriteString(normalRow.Render(line))
		}
		b.WriteString("\n")
		rendered++
	}

	return b.String()
}

func statusDot(wt *WorktreeStatus) string {
	if wt.Listening {
		return lipgloss.NewStyle().Foreground(statusRunning).Render("●")
	}
	if wt.Supervisor == "running" {
		return lipgloss.NewStyle().Foreground(statusIdle).Render("●")
	}
	return lipgloss.NewStyle().Foreground(statusStopped).Render("○")
}

func primaryPort(wt *WorktreeStatus) int {
	if len(wt.Ports) > 0 {
		return wt.Ports[0]
	}
	return 0
}

// --- Right panel: detail view ---

func (m *Model) renderDetailPanel(width, height int) string {
	wt := m.selectedWorktree()
	if wt == nil {
		return panelTitle.Render("DETAIL") + "\n\n" +
			detailValue.Foreground(dimmed).Render("  Select a worktree")
	}

	var b strings.Builder
	header := fmt.Sprintf("%s / %s", wt.Project, wt.Branch)
	b.WriteString(panelTitle.Render(truncate(header, width)))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"Port", portsString(wt.Ports)},
	}

	if wt.RouterURL != "" {
		rows = append(rows, struct{ label, value string }{"URL", truncate(wt.RouterURL, width-16)})
	}
	if wt.TunnelURL != "" {
		rows = append(rows, struct{ label, value string }{"Tunnel", truncate(wt.TunnelURL, width-16)})
	}

	rows = append(rows,
		struct{ label, value string }{"Database", valueOrDash(wt.Database)},
		struct{ label, value string }{"Redis", redisDisplay(wt)},
		struct{ label, value string }{"Supervisor", supervisorDisplay(wt)},
		struct{ label, value string }{"Listening", boolDisplay(wt.Listening)},
		struct{ label, value string }{"Env file", valueOrDash(wt.EnvFile)},
		struct{ label, value string }{"Worktree", truncate(wt.WorktreePath, width-16)},
	)

	for _, r := range rows {
		b.WriteString("  ")
		b.WriteString(detailLabel.Render(r.label))
		b.WriteString(detailValue.Render(r.value))
		b.WriteString("\n")
	}

	if len(wt.Links) > 0 {
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(detailLabel.Render("Overrides"))
		b.WriteString("\n")
		linkKeys := make([]string, 0, len(wt.Links))
		for k := range wt.Links {
			linkKeys = append(linkKeys, k)
		}
		sort.Strings(linkKeys)
		for _, k := range linkKeys {
			b.WriteString("    ")
			b.WriteString(linkIndicatorStyle.Render("⇄ "+k))
			b.WriteString(detailValue.Render(" → "+wt.Links[k]))
			b.WriteString("\n")
		}
	}

	return b.String()
}

func portsString(ports []int) string {
	if len(ports) == 0 {
		return "—"
	}
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = fmt.Sprintf("%d", p)
	}
	return strings.Join(parts, ", ")
}

func valueOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func redisDisplay(wt *WorktreeStatus) string {
	if wt.RedisPrefix != "" {
		return fmt.Sprintf("%s (db %d)", wt.RedisPrefix, wt.RedisDB)
	}
	if wt.RedisDB > 0 {
		return fmt.Sprintf("db %d", wt.RedisDB)
	}
	return "—"
}

func supervisorDisplay(wt *WorktreeStatus) string {
	switch wt.Supervisor {
	case "running":
		return lipgloss.NewStyle().Foreground(statusRunning).Render("running")
	case "stopped":
		return lipgloss.NewStyle().Foreground(statusStopped).Render("stopped")
	default:
		return lipgloss.NewStyle().Foreground(dimmed).Render(wt.Supervisor)
	}
}

func boolDisplay(v bool) string {
	if v {
		return lipgloss.NewStyle().Foreground(statusRunning).Render("yes")
	}
	return lipgloss.NewStyle().Foreground(statusStopped).Render("no")
}

// --- Bottom status bar ---

func (m *Model) renderStatusBar(width int) string {
	running := 0
	for _, wt := range m.snapshot.Worktrees {
		if wt.Listening {
			running++
		}
	}

	serveStr := "inactive"
	if m.snapshot.ServeRunning {
		serveStr = "active"
	}

	pollIndicator := " "
	if m.polling {
		pollIndicator = " " + m.spinner.View() + " "
	}

	stats := fmt.Sprintf("%s%d projects · %d worktrees · %d running · serve: %s",
		pollIndicator, len(m.snapshot.Projects), len(m.snapshot.Worktrees), running, serveStr)

	if m.snapshot.TunnelDomain != "" {
		stats += " · tunnel: " + m.snapshot.TunnelDomain
	}

	if m.statusMsg != "" {
		stats += " · " + m.statusMsg
	}

	keys := []struct{ key, desc string }{
		{"s", "start/stop"},
		{"o", "open"},
		{"r", "restart"},
		{"e", "env sync"},
		{"n", "new"},
		{"d", "release"},
		{"/", "filter"},
		{"?", "help"},
		{"q", "quit"},
	}

	var kb strings.Builder
	for i, k := range keys {
		if i > 0 {
			kb.WriteString("  ")
		}
		kb.WriteString(helpKeyStyle.Render(k.key))
		kb.WriteString(helpDescStyle.Render(":" + k.desc))
	}

	statsLine := statusBarStyle.Width(width).Render(stats)
	keysLine := statusBarStyle.Width(width).Render(" " + kb.String())
	return lipgloss.JoinVertical(lipgloss.Left, statsLine, keysLine)
}

// --- Help overlay ---

func (m *Model) renderHelpOverlay() string {
	help := `
  gtl dashboard — Keyboard Shortcuts

  j/k, ↑/↓     Navigate worktree list
  Enter         Focus detail panel
  Tab           Cycle focus
  s             Start/stop supervisor
  o             Open in browser
  r             Restart supervisor
  e             Sync env file
  n             New worktree
  d             Release worktree (confirm)
  /             Filter worktrees
  Esc           Clear filter
  ?             Toggle this help
  q, Ctrl+C     Quit
`
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 2).
		Render(help)

	return lipgloss.Place(m.width, m.height-statusBarHeight,
		lipgloss.Center, lipgloss.Center, box)
}

// --- Confirm overlay ---

func (m *Model) renderConfirmOverlay() string {
	wt := m.selectedWorktree()
	if wt == nil {
		return ""
	}
	prompt := fmt.Sprintf("Release worktree %q?\n\n  y = confirm   any other key = cancel", wt.Branch)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(danger).
		Padding(1, 2).
		Render(prompt)

	return lipgloss.Place(m.width, m.height-statusBarHeight,
		lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) renderInputOverlay() string {
	prompt := fmt.Sprintf("New worktree — branch name:\n\n  %s▌\n\n  Enter = create   Esc = cancel", m.inputText)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 2).
		Render(prompt)

	return lipgloss.Place(m.width, m.height-statusBarHeight,
		lipgloss.Center, lipgloss.Center, box)
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
