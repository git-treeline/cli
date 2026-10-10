package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/git-treeline/cli/internal/registry"
)

// --- buildFlatList ---

func testSnapshot() Snapshot {
	return Snapshot{
		Projects: []string{"api", "frontend"},
		Worktrees: []WorktreeStatus{
			{Project: "api", Branch: "main", WorktreeName: "api-main", Ports: []int{3000}},
			{Project: "api", Branch: "feature-x", WorktreeName: "api-feature-x", Ports: []int{3010}},
			{Project: "frontend", Branch: "main", WorktreeName: "fe-main", Ports: []int{3020}},
			{Project: "frontend", Branch: "redesign", WorktreeName: "fe-redesign", Ports: []int{3030}},
		},
	}
}

func TestBuildFlatList_NoFilter(t *testing.T) {
	snap := testSnapshot()
	entries := buildFlatList(snap, "")

	// 2 project headers + 4 worktree rows = 6
	if len(entries) != 6 {
		t.Fatalf("expected 6 entries, got %d", len(entries))
	}

	if !entries[0].projectHeader || entries[0].project != "api" {
		t.Errorf("entry 0: expected api header, got %+v", entries[0])
	}
	if entries[1].projectHeader || entries[1].wt.Branch != "main" {
		t.Errorf("entry 1: expected api/main worktree, got %+v", entries[1])
	}
	if !entries[3].projectHeader || entries[3].project != "frontend" {
		t.Errorf("entry 3: expected frontend header, got %+v", entries[3])
	}
}

func TestBuildFlatList_WithFilter(t *testing.T) {
	snap := testSnapshot()
	entries := buildFlatList(snap, "redesign")

	// Only frontend/redesign matches, so: 1 header + 1 worktree = 2
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if !entries[0].projectHeader || entries[0].project != "frontend" {
		t.Errorf("entry 0: expected frontend header, got %+v", entries[0])
	}
	if entries[1].wt.Branch != "redesign" {
		t.Errorf("entry 1: expected redesign worktree, got branch %s", entries[1].wt.Branch)
	}
}

func TestBuildFlatList_FilterMatchesNothing(t *testing.T) {
	snap := testSnapshot()
	entries := buildFlatList(snap, "zzzzz")

	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestBuildFlatList_FilterCaseInsensitive(t *testing.T) {
	snap := testSnapshot()
	entries := buildFlatList(snap, "FEATURE")

	// api/feature-x matches
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (header + worktree), got %d", len(entries))
	}
	if entries[1].wt.Branch != "feature-x" {
		t.Errorf("expected feature-x, got %s", entries[1].wt.Branch)
	}
}

// --- matchesFilter ---

func TestMatchesFilter_Project(t *testing.T) {
	wt := &WorktreeStatus{Project: "MyApp", Branch: "main", WorktreeName: "myapp-main"}
	if !matchesFilter(wt, "myapp") {
		t.Error("expected filter 'myapp' to match project 'MyApp'")
	}
}

func TestMatchesFilter_Branch(t *testing.T) {
	wt := &WorktreeStatus{Project: "api", Branch: "feature-auth", WorktreeName: "api-auth"}
	if !matchesFilter(wt, "auth") {
		t.Error("expected filter 'auth' to match branch 'feature-auth'")
	}
}

func TestMatchesFilter_NoMatch(t *testing.T) {
	wt := &WorktreeStatus{Project: "api", Branch: "main", WorktreeName: "api-main"}
	if matchesFilter(wt, "zzz") {
		t.Error("expected filter 'zzz' to not match")
	}
}

// --- moveCursor ---

func buildTestModel() Model {
	snap := testSnapshot()
	m := Model{
		snapshot: snap,
		flatList: buildFlatList(snap, ""),
		height:   40,
		width:    120,
	}
	m.clampCursor()
	return m
}

func TestMoveCursor_Down(t *testing.T) {
	m := buildTestModel()
	// Should start at index 1 (first non-header)
	if m.cursor != 1 {
		t.Fatalf("expected initial cursor at 1, got %d", m.cursor)
	}

	m.moveCursor(1)
	if m.cursor != 2 {
		t.Errorf("expected cursor at 2 after moving down, got %d", m.cursor)
	}
}

func TestMoveCursor_DownSkipsHeader(t *testing.T) {
	m := buildTestModel()
	m.cursor = 2 // api/feature-x, next is frontend header at 3

	m.moveCursor(1)
	// Should skip header at 3, land on 4 (frontend/main)
	if m.cursor != 4 {
		t.Errorf("expected cursor at 4 (skip header), got %d", m.cursor)
	}
}

func TestMoveCursor_UpSkipsHeader(t *testing.T) {
	m := buildTestModel()
	m.cursor = 4 // frontend/main, prev is header at 3

	m.moveCursor(-1)
	// Should skip header at 3, land on 2 (api/feature-x)
	if m.cursor != 2 {
		t.Errorf("expected cursor at 2 (skip header), got %d", m.cursor)
	}
}

