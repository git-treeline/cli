package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

type operationResultMsg struct {
	operation operationState
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ensureCursorVisible()
		m.clampDetailScroll()
		return m, nil

	case dataMsg:
		m.ready = true
		m.polling = false
		if msg.err != nil {
			// Retain the last-known-good snapshot; surface the failure in the
			// view rather than blanking the dashboard to "0 worktrees".
			m.pollErr = msg.err
			return m, nil
		}
		m.pollErr = nil
		selected := m.selectedPath()
		m.snapshot = msg.snapshot
		for path, report := range m.health {
			report.stale = report.stale || !m.hasWorktree(path, report.branch)
			m.health[path] = report
		}
		m.rebuildList(selected)
		m.clampDetailScroll()
		return m, nil

	case tickMsg:
		if m.polling {
			return m, doTick()
		}
		m.polling = true
		return m, tea.Batch(doPoll(), doTick())

	case operationResultMsg:
		if m.operationCancel != nil {
			m.operationCancel()
			m.operationCancel = nil
		}
		op := msg.operation
		op.running = false
		m.operation = &op
		if op.name == "Health" {
			if m.health == nil {
				m.health = make(map[string]healthReport)
			}
			m.health[op.path] = healthReport{branch: op.branch, output: op.output, err: op.err, stale: !m.hasWorktree(op.path, op.branch)}
		}
		m.statusMsg = op.name + " completed: " + op.branch
		if op.err != nil {
			m.statusMsg = op.name + " failed: " + op.branch + " — " + op.err.Error()
		}
		m.clampDetailScroll()
		if m.pendingQuit {
			m.quitting = true
			return m, tea.Quit
		}
		if m.polling {
			return m, nil
		}
		m.polling = true
		return m, doPoll()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		if m.pendingQuit {
			return m, nil
		}
		if m.inputMode != "" {
			return m.updateInput(msg)
		}
		if m.filterMode {
			return m.updateFilter(msg)
		}
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		if m.confirmKind != "" {
			return m.updateConfirm(msg)
		}
		return m.updateNormal(msg)

	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	}

	return m, nil
}

func (m Model) updateNormal(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m.quit()

	case "j", "down":
		if m.focus == paneDetail {
			m.scrollDetail(1)
		} else {
			m.moveCursor(1)
		}
	case "k", "up":
		if m.focus == paneDetail {
			m.scrollDetail(-1)
		} else {
			m.moveCursor(-1)
		}

	case "pgdown", "pgup", "home", "end":
		if m.focus == paneDetail {
			switch msg.String() {
			case "pgdown":
				m.scrollDetail(m.detailViewportHeight())
			case "pgup":
				m.scrollDetail(-m.detailViewportHeight())
			case "home":
				m.detailScroll = 0
			case "end":
				m.detailScroll = m.detailLineCount(m.detailPanelWidth())
				m.clampDetailScroll()
			}
		}
	case "1", "2", "3", "h":
		m.focus = paneDetail
		m.detailScroll = 0
		switch msg.String() {
		case "1":
			m.detailTab = tabOverview
		case "3":
			m.detailTab = tabActivity
		default:
			m.detailTab = tabHealth
			cmd := m.checkHealth()
			return m, cmd
		}
	case "left":
		m.focus = paneList
	case "tab":
		if m.focus == paneList {
			m.focus = paneDetail
		} else {
			m.focus = paneList
		}

	case "enter":
		m.focus = paneDetail

	case "s":
		cmd := m.toggleSupervisor()
		return m, cmd
	case "r":
		cmd := m.restartSupervisor()
		return m, cmd
	case "o":
		m.openInBrowser()
	case "d":
		if m.busy() {
			return m, nil
		}
		if wt := m.selectedWorktree(); wt != nil {
			target := *wt
			m.confirmTarget = &target
			m.confirmKind = "release"
		}

	case "e":
		cmd := m.syncEnv()
		return m, cmd

	case "n":
		if m.busy() {
			return m, nil
		}
		wt := m.selectedWorktree()
		if wt == nil {
			return m, nil
		}
		target := *wt
		m.inputTarget = &target
		m.inputMode = "new_worktree"
		m.inputText = ""
		return m, nil

	case "/":
		m.focus = paneList
		m.filterMode = true
		m.filterText = ""

	case "?":
		m.showHelp = true

	case "escape", "esc":
		m.focus = paneList
		if m.filterText != "" {
			m.filterText = ""
			m.rebuildList(m.selectedPath())
		}
	}
	return m, nil
}

