package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	listPanelRatio   = 0.40
	borderChrome     = 2 // left + right border
	statusBarHeight  = 2
	minContentHeight = 4
	compactWidth     = 80
)

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	var content string
	if !m.ready {
		content = panelTitle.Render("loading dashboard...")
	} else {
		height := max(0, m.height-statusBarHeight-borderChrome)
		if m.width < compactWidth {
			width := max(0, m.width-borderChrome)
			panel := m.renderListPanel(width, height)
			if m.focus == paneDetail {
				panel = m.renderDetailPanel(width, height)
			}
			content = panelBorderActive.Width(width + borderChrome).Height(height + borderChrome).Render(panel)
		} else {
			leftWidth := int(float64(m.width)*listPanelRatio) - borderChrome
			rightWidth := m.detailPanelWidth()
			leftBorder, rightBorder := panelBorder, panelBorder
			if m.focus == paneList {
				leftBorder = panelBorderActive
			} else {
				rightBorder = panelBorderActive
			}
			left := leftBorder.Width(leftWidth + borderChrome).Height(height + borderChrome).Render(m.renderListPanel(leftWidth, height))
			right := rightBorder.Width(rightWidth + borderChrome).Height(height + borderChrome).Render(m.renderDetailPanel(rightWidth, height))
			content = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
		}
		content = lipgloss.JoinVertical(lipgloss.Left, content, m.renderStatusBar(m.width))
	}
	if m.showHelp {
		content = m.renderHelpOverlay()
	}
	if m.confirmKind != "" {
		content = m.renderConfirmOverlay()
	}
	if m.inputMode != "" {
		content = m.renderInputOverlay()
	}
	v := tea.NewView(clipBlock(content, m.width, m.height))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// --- Left panel: worktree list ---