func TestMoveCursor_UpAtTopStays(t *testing.T) {
	m := buildTestModel()
	m.cursor = 1 // first selectable row

	m.moveCursor(-1)
	// Entry 0 is a header, no valid entry above — should stay at 1
	if m.cursor != 1 {
		t.Errorf("expected cursor to stay at 1 at top boundary, got %d", m.cursor)
	}
}

func TestMoveCursor_DownAtBottomStays(t *testing.T) {
	m := buildTestModel()
	last := len(m.flatList) - 1
	m.cursor = last

	m.moveCursor(1)
	if m.cursor != last {
		t.Errorf("expected cursor to stay at %d at bottom boundary, got %d", last, m.cursor)
	}
}

// --- clampCursor ---

func TestClampCursor_NegativeBecomesFirstSelectable(t *testing.T) {
	m := buildTestModel()
	m.cursor = -5
	m.clampCursor()

	if m.cursor < 0 || m.flatList[m.cursor].projectHeader {
		t.Errorf("expected cursor on a selectable row, got %d", m.cursor)
	}
}

func TestClampCursor_BeyondEndClamps(t *testing.T) {
	m := buildTestModel()
	m.cursor = 999
	m.clampCursor()

	if m.cursor >= len(m.flatList) {
		t.Errorf("expected cursor within bounds, got %d (len=%d)", m.cursor, len(m.flatList))
	}
}

// --- renderedLinesBetween ---

func TestRenderedLinesBetween(t *testing.T) {
	m := buildTestModel()
	// entries: [header, wt, wt, header, wt, wt]
	// lines:     2      1   1    2      1   1 = 8 total

	lines := m.renderedLinesBetween(0, 5)
	if lines != 8 {
		t.Errorf("expected 8 rendered lines for full list, got %d", lines)
	}

	// Just the first project group: header(2) + 2 worktrees(2) = 4
	lines = m.renderedLinesBetween(0, 2)
	if lines != 4 {
		t.Errorf("expected 4 lines for entries 0-2, got %d", lines)
	}

	// Single worktree row
	lines = m.renderedLinesBetween(1, 1)
	if lines != 1 {
		t.Errorf("expected 1 line for single worktree, got %d", lines)
	}

	// Single header
	lines = m.renderedLinesBetween(0, 0)
	if lines != 2 {
		t.Errorf("expected 2 lines for single header, got %d", lines)
	}
}

// --- ensureCursorVisible ---

func TestEnsureCursorVisible_ScrollsDown(t *testing.T) {
	m := buildTestModel()
	m.height = 8 // very short — listVisibleLines = 8-2-2-1 = 3
	m.cursor = 5 // last entry
	m.scrollOffset = 0

	m.ensureCursorVisible()

	if m.scrollOffset == 0 {
		t.Error("expected scrollOffset to increase when cursor is below visible area")
	}
}

func TestEnsureCursorVisible_ScrollsUp(t *testing.T) {
	m := buildTestModel()
	m.height = 20 // enough room for visible lines > margin
	m.scrollOffset = 4
	m.cursor = 1

	m.ensureCursorVisible()

	if m.scrollOffset > m.cursor {
		t.Errorf("expected scrollOffset <= cursor, got offset=%d cursor=%d", m.scrollOffset, m.cursor)
	}
}

// --- listIndexAtRow / mouse click ---

func TestListIndexAtRow_AccountsForHeaderRows(t *testing.T) {
	m := buildTestModel()
	// entries: [header api, api/main, api/feature-x, header frontend, fe/main, fe/redesign]
	// rows:     0 blank, 1 "api", 2 idx1, 3 idx2, 4 blank, 5 "frontend", 6 idx4, 7 idx5
	want := map[int]int{
		-1: -1,
		0:  -1,
		1:  -1,
		2:  1,
		3:  2,
		4:  -1,
		5:  -1,
		6:  4,
		7:  5,
		8:  -1,
	}
	for row, idx := range want {
		if got := m.listIndexAtRow(row); got != idx {
			t.Errorf("row %d: got index %d, want %d", row, got, idx)
		}
	}
}

func TestListIndexAtRow_RespectsScrollOffset(t *testing.T) {
	m := buildTestModel()
	m.scrollOffset = 1
	// rows: 0 idx1, 1 idx2, 2 blank, 3 "frontend", 4 idx4, 5 idx5
	want := map[int]int{0: 1, 1: 2, 2: -1, 3: -1, 4: 4, 5: 5, 6: -1}
	for row, idx := range want {
		if got := m.listIndexAtRow(row); got != idx {
			t.Errorf("row %d: got index %d, want %d", row, got, idx)
		}
	}
}

func TestListIndexAtRow_BeyondVisibleArea(t *testing.T) {
	m := buildTestModel()
	m.height = 8 // listVisibleLines = 3: rows 0..2 only
	if got := m.listIndexAtRow(3); got != -1 {
		t.Errorf("row past the visible panel should not select, got %d", got)
	}
}