func (m Model) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "escape", "esc":
		m.filterMode = false
		return m, nil
	case "backspace":
		if len(m.filterText) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.filterText)
			m.filterText = m.filterText[:len(m.filterText)-size]
		}
	default:
		if msg.Key().Text != "" {
			m.filterText += msg.Key().Text
		}
	}
	m.rebuildList(m.selectedPath())
	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if m.confirmKind == "release" {
			if !m.releaseConfirmationFits() {
				return m, nil
			}
			cmd := m.releaseWorktree(m.confirmTarget)
			m.confirmKind = ""
			m.confirmTarget = nil
			return m, cmd
		}
	}
	m.confirmKind = ""
	m.confirmTarget = nil
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if len(m.flatList) == 0 {
		return
	}
	next := m.cursor + delta
	for next >= 0 && next < len(m.flatList) && m.flatList[next].projectHeader {
		next += delta
	}
	if next >= 0 && next < len(m.flatList) {
		if m.cursor != next {
			m.detailScroll = 0
		}
		m.cursor = next
	}
	m.ensureCursorVisible()
}

const scrollMargin = 2

// ensureCursorVisible adjusts scrollOffset so the cursor stays within
// the visible portion of the list panel, accounting for project headers
// consuming 2 rendered lines each.
func (m *Model) ensureCursorVisible() {
	maxLines := m.listVisibleLines()
	if maxLines <= 0 {
		return
	}

	// Scroll up: if cursor is above or within the margin of the top
	for m.cursor < m.scrollOffset+scrollMargin && m.scrollOffset > 0 {
		m.scrollOffset--
	}
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}

	// Scroll down: count rendered lines from scrollOffset to cursor.
	// If the cursor would render below the visible area, bump scrollOffset up.
	for {
		lines := m.renderedLinesBetween(m.scrollOffset, m.cursor)
		if lines <= max(1, maxLines-scrollMargin) {
			break
		}
		if m.scrollOffset >= m.cursor {
			break
		}
		m.scrollOffset++
		if m.scrollOffset >= len(m.flatList) {
			break
		}
	}

	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

// renderedLinesBetween counts how many terminal lines entries [from..to]
// would consume: project headers = 2 lines, worktree rows = 1 line.
func (m *Model) renderedLinesBetween(from, to int) int {
	lines := 0
	for i := from; i <= to && i < len(m.flatList); i++ {
		if m.flatList[i].projectHeader {
			lines += 2
		} else {
			lines++
		}
	}
	return lines
}

func (m *Model) listVisibleLines() int {
	return max(0, m.height-statusBarHeight-borderChrome-1)
}

// listIndexAtRow maps a row within the list panel's content area (row 0 is
// the first line below the title) to the flatList index rendered there,
// mirroring renderListPanel: project headers occupy two rows (a blank line
// and the name), worktrees one. Header rows and rows past the rendered list
// return -1.
func (m *Model) listIndexAtRow(row int) int {
	if row < 0 || row >= m.listVisibleLines() {
		return -1
	}
	line := 0
	for i := m.scrollOffset; i < len(m.flatList) && line <= row; i++ {
		if m.flatList[i].projectHeader {
			line += 2
			continue
		}
		if line == row {
			return i
		}
		line++
	}
	return -1
}

func (m *Model) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.flatList) {
		m.cursor = len(m.flatList) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// If sitting on a header, nudge to next non-header
	if m.cursor < len(m.flatList) && m.flatList[m.cursor].projectHeader {
		for i := m.cursor; i < len(m.flatList); i++ {
			if !m.flatList[i].projectHeader {
				m.cursor = i
				m.ensureCursorVisible()
				return
			}
		}
		for i := m.cursor; i >= 0; i-- {
			if !m.flatList[i].projectHeader {
				m.cursor = i
				m.ensureCursorVisible()
				return
			}
		}
	}
	m.ensureCursorVisible()
}

