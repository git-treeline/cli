package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func assertViewFits(t *testing.T, content string, width, height int) {
	t.Helper()
	if !utf8.ValidString(content) {
		t.Fatal("view is not valid UTF-8")
	}
	if width == 0 || height == 0 {
		if content != "" {
			t.Fatalf("zero-sized terminal rendered %q", content)
		}
		return
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		t.Fatalf("view has %d rows in a %d-row terminal", len(lines), height)
	}
	for i, line := range lines {
		if cells := lipgloss.Width(line); cells > width {
			t.Errorf("row %d occupies %d cells in a %d-column terminal: %q", i, cells, width, line)
		}
	}
}

func TestViewFitsTerminalWithLongUnicodeContent(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {120, 24}, {80, 24}, {79, 24}, {40, 12}, {20, 6}, {2, 2}, {1, 1}, {0, 0}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := buildTestModel()
			m.ready, m.width, m.height = true, size[0], size[1]
			m.selectedWorktree().Branch = strings.Repeat("分支👩‍💻é", 30)
			m.selectedWorktree().WorktreePath = "/work/" + strings.Repeat("long-directory/", 20)
			m.selectedWorktree().EnvFile = strings.Repeat("env-file", 30)
			m.operation = &operationState{name: "Restart", branch: m.selectedWorktree().Branch, output: strings.Repeat("wide 输出 👩‍💻\n", 50), err: errors.New(strings.Repeat("failed ", 50))}
			m.health = map[string]healthReport{m.selectedWorktree().WorktreePath: {output: m.operation.output, err: m.operation.err}}
			m.statusMsg = strings.Repeat("long operation error ", 20)
			for _, focus := range []focusPane{paneList, paneDetail} {
				m.focus = focus
				for _, tab := range []detailTab{tabOverview, tabHealth, tabActivity} {
					m.detailTab = tab
					assertViewFits(t, m.View().Content, m.width, m.height)
				}
			}
			m.showHelp = true
			assertViewFits(t, m.View().Content, m.width, m.height)
			m.showHelp, m.confirmKind, m.confirmTarget = false, "release", m.selectedWorktree()
			assertViewFits(t, m.View().Content, m.width, m.height)
			m.confirmKind, m.inputMode, m.inputText = "", "new_worktree", strings.Repeat("新分支", 40)
			assertViewFits(t, m.View().Content, m.width, m.height)
		})
	}
}

func TestDetailScrollPreservesHeaderAndMakesLastOutputReachable(t *testing.T) {
	m := buildTestModel()
	m.ready, m.height, m.detailTab = true, 16, tabActivity
	m.operation = &operationState{name: "Restart", branch: "other-branch", path: "/other/repo", output: strings.Repeat("intermediate output\n", 40) + "FINAL RECOVERY STEP"}
	width := m.detailPanelWidth()
	before := ansi.Strip(m.renderDetailPanel(width, m.height-statusBarHeight-borderChrome))
	if strings.Contains(before, "FINAL RECOVERY STEP") {
		t.Fatal("expected final output to begin below viewport")
	}
	m.detailScroll = m.detailLineCount(width) - m.detailViewportHeight()
	after := ansi.Strip(m.renderDetailPanel(width, m.height-statusBarHeight-borderChrome))
	if !strings.Contains(after, "FINAL RECOVERY STEP") {
		t.Fatalf("last output not reachable: %s", after)
	}
	for _, content := range []string{before, after} {
		if !strings.Contains(content, "LAST OPERATION") || !strings.Contains(content, "1 Overview  2 Health  3 Activity") {
			t.Fatalf("title and tabs should remain pinned: %s", content)
		}
	}
}