func TestHandleMouseClick_SelectsClickedWorktree(t *testing.T) {
	m := buildTestModel()
	// Screen row 2 + 6 = 8 is fe/main (flatList index 4); the old direct
	// mapping would have computed index 6, past the end of the list.
	updated, _ := m.handleMouseClick(tea.MouseClickMsg{X: 1, Y: 8, Button: tea.MouseLeft})
	got := updated.(Model)
	if got.cursor != 4 {
		t.Errorf("expected cursor 4 (frontend/main), got %d", got.cursor)
	}
	if got.focus != paneList {
		t.Error("click in list pane should focus the list")
	}

	// Screen row 6 (list row 4) is the blank line before the frontend header: no change.
	updated, _ = got.handleMouseClick(tea.MouseClickMsg{X: 1, Y: 6, Button: tea.MouseLeft})
	if updated.(Model).cursor != 4 {
		t.Errorf("clicking a header row must not move the cursor, got %d", updated.(Model).cursor)
	}
}

// --- extractLinks ---

func TestExtractLinks_WithLinks(t *testing.T) {
	a := registry.Allocation{
		"worktree": "/tmp/test",
		"links": map[string]any{
			"api":   "http://localhost:3040",
			"redis": "redis://localhost:6380",
		},
	}

	links := extractLinks(a)
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links["api"] != "http://localhost:3040" {
		t.Errorf("expected api link, got %s", links["api"])
	}
}

func TestExtractLinks_NoLinks(t *testing.T) {
	a := registry.Allocation{"worktree": "/tmp/test"}
	links := extractLinks(a)
	if links != nil {
		t.Errorf("expected nil links, got %v", links)
	}
}

func TestExtractLinks_EmptyLinksMap(t *testing.T) {
	a := registry.Allocation{
		"worktree": "/tmp/test",
		"links":    map[string]any{},
	}
	links := extractLinks(a)
	if links != nil {
		t.Errorf("expected nil for empty links map, got %v", links)
	}
}

func TestExtractLinks_NonStringValues(t *testing.T) {
	a := registry.Allocation{
		"links": map[string]any{
			"api":   42,
			"redis": "redis://localhost",
		},
	}
	links := extractLinks(a)
	if len(links) != 1 {
		t.Fatalf("expected 1 link (non-string filtered), got %d", len(links))
	}
	if links["redis"] != "redis://localhost" {
		t.Errorf("expected redis link, got %s", links["redis"])
	}
}

// --- input mode ---