func (m Model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.pendingQuit || m.inputMode != "" || m.filterMode || m.showHelp || m.confirmKind != "" {
		return m, nil
	}
	leftWidth := int(float64(m.width) * listPanelRatio)
	mouse := msg.Mouse()
	if m.width < compactWidth {
		if m.focus == paneDetail {
			return m, nil
		}
		leftWidth = m.width
	}
	if mouse.X < leftWidth {
		m.focus = paneList
		// Row 0 is the top border, row 1 the panel title.
		if idx := m.listIndexAtRow(mouse.Y - 2); idx >= 0 {
			if m.cursor != idx {
				m.detailScroll = 0
			}
			m.cursor = idx
			m.ensureCursorVisible()
		}
	} else {
		m.focus = paneDetail
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.busy() {
		m.pendingQuit = true
		m.inputMode, m.confirmKind = "", ""
		m.filterMode, m.showHelp = false, false
		m.inputTarget, m.confirmTarget = nil, nil
		m.statusMsg = "Cancelling " + m.operation.name + "…"
		if m.operationCancel != nil {
			m.operationCancel()
		}
		return m, nil
	}
	m.quitting = true
	return m, tea.Quit
}

func (m Model) busy() bool { return m.operation != nil && m.operation.running }

func (m *Model) beginOperation(name string, wt *WorktreeStatus, args ...string) tea.Cmd {
	if m.busy() || wt == nil || wt.WorktreePath == "" {
		return nil
	}
	if m.lifecycle == nil {
		m.lifecycle = newCommandLifecycle()
	}
	lifecycle := m.lifecycle
	ctx, cancel := context.WithCancel(lifecycle.ctx)
	m.operationCancel = cancel
	op := operationState{name: name, path: wt.WorktreePath, branch: wt.Branch, running: true}
	m.operation = &op
	m.statusMsg = name + " in progress: " + wt.Branch
	if name != "Health" {
		m.detailTab = tabActivity
		m.focus = paneDetail
		m.detailScroll = 0
		if report, ok := m.health[wt.WorktreePath]; ok {
			report.stale = true
			m.health[wt.WorktreePath] = report
		}
	}
	run := m.deps.runCommand
	return func() tea.Msg {
		result := op
		defer cancel()
		if !lifecycle.start() {
			result.err = context.Canceled
			result.running = false
			return operationResultMsg{operation: result}
		}
		defer lifecycle.running.Done()
		result.output, result.err = run(ctx, result.path, args...)
		result.running = false
		return operationResultMsg{operation: result}
	}
}

func (m *Model) toggleSupervisor() tea.Cmd {
	wt := m.selectedWorktree()
	if wt != nil && wt.Supervisor == "running" {
		return m.beginOperation("Stop", wt, "stop")
	}
	return m.beginOperation("Start", wt, "start", "--await")
}

func (m *Model) restartSupervisor() tea.Cmd {
	return m.beginOperation("Restart", m.selectedWorktree(), "restart")
}

func (m *Model) checkHealth() tea.Cmd {
	return m.beginOperation("Health", m.selectedWorktree(), "doctor")
}

func (m *Model) selectedPath() string {
	if wt := m.selectedWorktree(); wt != nil {
		return wt.WorktreePath
	}
	return ""
}

func (m Model) hasWorktree(path, branch string) bool {
	for _, wt := range m.snapshot.Worktrees {
		if wt.WorktreePath == path && wt.Branch == branch {
			return true
		}
	}
	return false
}

func (m *Model) rebuildList(selected string) {
	m.flatList = buildFlatList(m.snapshot, m.filterText)
	if selected != "" {
		for i, entry := range m.flatList {
			if entry.wt != nil && entry.wt.WorktreePath == selected {
				m.cursor = i
				break
			}
		}
	}
	m.clampCursor()
	if m.selectedPath() != selected {
		m.detailScroll = 0
	}
}

func (m *Model) scrollDetail(delta int) {
	m.detailScroll += delta
	m.clampDetailScroll()
}

func (m *Model) clampDetailScroll() {
	limit := max(0, m.detailLineCount(m.detailPanelWidth())-m.detailViewportHeight())
	m.detailScroll = max(0, min(m.detailScroll, limit))
}

func (m Model) updateInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		branch := strings.TrimSpace(m.inputText)
		m.inputMode = ""
		m.inputText = ""
		target := m.inputTarget
		m.inputTarget = nil
		if branch == "" {
			return m, nil
		}
		cmd := m.beginOperation("Create", target, "new", "--force", "--", branch)
		return m, cmd
	case "escape", "esc":
		m.inputTarget = nil
		m.inputMode = ""
		m.inputText = ""
		return m, nil
	case "backspace":
		if len(m.inputText) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.inputText)
			m.inputText = m.inputText[:len(m.inputText)-size]
		}
	default:
		ch := msg.Key().Text
		if ch != "" && !strings.ContainsAny(ch, " \t\n\r") {
			m.inputText += ch
		}
	}
	return m, nil
}

func (m *Model) syncEnv() tea.Cmd {
	return m.beginOperation("Env sync", m.selectedWorktree(), "env", "sync")
}

func (m *Model) createWorktree(branch string) tea.Cmd {
	return m.beginOperation("Create", m.selectedWorktree(), "new", "--force", "--", branch)
}

func (m *Model) openInBrowser() {
	wt := m.selectedWorktree()
	if wt == nil || len(wt.Ports) == 0 {
		return
	}
	url := wt.RouterURL
	if url == "" {
		url = fmt.Sprintf("http://localhost:%d", wt.Ports[0])
	}
	m.deps.openURL(url)
}

func (m *Model) releaseWorktree(wt *WorktreeStatus) tea.Cmd {
	if wt == nil {
		return nil
	}
	return m.beginOperation("Release", wt, "release", "--force", wt.WorktreePath)
}
