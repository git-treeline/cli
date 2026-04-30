package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/worktree"
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
	supervisorSend  func(sockPath, command string) (string, error)
	supervisorSock  func(worktreePath string) string
	openURL         func(url string)
	releaseWorktree func(worktreePath string) error
	syncEnv         func(worktreePath string) error
	createWorktree  func(existingWorktreePath, branch string) error
}

// Model is the root Bubble Tea model for the gtl dashboard.
type Model struct {
	snapshot     Snapshot
	width        int
	height       int
	focus        focusPane
	cursor       int // selected index into flatList
	scrollOffset int // first visible index in the list panel
	flatList     []flatEntry
	ready        bool
	polling      bool
	spinner      spinner.Model
	filterMode   bool
	filterText   string
	showHelp     bool
	confirmKind  string // "release" or ""
	quitting     bool
	inputMode    string // "" or "new_worktree"
	inputText    string
	statusMsg    string // transient status message
	deps         actionDeps
}

// flatEntry is a denormalized row in the worktree list.
// projectHeader=true means this row is a group header, not a selectable worktree.
type flatEntry struct {
	projectHeader bool
	project       string
	wt            *WorktreeStatus
}

type tickMsg time.Time
type dataMsg Snapshot

func doTick() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func doPoll() tea.Cmd {
	return func() tea.Msg {
		return dataMsg(Poll())
	}
}

func defaultDeps() actionDeps {
	return actionDeps{
		supervisorSend: supervisor.Send,
		supervisorSock: supervisor.SocketPath,
		openURL:        openURLDefault,
		releaseWorktree: func(wtPath string) error {
			return exec.Command("git-treeline", "release", "--force", wtPath).Run()
		},
		syncEnv: func(wtPath string) error {
			uc := config.LoadUserConfig("")
			return setup.RegenerateEnvFile(wtPath, uc)
		},
		createWorktree: func(existingWorktreePath, branch string) error {
			mainRepo := worktree.DetectMainRepo(existingWorktreePath)
			if mainRepo == "" {
				return fmt.Errorf("cannot detect main repo from %s", existingWorktreePath)
			}
			uc := config.LoadUserConfig("")
			pc := config.LoadProjectConfig(mainRepo)
			projectName := pc.Project()

			wtPath := uc.ResolveWorktreePath(mainRepo, projectName, branch)
			if wtPath == "" {
				wtPath = mainRepo + "-" + branch
			}

			if worktree.BranchExists(branch) {
				_ = worktree.Fetch("origin", branch)
				if err := worktree.Create(wtPath, branch, false, ""); err != nil {
					return err
				}
			} else {
				base := worktree.DetectDefaultBranch(mainRepo)
				if err := worktree.Create(wtPath, branch, true, base); err != nil {
					return err
				}
			}

			s := setup.New(wtPath, mainRepo, uc)
			_, err := s.Run()
			return err
		},
	}
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
	return Model{spinner: s, polling: true, deps: defaultDeps()}
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