func TestInputMode_EnterClearsMode(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.inputMode = "new_worktree"
	m.inputText = "feature-x"

	result, _ := m.updateInput(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	rm := result.(Model)
	if rm.inputMode != "" {
		t.Errorf("expected inputMode cleared, got %q", rm.inputMode)
	}
}

func TestInputMode_EscapeCancels(t *testing.T) {
	m := buildTestModel()
	m.inputMode = "new_worktree"
	m.inputText = "feature-x"

	result, _ := m.updateInput(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	rm := result.(Model)
	if rm.inputMode != "" {
		t.Errorf("expected inputMode cleared, got %q", rm.inputMode)
	}
	if rm.inputText != "" {
		t.Errorf("expected inputText cleared, got %q", rm.inputText)
	}
}

func TestInputMode_EmptyEnterDoesNothing(t *testing.T) {
	m := buildTestModel()
	m.inputMode = "new_worktree"
	m.inputText = ""

	result, cmd := m.updateInput(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	rm := result.(Model)
	if rm.inputMode != "" {
		t.Errorf("expected inputMode cleared, got %q", rm.inputMode)
	}
	if cmd != nil {
		t.Error("expected nil cmd for empty input")
	}
}

func TestInputMode_WhitespaceOnlyDoesNothing(t *testing.T) {
	m := buildTestModel()
	m.inputMode = "new_worktree"
	m.inputText = "   "

	_, cmd := m.updateInput(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd != nil {
		t.Error("expected nil cmd for whitespace-only input")
	}
}

func TestInputMode_RejectsWhitespaceCharacters(t *testing.T) {
	m := buildTestModel()
	m.inputMode = "new_worktree"
	m.inputText = "feat"

	result, _ := m.updateInput(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	rm := result.(Model)
	if rm.inputText != "feat" {
		t.Errorf("expected space rejected, got %q", rm.inputText)
	}
}

func TestGtlBinaryPath_ResolvesRunningExecutable(t *testing.T) {
	got := gtlBinaryPath()
	if got == "" {
		t.Fatal("expected non-empty binary path")
	}
	// os.Executable succeeds under the test runner, so an absolute path to the
	// running binary is expected rather than the bare fallback name.
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestTruncate_RuneAware(t *testing.T) {
	// A multibyte (CJK) branch name must be cut on a rune boundary and stay
	// within the cell-width budget.
	s := "特性ブランチテスト"
	got := truncate(s, 6)
	if !utf8.ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	if w := lipgloss.Width(got); w > 6 {
		t.Errorf("truncate exceeded width budget: width=%d, %q", w, got)
	}

	// A multibyte string within the budget is returned unchanged.
	if got := truncate("café", 10); got != "café" {
		t.Errorf("expected café unchanged, got %q", got)
	}
}

// --- poll error handling ---

func TestDataMsg_FailedPollRetainsSnapshotAndSurfacesError(t *testing.T) {
	m := buildTestModel()
	m.ready = true
	prior := m.snapshot

	result, _ := m.Update(dataMsg{err: errors.New("registry is corrupt")})
	rm := result.(Model)

	if rm.pollErr == nil {
		t.Fatal("expected pollErr to be set on failed poll")
	}
	if len(rm.snapshot.Worktrees) != len(prior.Worktrees) {
		t.Errorf("expected prior snapshot retained (%d worktrees), got %d",
			len(prior.Worktrees), len(rm.snapshot.Worktrees))
	}
	if len(rm.flatList) == 0 {
		t.Error("expected flatList retained, got empty")
	}

	bar := rm.renderStatusBar(120)
	if !strings.Contains(bar, "poll failed") {
		t.Errorf("expected status bar to surface poll failure, got %q", bar)
	}

	// A subsequent successful poll clears the error and updates the snapshot.
	next := Snapshot{Projects: []string{"api"}, Worktrees: []WorktreeStatus{
		{Project: "api", Branch: "main", WorktreeName: "api-main", Ports: []int{3000}},
	}}
	result, _ = rm.Update(dataMsg{snapshot: next})
	rm = result.(Model)
	if rm.pollErr != nil {
		t.Errorf("expected pollErr cleared after successful poll, got %v", rm.pollErr)
	}
	if len(rm.snapshot.Worktrees) != 1 {
		t.Errorf("expected snapshot updated to 1 worktree, got %d", len(rm.snapshot.Worktrees))
	}
}

// --- action handoff tests ---

type callRecord struct {
	action string
	args   []string
}

func recordingDeps(calls *[]callRecord) actionDeps {
	return actionDeps{
		runCommand: func(_ context.Context, path string, args ...string) (string, error) {
			*calls = append(*calls, callRecord{"runCommand", append([]string{path}, args...)})
			return "ok", nil
		},
		openURL: func(url string) { *calls = append(*calls, callRecord{"openURL", []string{url}}) },
	}
}

func buildTestModelWithDeps(calls *[]callRecord) Model {
	snap := Snapshot{
		Projects: []string{"api"},
		Worktrees: []WorktreeStatus{
			{
				Project:      "api",
				Branch:       "main",
				WorktreeName: "api-main",
				WorktreePath: "/home/dev/api",
				Ports:        []int{3000},
				RouterURL:    "https://api-main.prt.dev",
				Supervisor:   "running",
			},
			{
				Project:      "api",
				Branch:       "feature-x",
				WorktreeName: "api-feature-x",
				WorktreePath: "/home/dev/api-feature-x",
				Ports:        []int{3010},
				RouterURL:    "",
				Supervisor:   "stopped",
			},
		},
	}
	m := Model{
		snapshot: snap,
		flatList: buildFlatList(snap, ""),
		height:   40,
		width:    120,
		deps:     recordingDeps(calls),
	}
	m.clampCursor()
	return m
}

func TestOpenInBrowser_UsesRouterURL(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	// cursor is on api/main which has RouterURL set
	m.openInBrowser()

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].action != "openURL" {
		t.Fatalf("expected openURL, got %s", calls[0].action)
	}
	if calls[0].args[0] != "https://api-main.prt.dev" {
		t.Errorf("expected router URL, got %s", calls[0].args[0])
	}
}

func TestOpenInBrowser_FallsBackToLocalhost(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 2 // api/feature-x, no RouterURL
	m.openInBrowser()

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].args[0] != "http://localhost:3010" {
		t.Errorf("expected localhost fallback, got %s", calls[0].args[0])
	}
}

func TestEnvSync_PassesSelectedWorktreePath(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	cmd := m.syncEnv()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	result := msg.(operationResultMsg).operation
	if result.err != nil {
		t.Errorf("expected nil error, got %v", result.err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand got %s", calls[0].action)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected /home/dev/api, got %s", calls[0].args[0])
	}
}

func TestEnvSync_NilWhenNoSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 0 // header
	cmd := m.syncEnv()
	if cmd != nil {
		t.Error("expected nil cmd when on header")
	}
}

func TestEnvSync_PropagatesError(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.deps.runCommand = func(context.Context, string, ...string) (string, error) {
		return "diagnostic output", errors.New("permission denied")
	}
	cmd := m.syncEnv()
	msg := cmd()
	result := msg.(operationResultMsg).operation
	if result.err == nil || result.err.Error() != "permission denied" {
		t.Errorf("expected permission denied error, got %v", result.err)
	}
}

func TestCreateWorktree_PassesBranchAndExistingPath(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	cmd := m.createWorktree("feature-new")
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	result := msg.(operationResultMsg).operation
	if result.err != nil {
		t.Errorf("expected nil error, got %v", result.err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand got %s", calls[0].action)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected existing worktree path, got %s", calls[0].args[0])
	}
	if calls[0].args[4] != "feature-new" {
		t.Errorf("expected branch feature-new, got %s", calls[0].args[4])
	}
}

func TestCreateWorktree_NilWhenNoSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 0 // header row — selectedWorktree() returns nil
	cmd := m.createWorktree("feature-new")
	if cmd != nil {
		t.Error("expected nil cmd when no worktree selected")
	}
}

func TestToggleSupervisor_SendsStop(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	// cursor on api/main, supervisor="running"
	cmd := m.toggleSupervisor()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	cmd()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand, got %s", calls[0].action)
	}
	if calls[0].args[1] != "stop" {
		t.Errorf("expected stop command for running supervisor, got %s", calls[0].args[1])
	}
}

func TestToggleSupervisor_SendsStart(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 2 // api/feature-x, supervisor="stopped"
	cmd := m.toggleSupervisor()
	cmd()
	if calls[0].args[1] != "start" {
		t.Errorf("expected start command for stopped supervisor, got %s", calls[0].args[1])
	}
}

func TestRestartSupervisor_SendsRestart(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	cmd := m.restartSupervisor()
	cmd()
	if calls[0].args[1] != "restart" {
		t.Errorf("expected restart command, got %s", calls[0].args[1])
	}
}

func TestReleaseWorktree_PassesPath(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	cmd := m.releaseWorktree(m.selectedWorktree())
	cmd()
	if len(calls) != 1 || calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand, got %v", calls)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected /home/dev/api, got %s", calls[0].args[0])
	}
}