func (m *Model) renderListPanel(width, height int) string {
	title := "WORKTREES"
	if m.filterMode {
		title += fmt.Sprintf(" (/%s▌)", m.filterText)
	} else if m.filterText != "" {
		title += fmt.Sprintf(" [%s]", m.filterText)
	}
	lines := []string{panelTitle.Render(truncate(title, width-1))}
	if len(m.flatList) == 0 {
		lines = append(lines, "", " No worktrees found.")
	}
	for i := m.scrollOffset; i < len(m.flatList) && len(lines) < height; i++ {
		entry := m.flatList[i]
		if entry.projectHeader {
			lines = append(lines, "", projectHeader.Render(truncate(entry.project, width)))
			continue
		}
		wt := entry.wt
		prefix := fmt.Sprintf("  %s %s ", statusDot(wt), portStyle.Render(fmt.Sprintf(":%d", primaryPort(wt))))
		badge := ""
		if len(wt.Links) > 0 {
			badge = " " + linkIndicatorStyle.Render("⇄")
		}
		line := prefix + truncate(wt.Branch, width-lipgloss.Width(prefix)-lipgloss.Width(badge)) + badge
		if i == m.cursor {
			line = selectedRow.Render(line)
		}
		lines = append(lines, line)
	}
	return clipBlock(strings.Join(lines, "\n"), width, height)
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

func (m Model) detailPanelWidth() int {
	if m.width < compactWidth {
		return max(0, m.width-borderChrome)
	}
	return max(0, m.width-int(float64(m.width)*listPanelRatio)-borderChrome)
}

func (m Model) detailViewportHeight() int {
	return max(0, m.height-statusBarHeight-borderChrome-4)
}

func (m Model) detailLineCount(width int) int {
	return len(m.detailLines(width))
}

func (m *Model) renderDetailPanel(width, height int) string {
	title := "DETAIL"
	if wt := m.selectedWorktree(); wt != nil {
		title = wt.Project + " / " + wt.Branch
	}
	if m.detailTab == tabActivity {
		title = "LAST OPERATION"
	}
	tabs := []string{"1 Overview", "2 Health", "3 Activity"}
	if width < 34 {
		tabs = []string{"1 Info", "2 Health", "3 Act"}
	}
	for i := range tabs {
		if detailTab(i) == m.detailTab {
			tabs[i] = selectedRow.Render(tabs[i])
		} else {
			tabs[i] = helpDescStyle.Render(tabs[i])
		}
	}
	lines := []string{panelTitle.Render(truncate(title, width-1)), strings.Join(tabs, "  "), ""}
	body := m.detailLines(width)
	visible := max(0, height-4)
	start := min(max(0, m.detailScroll), max(0, len(body)-visible))
	end := min(len(body), start+visible)
	lines = append(lines, body[start:end]...)
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	footer := "Tab: list · h: check health"
	if len(body) > visible && visible > 0 {
		footer = fmt.Sprintf("%d–%d/%d · j/k: scroll · Tab: list", start+1, end, len(body))
	}
	lines = append(lines, helpDescStyle.Render(footer))
	return clipBlock(strings.Join(lines, "\n"), width, height)
}

func (m Model) detailLines(width int) []string {
	if width <= 0 {
		return nil
	}
	var body string
	switch m.detailTab {
	case tabHealth:
		wt := m.selectedWorktree()
		if wt == nil {
			body = "Select a worktree to check its health."
		} else if report, ok := m.health[wt.WorktreePath]; ok {
			body = "Health · " + wt.Branch + "\n"
			if report.stale {
				body += "Changed since this check. Press h to recheck.\n"
			}
			if report.err != nil {
				body += statusErrorStyle.Render("Needs attention: "+plainOutput(report.err.Error())) + "\n"
			}
			body += "\n" + plainOutput(report.output)
			if strings.TrimSpace(report.output) == "" && report.err == nil {
				body += "Health check completed without output."
			}
			body += "\n\nPress h to run doctor again."
		} else {
			body = "No health check yet.\n\nPress h to run doctor for this worktree.\nThe report includes checks and recovery steps."
		}
		if m.operation != nil && m.operation.running && wt != nil && m.operation.path == wt.WorktreePath {
			body = "Running: " + m.operation.name + "\n\n" + body
		}
	case tabActivity:
		if op := m.operation; op != nil {
			state := "Completed"
			if op.running {
				state = "Running"
			} else if op.err != nil {
				state = "Failed"
			}
			body = state + " · " + op.name + "\nTarget: " + op.branch + "\n" + op.path + "\n"
			if op.err != nil {
				body += "\n" + statusErrorStyle.Render(plainOutput(op.err.Error())) + "\n"
			}
			if strings.TrimSpace(op.output) != "" {
				body += "\n" + plainOutput(op.output)
			} else if op.running {
				body += "\nWaiting for command output…"
			} else if op.err == nil {
				body += "\nCommand completed successfully."
			}
		} else {
			body = "No operations yet.\n\nStart, restart, sync env, or check health.\nThe latest operation and its output stay here until the next operation."
		}
	default:
		body = m.overviewContent()
	}
	return strings.Split(ansi.Hardwrap(body, width, true), "\n")
}

func (m Model) overviewContent() string {
	wt := m.selectedWorktree()
	if wt == nil {
		return "Select a worktree."
	}
	rows := []struct{ label, value string }{{"Port", portsString(wt.Ports)}}
	if wt.RouterURL != "" {
		rows = append(rows, struct{ label, value string }{"URL", wt.RouterURL})
	}
	if wt.TunnelURL != "" {
		rows = append(rows, struct{ label, value string }{"Tunnel", wt.TunnelURL})
	}
	rows = append(rows,
		struct{ label, value string }{"Database", valueOrDash(wt.Database)},
		struct{ label, value string }{"Redis", redisDisplay(wt)},
		struct{ label, value string }{"Supervisor", supervisorDisplay(wt)},
		struct{ label, value string }{"Listening", boolDisplay(wt.Listening)},
		struct{ label, value string }{"Env file", valueOrDash(wt.EnvFile)},
		struct{ label, value string }{"Worktree", wt.WorktreePath},
	)
	var lines []string
	for _, r := range rows {
		lines = append(lines, detailLabel.Render(r.label)+detailValue.Render(r.value))
	}
	if len(wt.Links) > 0 {
		lines = append(lines, "", detailLabel.Render("Overrides"))
		keys := make([]string, 0, len(wt.Links))
		for k := range wt.Links {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, linkIndicatorStyle.Render("⇄ "+k)+" → "+wt.Links[k])
		}
	}
	lines = append(lines, "", helpDescStyle.Render("h: diagnose · 2: health · 3: activity"))
	return strings.Join(lines, "\n")
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
	serve := "inactive"
	if m.snapshot.ServeRunning {
		serve = "active"
	}
	stats := fmt.Sprintf("%d projects · %d worktrees · %d running · serve: %s", len(m.snapshot.Projects), len(m.snapshot.Worktrees), running, serve)
	if m.pendingQuit {
		stats = m.spinner.View() + " " + plainOutput(m.statusMsg)
	} else if m.operation != nil && m.operation.running {
		stats = m.spinner.View() + " Running " + m.operation.name + " · " + m.operation.branch + " · 3: activity"
		if m.pollErr != nil {
			stats += " · poll failed (stale)"
		}
	} else if m.pollErr != nil {
		stats = statusErrorStyle.Render("⚠ poll failed (stale): "+plainOutput(m.pollErr.Error())) + " · " + stats
	} else if m.statusMsg != "" {
		stats = plainOutput(m.statusMsg) + " · " + stats
	}
	keys := "Tab: focus  1/2/3: details  h: health  s: start/stop  r: restart  ?: help  q: quit"
	if width < compactWidth {
		keys = "Tab: list/detail  1/2/3: tabs  h: health  ?: help"
	}
	inner := max(0, width-2)
	style := statusBarStyle.Width(width)
	return clipBlock(style.Render(truncate(strings.ReplaceAll(stats, "\n", " "), inner))+"\n"+style.Render(truncate(keys, inner)), width, statusBarHeight)
}

