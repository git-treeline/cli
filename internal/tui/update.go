package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
)

type envSyncResultMsg struct{ err error }
type newWorktreeResultMsg struct{ err error }
type releaseResultMsg struct{ err error }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case dataMsg:
		m.snapshot = Snapshot(msg)
		m.flatList = buildFlatList(m.snapshot, m.filterText)
		m.clampCursor()
		m.ready = true
		m.polling = false
		return m, nil

	case tickMsg:
		m.statusMsg = ""
		m.polling = true
		return m, tea.Batch(doPoll(), doTick())

	case supervisorResultMsg:
		m.polling = true
		return m, doPoll()

	case releaseResultMsg:
		if msg.err != nil {
			m.statusMsg = "release failed: " + msg.err.Error()
		} else {
			m.statusMsg = "worktree released"
		}
		m.polling = true
		return m, doPoll()

	case envSyncResultMsg:
		if msg.err != nil {
			m.statusMsg = "env sync failed: " + msg.err.Error()
		} else {
			m.statusMsg = "env synced"
		}
		m.polling = true
		return m, doPoll()

	case newWorktreeResultMsg:
		if msg.err != nil {
			m.statusMsg = "create failed: " + msg.err.Error()
		} else {
			m.statusMsg = "worktree created"
		}
		m.polling = true
		return m, doPoll()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
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
		m.quitting = true
		return m, tea.Quit

	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)

	case "tab":
		if m.focus == paneList {
			m.focus = paneDetail
		} else {
			m.focus = paneList
		}

	case "enter":
		m.focus = paneDetail

	case "s":
		return m, m.toggleSupervisor()
	case "r":
		return m, m.restartSupervisor()
	case "o":
		m.openInBrowser()
	case "d":
		if m.selectedWorktree() != nil {
			m.confirmKind = "release"
		}

	case "e":
		return m, m.syncEnv()

	case "n":
		m.inputMode = "new_worktree"
		m.inputText = ""
		return m, nil

	case "/":
		m.filterMode = true
		m.filterText = ""

	case "?":
		m.showHelp = true

	case "escape", "esc":
		if m.filterText != "" {
			m.filterText = ""
			m.flatList = buildFlatList(m.snapshot, "")
			m.clampCursor()
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
			m.filterText = m.filterText[:len(m.filterText)-1]
		}
	default:
		if msg.Key().Text != "" {
			m.filterText += msg.Key().Text
		}
	}
	m.flatList = buildFlatList(m.snapshot, m.filterText)
	m.clampCursor()
	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if m.confirmKind == "release" {
			cmd := m.releaseWorktree()
			m.confirmKind = ""
			return m, cmd
		}
	}
	m.confirmKind = ""
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
		if lines < maxLines-scrollMargin {
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
	contentHeight := m.height - statusBarHeight - borderChrome
	if contentHeight < minContentHeight {
		contentHeight = minContentHeight
	}
	return contentHeight - 1
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
	leftWidth := int(float64(m.width) * listPanelRatio)
	mouse := msg.Mouse()
	if mouse.X < leftWidth {
		m.focus = paneList
		// offset by 2 for border + title, then add scrollOffset
		idx := mouse.Y - 2 + m.scrollOffset
		if idx >= 0 && idx < len(m.flatList) && !m.flatList[idx].projectHeader {
			m.cursor = idx
			m.ensureCursorVisible()
		}
	} else {
		m.focus = paneDetail
	}
	return m, nil
}

// --- Supervisor actions ---

type supervisorResultMsg struct{}

func (m *Model) toggleSupervisor() tea.Cmd {
	wt := m.selectedWorktree()
	if wt == nil {
		return nil
	}
	sockPath := m.deps.supervisorSock(wt.WorktreePath)
	command := "start"
	if wt.Supervisor == "running" {
		command = "stop"
	}
	send := m.deps.supervisorSend
	return func() tea.Msg {
		_, _ = send(sockPath, command)
		return supervisorResultMsg{}
	}
}

func (m *Model) restartSupervisor() tea.Cmd {
	wt := m.selectedWorktree()
	if wt == nil {
		return nil
	}
	sockPath := m.deps.supervisorSock(wt.WorktreePath)
	send := m.deps.supervisorSend
	return func() tea.Msg {
		_, _ = send(sockPath, "restart")
		return supervisorResultMsg{}
	}
}

func (m Model) updateInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		branch := strings.TrimSpace(m.inputText)
		m.inputMode = ""
		m.inputText = ""
		if branch == "" {
			return m, nil
		}
		return m, m.createWorktree(branch)
	case "escape", "esc":
		m.inputMode = ""
		m.inputText = ""
		return m, nil
	case "backspace":
		if len(m.inputText) > 0 {
			m.inputText = m.inputText[:len(m.inputText)-1]
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
	wt := m.selectedWorktree()
	if wt == nil || wt.WorktreePath == "" {
		return nil
	}
	wtPath := wt.WorktreePath
	fn := m.deps.syncEnv
	return func() tea.Msg {
		return envSyncResultMsg{err: fn(wtPath)}
	}
}

func (m *Model) createWorktree(branch string) tea.Cmd {
	wt := m.selectedWorktree()
	if wt == nil || wt.WorktreePath == "" {
		return nil
	}
	existingPath := wt.WorktreePath
	fn := m.deps.createWorktree
	return func() tea.Msg {
		return newWorktreeResultMsg{err: fn(existingPath, branch)}
	}
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

func (m *Model) releaseWorktree() tea.Cmd {
	wt := m.selectedWorktree()
	if wt == nil {
		return nil
	}
	wtPath := wt.WorktreePath
	fn := m.deps.releaseWorktree
	return func() tea.Msg {
		return releaseResultMsg{err: fn(wtPath)}
	}
}