func TestKeyE_TriggersEnvSync(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.ready = true
	_, cmd := m.updateNormal(tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"}))
	if cmd == nil {
		t.Fatal("expected non-nil cmd from 'e' key")
	}
	cmd()
	if len(calls) != 1 || calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand, got %v", calls)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected /home/dev/api, got %s", calls[0].args[0])
	}
}

func TestKeyN_EntersInputMode(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	result, _ := m.updateNormal(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	rm := result.(Model)
	if rm.inputMode != "new_worktree" {
		t.Errorf("expected inputMode=new_worktree, got %q", rm.inputMode)
	}
}

func TestInputMode_EnterTriggersCreate(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.inputMode = "new_worktree"
	m.inputTarget = m.selectedWorktree()
	m.inputText = "feature-new"

	result, cmd := m.updateInput(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	rm := result.(Model)
	if rm.inputMode != "" {
		t.Errorf("expected inputMode cleared, got %q", rm.inputMode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	// Execute the cmd and verify the handoff
	cmd()
	if len(calls) != 1 || calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand, got %v", calls)
	}
	if calls[0].args[4] != "feature-new" {
		t.Errorf("expected branch feature-new, got %s", calls[0].args[1])
	}
}

// --- selectedWorktree ---

func TestSelectedWorktree_ValidCursor(t *testing.T) {
	m := buildTestModel()
	m.cursor = 1
	wt := m.selectedWorktree()
	if wt == nil {
		t.Fatal("expected non-nil worktree")
	}
	if wt.Branch != "main" || wt.Project != "api" {
		t.Errorf("expected api/main, got %s/%s", wt.Project, wt.Branch)
	}
}

func TestSelectedWorktree_OnHeader(t *testing.T) {
	m := buildTestModel()
	m.cursor = 0 // header
	wt := m.selectedWorktree()
	if wt != nil {
		t.Errorf("expected nil when cursor is on header, got %+v", wt)
	}
}

func TestSelectedWorktree_OutOfBounds(t *testing.T) {
	m := buildTestModel()
	m.cursor = -1
	if m.selectedWorktree() != nil {
		t.Error("expected nil for negative cursor")
	}
	m.cursor = 999
	if m.selectedWorktree() != nil {
		t.Error("expected nil for out-of-bounds cursor")
	}
}

// --- confirm flow ---

func TestKeyD_SetsConfirmKind(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	result, _ := m.updateNormal(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
	rm := result.(Model)
	if rm.confirmKind != "release" {
		t.Errorf("expected confirmKind=release, got %q", rm.confirmKind)
	}
}

func TestKeyD_NoOpWhenNoSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 0 // header row
	result, _ := m.updateNormal(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
	rm := result.(Model)
	if rm.confirmKind != "" {
		t.Errorf("expected no confirmKind on header, got %q", rm.confirmKind)
	}
}

func TestConfirm_YReleasesWorktree(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.confirmKind = "release"
	m.confirmTarget = m.selectedWorktree()

	result, cmd := m.updateConfirm(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	rm := result.(Model)
	if rm.confirmKind != "" {
		t.Errorf("expected confirmKind cleared, got %q", rm.confirmKind)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from confirm")
	}
	cmd()
	if len(calls) != 1 || calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand, got %v", calls)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected /home/dev/api, got %s", calls[0].args[0])
	}
}

func TestConfirm_ReleasesCapturedTargetAfterListReorder(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	// cursor is on api/main (path /home/dev/api)

	// Open the release-confirm overlay on the selected worktree.
	res, _ := m.updateNormal(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
	m = res.(Model)
	if m.confirmKind != "release" || m.confirmTarget == nil {
		t.Fatalf("expected release confirm open, got kind=%q target=%v", m.confirmKind, m.confirmTarget)
	}
	if m.confirmTarget.WorktreePath != "/home/dev/api" {
		t.Fatalf("expected captured target /home/dev/api, got %s", m.confirmTarget.WorktreePath)
	}

	// A background poll rebuilds the list and shrinks/reorders it so the
	// cursor now points at a different worktree while the overlay is open.
	reordered := Snapshot{
		Projects: []string{"api"},
		Worktrees: []WorktreeStatus{
			{Project: "api", Branch: "feature-x", WorktreeName: "api-feature-x", WorktreePath: "/home/dev/api-feature-x", Ports: []int{3010}},
		},
	}
	res, _ = m.Update(dataMsg{snapshot: reordered})
	m = res.(Model)

	if sw := m.selectedWorktree(); sw == nil || sw.WorktreePath != "/home/dev/api-feature-x" {
		t.Fatalf("expected cursor to have moved to api-feature-x, got %v", sw)
	}

	// Confirming must release the originally-captured api/main, not whatever
	// the cursor now points at.
	_, cmd := m.updateConfirm(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	if cmd == nil {
		t.Fatal("expected non-nil release cmd")
	}
	cmd()
	if len(calls) != 1 || calls[0].action != "runCommand" {
		t.Fatalf("expected runCommand, got %v", calls)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected originally-selected /home/dev/api released, got %s", calls[0].args[0])
	}
}

func TestConfirm_OtherKeyCancels(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.confirmKind = "release"

	result, cmd := m.updateConfirm(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	rm := result.(Model)
	if rm.confirmKind != "" {
		t.Errorf("expected confirmKind cleared on cancel, got %q", rm.confirmKind)
	}
	if cmd != nil {
		t.Error("expected nil cmd on cancel")
	}
	if len(calls) != 0 {
		t.Errorf("expected no calls on cancel, got %v", calls)
	}
}

// --- nil-guard tests ---

func TestToggleSupervisor_NilWhenNoSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 0 // header
	cmd := m.toggleSupervisor()
	if cmd != nil {
		t.Error("expected nil cmd when on header")
	}
}

func TestRestartSupervisor_NilWhenNoSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 0 // header
	cmd := m.restartSupervisor()
	if cmd != nil {
		t.Error("expected nil cmd when on header")
	}
}

func TestReleaseWorktree_NilWhenNoSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 0 // header
	cmd := m.releaseWorktree(m.selectedWorktree())
	if cmd != nil {
		t.Error("expected nil cmd when on header")
	}
}

func TestOpenInBrowser_NoOpWhenNoSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 0 // header
	m.openInBrowser()
	if len(calls) != 0 {
		t.Errorf("expected no calls when on header, got %v", calls)
	}
}

func TestOpenInBrowser_NoOpWhenNoPorts(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.flatList[m.cursor].wt.Ports = nil
	m.openInBrowser()
	if len(calls) != 0 {
		t.Errorf("expected no calls when no ports, got %v", calls)
	}
}

func press(m Model, key string) (Model, tea.Cmd) {
	result, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key}))
	return result.(Model), cmd
}