// --- Overlays ---

func (m *Model) renderHelpOverlay() string {
	return m.renderOverlay("gtl dashboard — Shortcuts\n\nj/k, ↑/↓  Navigate / scroll focused panel\nEnter      Focus details\nTab        Switch list / details\n1 / 2 / 3  Overview / Health / Activity\nh          Run / recheck health\ns          Start / stop server\nr          Restart server\ne          Sync env file\no          Open in browser\nn          New worktree\nd          Release allocations (confirm)\n/          Filter worktrees\nEsc / ←    Return to list; Esc clears filter\n?          Toggle help\nq, Ctrl+C  Quit\n\nPress any key to close.", false)
}

func (m *Model) renderConfirmOverlay() string {
	if m.confirmTarget == nil {
		return ""
	}
	prompt := fmt.Sprintf("Release allocations for %q?\n\ny: release   any other key: cancel\n\nStops its server and frees its allocated resources.\nKeeps the worktree directory and databases.\nConfigured release hooks also run.", m.confirmTarget.Branch)
	return m.renderOverlay(prompt, true)
}

func (m *Model) renderInputOverlay() string {
	prompt := fmt.Sprintf("New worktree — branch name\n\n%s▌\n\nEnter: create   Esc: cancel", m.inputText)
	if m.inputTarget != nil {
		prompt += "\n\nProject: " + m.inputTarget.Project + "\nFrom: " + m.inputTarget.WorktreePath
	}
	return m.renderOverlay(prompt, false)
}

func (m Model) renderOverlay(content string, destructive bool) string {
	width := max(1, min(62, m.width-6))
	height := max(1, m.height-4)
	color := highlight
	if destructive {
		color = danger
	}
	body := clipBlock(ansi.Hardwrap(content, width, false), width, height)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Padding(0, 1).Render(body)
	box = clipBlock(box, m.width, m.height)
	return lipgloss.Place(max(0, m.width), max(0, m.height), lipgloss.Center, lipgloss.Center, box)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		return truncateToWidth(s, width)
	}
	return ansi.Truncate(s, width, "...")
}

func truncateToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

func clipBlock(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	lines = lines[:min(len(lines), height)]
	for i := range lines {
		lines[i] = truncateToWidth(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

func plainOutput(s string) string {
	s = strings.ReplaceAll(ansi.Strip(s), "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r != '\n' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
