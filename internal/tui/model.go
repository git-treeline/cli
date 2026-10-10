package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const pollInterval = 2 * time.Second

// focusPane tracks which panel has keyboard focus.
type focusPane int

const (
	paneList focusPane = iota
	paneDetail
)

// actionDeps holds injectable functions for side-effectful operations.
// Production code sets these to real implementations; tests use stubs.
type actionDeps struct {
	runCommand func(context.Context, string, ...string) (string, error)
	openURL    func(string)
}

type detailTab int

const (
	tabOverview detailTab = iota
	tabHealth
	tabActivity
)

type operationState struct {
	name, path, branch, output string
	err                        error
	running                    bool
}

type healthReport struct {
	branch string
	output string
	err    error
	stale  bool
}

// Model is the root Bubble Tea model for the gtl dashboard.
type Model struct {
	snapshot        Snapshot
	width           int
	height          int
	focus           focusPane
	cursor          int // selected index into flatList
	scrollOffset    int // first visible index in the list panel
	flatList        []flatEntry
	ready           bool
	polling         bool
	spinner         spinner.Model
	filterMode      bool
	filterText      string
	showHelp        bool
	confirmKind     string          // "release" or ""
	confirmTarget   *WorktreeStatus // worktree captured when the confirm overlay opened
	pendingQuit     bool
	quitting        bool
	inputMode       string // "" or "new_worktree"
	inputText       string
	statusMsg       string // last operation result
	pollErr         error  // last poll failure; snapshot below is then stale
	inputTarget     *WorktreeStatus
	detailTab       detailTab
	detailScroll    int
	operation       *operationState
	health          map[string]healthReport
	lifecycle       *commandLifecycle
	operationCancel context.CancelFunc
	deps            actionDeps
}

// flatEntry is a denormalized row in the worktree list.
// projectHeader=true means this row is a group header, not a selectable worktree.
type flatEntry struct {
	projectHeader bool
	project       string
	wt            *WorktreeStatus
}

type tickMsg time.Time
type dataMsg struct {
	snapshot Snapshot
	err      error
}

func doTick() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func doPoll() tea.Cmd {
	return func() tea.Msg {
		snap, err := Poll()
		return dataMsg{snapshot: snap, err: err}
	}
}

func defaultDeps() actionDeps {
	return actionDeps{runCommand: runCLI, openURL: openURLDefault}
}

// gtlBinaryPath resolves the path used to re-invoke the CLI for actions like
// release. It prefers the currently running executable (so release works
// regardless of whether the binary is installed as gtl or git-treeline), then
// falls back to a PATH lookup, and finally to a bare command name.
func gtlBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			return resolved
		}
		return exe
	}
	for _, name := range []string{"gtl", "git-treeline"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return "git-treeline"
}

func openURLDefault(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}

// NewModel creates the initial dashboard model.
func NewModel() Model {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(highlight)),
	)
	return Model{spinner: s, polling: true, deps: defaultDeps(), health: make(map[string]healthReport), lifecycle: newCommandLifecycle()}
}

// Close cancels pending dashboard commands and waits for running commands to finish.
// The initial model owns the lifecycle shared by all Bubble Tea model copies.
func (m Model) Close() {
	if m.lifecycle != nil {
		m.lifecycle.close()
	}
}

type commandLifecycle struct {
	mutex   sync.Mutex
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	running sync.WaitGroup
}

func newCommandLifecycle() *commandLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return &commandLifecycle{ctx: ctx, cancel: cancel}
}

func (l *commandLifecycle) start() bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.closed {
		return false
	}
	l.running.Add(1)
	return true
}

func (l *commandLifecycle) close() {
	l.mutex.Lock()
	l.closed = true
	l.cancel()
	l.mutex.Unlock()
	l.running.Wait()
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(doPoll(), doTick(), m.spinner.Tick)
}

// buildFlatList converts the snapshot into a flat list of project headers + worktree rows.
func buildFlatList(snap Snapshot, filter string) []flatEntry {
	grouped := make(map[string][]WorktreeStatus, len(snap.Projects))
	for i := range snap.Worktrees {
		wt := &snap.Worktrees[i]
		if filter != "" && !matchesFilter(wt, filter) {
			continue
		}
		grouped[wt.Project] = append(grouped[wt.Project], *wt)
	}

	var entries []flatEntry
	for _, proj := range snap.Projects {
		wts := grouped[proj]
		if len(wts) == 0 {
			continue
		}
		entries = append(entries, flatEntry{projectHeader: true, project: proj})
		for i := range wts {
			entries = append(entries, flatEntry{project: proj, wt: &wts[i]})
		}
	}
	return entries
}

func matchesFilter(wt *WorktreeStatus, filter string) bool {
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(wt.Project), f) ||
		strings.Contains(strings.ToLower(wt.Branch), f) ||
		strings.Contains(strings.ToLower(wt.WorktreeName), f)
}

// selectedWorktree returns the WorktreeStatus at the cursor, or nil if on a header.
func (m *Model) selectedWorktree() *WorktreeStatus {
	if m.cursor < 0 || m.cursor >= len(m.flatList) {
		return nil
	}
	return m.flatList[m.cursor].wt
}