func TestActionsUseCanonicalCLIInSelectedDirectory(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		cursor int
		args   []string
	}{
		{"stop", "s", 1, []string{"stop"}},
		{"cold start", "s", 2, []string{"start", "--await"}},
		{"restart", "r", 2, []string{"restart"}},
		{"env sync", "e", 2, []string{"env", "sync"}},
		{"health", "h", 2, []string{"doctor"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []callRecord
			m := buildTestModelWithDeps(&calls)
			m.cursor = tt.cursor
			target := m.selectedPath()
			m, cmd := press(m, tt.key)
			if cmd == nil || !m.busy() {
				t.Fatal("action must immediately enter running state")
			}
			m.cursor = 1
			cmd()
			want := append([]string{target}, tt.args...)
			if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, want) {
				t.Fatalf("calls=%v, want %v", calls, want)
			}
		})
	}
}

func TestOperationErrorAndOutputPersistAcrossPolling(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.deps.runCommand = func(context.Context, string, ...string) (string, error) {
		return "port 3000 is in use", errors.New("start failed")
	}
	m, cmd := press(m, "s")
	result, _ := m.Update(cmd())
	m = result.(Model)
	if m.busy() || m.operation.err == nil || m.operation.output != "port 3000 is in use" {
		t.Fatalf("lost operation result: %+v", m.operation)
	}
	status := m.statusMsg
	result, _ = m.Update(tickMsg{})
	m = result.(Model)
	result, _ = m.Update(dataMsg{snapshot: m.snapshot})
	m = result.(Model)
	if m.statusMsg != status || !strings.Contains(status, "failed") {
		t.Fatalf("operation failure was cleared: %q", m.statusMsg)
	}
}

