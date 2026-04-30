package cmd

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(worktreesCmd)
}

var worktreesCmd = &cobra.Command{
	Use:     "worktrees",
	Aliases: []string{"wt"},
	Short:   "Interactive worktree picker",
	Long: `Opens an interactive picker to browse and select worktrees.

Arrow keys to navigate, Enter to print path (for cd), q to quit.

For scripting, use 'gtl where <branch>' instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries := loadWorktreeEntries()
		if len(entries) == 0 {
			fmt.Println("No worktrees found.")
			return nil
		}

		m := newWorktreePickerModel(entries)
		p := tea.NewProgram(m)
		finalModel, err := p.Run()
		if err != nil {
			return err
		}

		if fm, ok := finalModel.(worktreePickerModel); ok && fm.selected != "" {
			fmt.Println(fm.selected)
		}
		return nil
	},
}

type worktreeEntry struct {
	project  string
	branch   string
	path     string
	isHeader bool
}

func loadWorktreeEntries() []worktreeEntry {
	reg := registry.New("")
	allocs := reg.Allocations()

	grouped := make(map[string][]registry.Allocation)
	var projects []string

	for _, a := range allocs {
		project, _ := a["project"].(string)
		if project == "" {
			project = "(unknown)"
		}
		if _, seen := grouped[project]; !seen {
			projects = append(projects, project)
		}
		grouped[project] = append(grouped[project], a)
	}

	sort.Strings(projects)

	var entries []worktreeEntry
	for _, proj := range projects {
		entries = append(entries, worktreeEntry{project: proj, isHeader: true})

		allocs := grouped[proj]
		sort.Slice(allocs, func(i, j int) bool {
			bi, _ := allocs[i]["branch"].(string)
			bj, _ := allocs[j]["branch"].(string)
			return bi < bj
		})

		for _, a := range allocs {
			branch, _ := a["branch"].(string)
			path, _ := a["worktree"].(string)
			entries = append(entries, worktreeEntry{
				project: proj,
				branch:  branch,
				path:    path,
			})
		}
	}
	return entries
}

type worktreePickerModel struct {
	entries  []worktreeEntry
	cursor   int
	selected string
	height   int
	offset   int
}

func newWorktreePickerModel(entries []worktreeEntry) worktreePickerModel {
	m := worktreePickerModel{entries: entries, height: 20}
	m.skipToNextWorktree(1)
	return m
}

func (m worktreePickerModel) Init() tea.Cmd {
	return nil
}

func (m worktreePickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.moveUp()
		case "down", "j":
			m.moveDown()
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.entries) && !m.entries[m.cursor].isHeader {
				m.selected = m.entries[m.cursor].path
			}
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.height = msg.Height - 4
		if m.height < 5 {
			m.height = 5
		}
	}
	return m, nil
}

func (m *worktreePickerModel) moveUp() {
	start := m.cursor
	for {
		m.cursor--
		if m.cursor < 0 {
			m.cursor = 0
			// If we started on a non-header and couldn't find another, stay put
			if start < len(m.entries) && !m.entries[start].isHeader {
				m.cursor = start
			}
			m.adjustScroll()
			return
		}
		if !m.entries[m.cursor].isHeader {
			break
		}
	}
	m.adjustScroll()
}

func (m *worktreePickerModel) moveDown() {
	start := m.cursor
	for {
		m.cursor++
		if m.cursor >= len(m.entries) {
			m.cursor = len(m.entries) - 1
			// If we started on a non-header and couldn't find another, stay put
			if start >= 0 && start < len(m.entries) && !m.entries[start].isHeader {
				m.cursor = start
			}
			m.adjustScroll()
			return
		}
		if !m.entries[m.cursor].isHeader {
			break
		}
	}
	m.adjustScroll()
}

func (m *worktreePickerModel) skipToNextWorktree(dir int) {
	for m.cursor >= 0 && m.cursor < len(m.entries) && m.entries[m.cursor].isHeader {
		m.cursor += dir
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
}

func (m *worktreePickerModel) adjustScroll() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
}

var (
	wtHeaderStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	wtSelectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("27")).Foreground(lipgloss.Color("15"))
	wtNormalStyle   = lipgloss.NewStyle()
	wtDimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m worktreePickerModel) View() tea.View {
	var sb strings.Builder
	sb.WriteString("Worktrees (↑↓ navigate, enter select, q quit)\n\n")

	end := m.offset + m.height
	if end > len(m.entries) {
		end = len(m.entries)
	}

	for i := m.offset; i < end; i++ {
		e := m.entries[i]
		if e.isHeader {
			sb.WriteString(wtHeaderStyle.Render(e.project) + "\n")
			continue
		}

		line := fmt.Sprintf("  %-30s %s", e.branch, e.path)
		if i == m.cursor {
			sb.WriteString(wtSelectedStyle.Render(line) + "\n")
		} else {
			sb.WriteString(wtNormalStyle.Render(line) + "\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(wtDimStyle.Render("cd $(gtl where <branch>) to jump"))

	v := tea.NewView(sb.String())
	v.AltScreen = true
	return v
}