func TestHealthAndActivityExposeFailureAndTarget(t *testing.T) {
	m := buildTestModel()
	m.detailTab = tabHealth
	m.selectedWorktree().WorktreePath = "/repos/api"
	m.health = map[string]healthReport{"/repos/api": {
		output: "FAIL: upstream is unreachable\nRun gtl start to recover.",
		err:    errors.New("doctor reported unhealthy worktree"),
		stale:  true,
	}}
	health := ansi.Strip(strings.Join(m.detailLines(100), "\n"))
	for _, want := range []string{"Needs attention", "unreachable", "gtl start", "Changed since this check", "h to recheck"} {
		if !strings.Contains(health, want) {
			t.Errorf("health report missing %q: %s", want, health)
		}
	}
	m.detailTab = tabActivity
	m.operation = &operationState{name: "Start", branch: "different-branch", path: "/repos/different", output: "setup failed: missing configuration", err: errors.New("exit status 1")}
	activity := ansi.Strip(strings.Join(m.detailLines(100), "\n"))
	for _, want := range []string{"Failed · Start", "Target: different-branch", "/repos/different", "missing configuration", "exit status 1"} {
		if !strings.Contains(activity, want) {
			t.Errorf("activity missing %q: %s", want, activity)
		}
	}
	m.operation.running, m.operation.err, m.operation.output = true, nil, ""
	activity = ansi.Strip(strings.Join(m.detailLines(100), "\n"))
	if !strings.Contains(activity, "Running · Start") || !strings.Contains(activity, "Waiting for command output") {
		t.Fatalf("running state missing: %s", activity)
	}
}

func TestCompactLayoutShowsFocusedPaneAndNavigation(t *testing.T) {
	m := buildTestModel()
	m.ready, m.width, m.height = true, 60, 24
	list := ansi.Strip(m.View().Content)
	if !strings.Contains(list, "WORKTREES") || !strings.Contains(list, "Tab: list/detail") || strings.Contains(list, "1 Overview") {
		t.Fatalf("compact list should show one pane and focus hint: %s", list)
	}
	m.focus = paneDetail
	detail := ansi.Strip(m.View().Content)
	if strings.Contains(detail, "WORKTREES") || !strings.Contains(detail, "1 Overview") || !strings.Contains(detail, "Tab: list") {
		t.Fatalf("compact details should replace list with discoverable return: %s", detail)
	}
}

func TestReleaseConfirmationStatesWhatIsKept(t *testing.T) {
	m := buildTestModel()
	m.confirmTarget = m.selectedWorktree()
	content := ansi.Strip(m.renderConfirmOverlay())
	for _, want := range []string{"Release allocations", "Stops its server", "Keeps the worktree directory and databases", "release hooks", "y: release"} {
		if !strings.Contains(content, want) {
			t.Errorf("release confirmation missing %q: %s", want, content)
		}
	}
}

func TestOutputCannotInjectTerminalControls(t *testing.T) {
	input := "\x1b[31merror\x1b[0m\x1b[2J\r\x00\x07\n\tfix"
	if got := plainOutput(input); got != "error\n    fix" {
		t.Fatalf("unexpected cleaned output: %q", got)
	}
}

func TestViewUsesExactPanelDimensionsWithoutRewrapping(t *testing.T) {
	for _, size := range [][2]int{{120, 32}, {80, 24}, {60, 18}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := buildTestModel()
			m.ready, m.width, m.height, m.focus, m.detailTab = true, size[0], size[1], paneDetail, tabHealth
			width := m.detailPanelWidth()
			m.health = map[string]healthReport{m.selectedWorktree().WorktreePath: {
				output: strings.Repeat(strings.Repeat("x", width)+"\n", 40),
			}}
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			if len(lines) != m.height {
				t.Fatalf("view height = %d, want %d", len(lines), m.height)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != m.width {
					t.Fatalf("row %d width = %d, want %d: %q", i, got, m.width, line)
				}
			}
			left := 0
			if m.width >= compactWidth {
				left = int(float64(m.width) * listPanelRatio)
			}
			bottom := []rune(lines[m.height-statusBarHeight-1])
			if bottom[0] != '╰' || bottom[left] != '╰' || bottom[m.width-1] != '╯' {
				t.Fatalf("panel bottoms do not align above status bar: %q", string(bottom))
			}
			body := m.detailLines(width)
			for i := 0; i < m.detailViewportHeight(); i++ {
				row := []rune(lines[4+i])
				actual := strings.TrimRight(string(row[left+1:left+1+width]), " ")
				if actual != body[i] {
					t.Fatalf("body row %d rewrapped: got %q, want %q", i, actual, body[i])
				}
			}
			if !strings.Contains(lines[m.height-statusBarHeight-2], "j/k: scroll") {
				t.Fatalf("detail footer is not directly above border: %q", lines[m.height-statusBarHeight-2])
			}
			if !strings.Contains(lines[m.height-2], "projects") || !strings.Contains(lines[m.height-1], "Tab:") {
				t.Fatalf("both status lines must remain visible: %q", lines[m.height-2:])
			}
		})
	}
}