func TestBusyOperationBlocksOtherActionsAndPrompts(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m, pending := press(m, "r")
	for _, key := range []string{"s", "r", "e", "h", "n", "d"} {
		var cmd tea.Cmd
		m, cmd = press(m, key)
		if cmd != nil || m.inputMode != "" || m.confirmKind != "" {
			t.Fatalf("%q initiated another operation", key)
		}
	}
	pending()
	if len(calls) != 1 {
		t.Fatalf("got %d calls", len(calls))
	}
}

func TestHealthResultBelongsToCapturedWorktreeAndBecomesStale(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	target := m.selectedPath()
	m, cmd := press(m, "h")
	m.cursor = 2
	result, _ := m.Update(cmd())
	m = result.(Model)
	if m.health[target].output != "ok" || m.health[target].stale {
		t.Fatalf("missing fresh report for %s", target)
	}
	if _, ok := m.health[m.selectedPath()]; ok {
		t.Fatal("health report assigned to current selection")
	}
	m.cursor = 1
	m, _ = press(m, "e")
	if !m.health[target].stale {
		t.Fatal("mutation must invalidate prior health diagnosis")
	}
}

func TestPollPreservesSelectedWorktreeAcrossInsertion(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.cursor = 2
	target := m.selectedPath()
	snap := m.snapshot
	snap.Worktrees = append([]WorktreeStatus{{Project: "api", WorktreePath: "/new", Branch: "new"}}, snap.Worktrees...)
	result, _ := m.Update(dataMsg{snapshot: snap})
	m = result.(Model)
	if m.selectedPath() != target || m.cursor != 3 {
		t.Fatalf("selection changed after poll: %s at %d", m.selectedPath(), m.cursor)
	}
}

func TestNewWorktreeUsesPromptTargetAfterPollRemovesSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	target := m.selectedPath()
	m, _ = press(m, "n")
	snap := m.snapshot
	snap.Worktrees = snap.Worktrees[1:]
	result, _ := m.Update(dataMsg{snapshot: snap})
	m = result.(Model)
	m.inputText = "feature-new"
	result, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = result.(Model)
	if cmd == nil || !m.busy() {
		t.Fatal("create did not start")
	}
	cmd()
	want := []string{target, "new", "--force", "--", "feature-new"}
	if !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("got %v, want %v", calls[0].args, want)
	}
}

func TestQuitCancelsActiveCommandFromEveryMode(t *testing.T) {
	for _, mode := range []string{"normal", "input", "filter", "help", "confirm"} {
		t.Run(mode, func(t *testing.T) {
			var calls []callRecord
			m := buildTestModelWithDeps(&calls)
			var commandContext context.Context
			m.deps.runCommand = func(ctx context.Context, _ string, _ ...string) (string, error) { commandContext = ctx; return "", nil }
			m, cmd := press(m, "r")
			completion := cmd()
			switch mode {
			case "input":
				m.inputMode = "new_worktree"
			case "filter":
				m.filterMode = true
			case "help":
				m.showHelp = true
			case "confirm":
				m.confirmKind = "release"
			}
			result, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
			m = result.(Model)
			if m.quitting || !m.pendingQuit || commandContext.Err() != context.Canceled {
				t.Fatal("quit must cancel and wait for active command completion")
			}
			result, quitCmd := m.Update(completion)
			if !result.(Model).quitting || quitCmd == nil {
				t.Fatal("command completion must finish pending quit")
			}
		})
	}
}

func TestDetailFocusScrollsWithoutChangingSelection(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.height = 12
	m.operation = &operationState{name: "Health", output: strings.Repeat("diagnosis\n", 80)}
	m, _ = press(m, "3")
	cursor := m.cursor
	m, _ = press(m, "j")
	if m.detailScroll != 1 || m.cursor != cursor {
		t.Fatalf("detail navigation moved selection or failed to scroll: %+v", m)
	}
	result, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	m = result.(Model)
	want := m.detailLineCount(m.detailPanelWidth()) - m.detailViewportHeight()
	if m.detailScroll != want {
		t.Fatalf("end scroll=%d, want %d", m.detailScroll, want)
	}
	result, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m = result.(Model)
	if m.detailScroll != 0 {
		t.Fatal("home must scroll to top")
	}
	result, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	m = result.(Model)
	m, _ = press(m, "j")
	if m.cursor == cursor {
		t.Fatal("left must return navigation to list")
	}
}

func TestMutationFocusesActivityOutput(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.detailTab = tabHealth
	m.detailScroll = 10
	m, _ = press(m, "r")
	if m.detailTab != tabActivity || m.focus != paneDetail || m.detailScroll != 0 {
		t.Fatal("mutation must reveal activity output from the beginning")
	}
}

func TestBackspacePreservesUnicode(t *testing.T) {
	m := buildTestModel()
	m.filterMode = true
	m.filterText = "feature-特"
	result, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	m = result.(Model)
	if m.filterText != "feature-" {
		t.Fatalf("backspace corrupted filter: %q", m.filterText)
	}
	m.filterMode = false
	m.inputMode = "new_worktree"
	m.inputText = "feature-特"
	result, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if result.(Model).inputText != "feature-" {
		t.Fatalf("backspace corrupted branch: %q", result.(Model).inputText)
	}
}

func TestPollingDoesNotOverlap(t *testing.T) {
	m := buildTestModel()
	result, cmd := m.Update(tickMsg{})
	m = result.(Model)
	if !m.polling || cmd == nil {
		t.Fatal("idle tick must start polling")
	}
	// A new poll and its next tick are returned as a batch. While that poll
	// remains pending, another tick must schedule only the subsequent tick.
	if _, ok := cmd().(tea.BatchMsg); !ok {
		t.Fatal("idle tick must batch poll and next tick")
	}
	result, _ = m.Update(tickMsg{})
	m = result.(Model)
	if !m.polling {
		t.Fatal("tick must preserve pending poll")
	}
	result, cmd = m.Update(operationResultMsg{operation: operationState{name: "Restart"}})
	m = result.(Model)
	if cmd != nil || !m.polling {
		t.Fatal("operation completion must not start an overlapping poll")
	}
	result, _ = m.Update(dataMsg{snapshot: m.snapshot})
	m = result.(Model)
	if m.polling {
		t.Fatal("poll result must clear pending state")
	}
	result, cmd = m.Update(operationResultMsg{operation: operationState{name: "Restart"}})
	if cmd == nil || !result.(Model).polling {
		t.Fatal("operation completion should poll when idle")
	}
}

func TestFilterFromCompactDetailsRevealsList(t *testing.T) {
	m := buildTestModel()
	m.width = compactWidth - 1
	m.focus = paneDetail
	m.detailTab = tabActivity
	m, _ = press(m, "/")
	if !m.filterMode || m.focus != paneList {
		t.Fatal("filter must reveal its input and worktree list from compact details")
	}
	m, _ = press(m, "redesign")
	if wt := m.selectedWorktree(); wt == nil || wt.Branch != "redesign" {
		t.Fatalf("filter did not select matching worktree: %v", wt)
	}
}

func TestResizeKeepsSelectedWorktreeVisible(t *testing.T) {
	m := buildTestModel()
	m.cursor = len(m.flatList) - 1
	m.scrollOffset = 0
	selected := m.selectedWorktree().Branch
	result, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 8})
	m = result.(Model)
	if m.selectedWorktree().Branch != selected {
		t.Fatal("resize changed selected worktree")
	}
	if lines := m.renderedLinesBetween(m.scrollOffset, m.cursor); lines > m.listVisibleLines() {
		t.Fatalf("selected worktree needs %d rows but viewport has %d", lines, m.listVisibleLines())
	}
}

func TestHealthRetainsCheckedBranchAfterExternalSwitch(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending=%v", pending), func(t *testing.T) {
			var calls []callRecord
			m := buildTestModelWithDeps(&calls)
			path := m.selectedPath()
			m, cmd := press(m, "h")
			completion := cmd()
			if !pending {
				result, _ := m.Update(completion)
				m = result.(Model)
			}
			snap := m.snapshot
			snap.Worktrees = append([]WorktreeStatus(nil), snap.Worktrees...)
			snap.Worktrees[0].Branch = "switched-branch"
			result, _ := m.Update(dataMsg{snapshot: snap})
			m = result.(Model)
			if pending {
				result, _ = m.Update(completion)
				m = result.(Model)
			}
			if report := m.health[path]; report.branch != "main" || !report.stale {
				t.Fatalf("report was relabeled or stayed fresh: %+v", report)
			}
		})
	}
}

func TestHealthForRemovedTargetIsStale(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	path := m.selectedPath()
	m, cmd := press(m, "h")
	snap := m.snapshot
	snap.Worktrees = snap.Worktrees[1:]
	result, _ := m.Update(dataMsg{snapshot: snap})
	m = result.(Model)
	result, _ = m.Update(cmd())
	m = result.(Model)
	if !m.health[path].stale {
		t.Fatal("removed worktree report must not remain fresh")
	}
}

func TestReleaseRequiresVisibleConfirmation(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m, _ = press(m, "d")
	target := m.confirmTarget
	m.width, m.height = 30, 8
	m, cmd := press(m, "y")
	if cmd != nil || m.confirmKind != "release" || m.confirmTarget != target {
		t.Fatal("clipped confirmation must remain open without releasing")
	}
	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = result.(Model)
	m, cmd = press(m, "y")
	if cmd == nil || m.confirmKind != "" {
		t.Fatal("resized visible confirmation must allow release")
	}
	cmd()
	if len(calls) != 1 {
		t.Fatalf("expected one release, got %v", calls)
	}
	m = buildTestModelWithDeps(&calls)
	m, _ = press(m, "d")
	m.width, m.height = 30, 8
	m, cmd = press(m, "n")
	if cmd != nil || m.confirmKind != "" {
		t.Fatal("small confirmation must allow cancellation")
	}
}
