package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/git-treeline/git-treeline/internal/registry"
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

// --- statusMsg ---

func TestStatusMsgClearedOnTick(t *testing.T) {
	m := buildTestModel()
	m.statusMsg = "env synced"

	result, _ := m.Update(tickMsg{})
	rm := result.(Model)
	if rm.statusMsg != "" {
		t.Errorf("expected statusMsg cleared on tick, got %q", rm.statusMsg)
	}
}

func TestEnvSyncResultMsg_SetsStatusOnSuccess(t *testing.T) {
	m := buildTestModel()
	result, _ := m.Update(envSyncResultMsg{})
	rm := result.(Model)
	if rm.statusMsg != "env synced" {
		t.Errorf("expected 'env synced', got %q", rm.statusMsg)
	}
}

func TestEnvSyncResultMsg_SetsStatusOnError(t *testing.T) {
	m := buildTestModel()
	result, _ := m.Update(envSyncResultMsg{err: errors.New("fail")})
	rm := result.(Model)
	if rm.statusMsg != "env sync failed: fail" {
		t.Errorf("expected error status, got %q", rm.statusMsg)
	}
}

func TestNewWorktreeResultMsg_SetsStatus(t *testing.T) {
	m := buildTestModel()
	result, _ := m.Update(newWorktreeResultMsg{})
	rm := result.(Model)
	if rm.statusMsg != "worktree created" {
		t.Errorf("expected 'worktree created', got %q", rm.statusMsg)
	}
}

func TestNewWorktreeResultMsg_SetsStatusOnError(t *testing.T) {
	m := buildTestModel()
	result, _ := m.Update(newWorktreeResultMsg{err: errors.New("exists")})
	rm := result.(Model)
	if rm.statusMsg != "create failed: exists" {
		t.Errorf("expected error status, got %q", rm.statusMsg)
	}
}

func TestReleaseResultMsg_SetsStatusOnSuccess(t *testing.T) {
	m := buildTestModel()
	result, _ := m.Update(releaseResultMsg{})
	rm := result.(Model)
	if rm.statusMsg != "worktree released" {
		t.Errorf("expected 'worktree released', got %q", rm.statusMsg)
	}
}

func TestReleaseResultMsg_SetsStatusOnError(t *testing.T) {
	m := buildTestModel()
	result, _ := m.Update(releaseResultMsg{err: errors.New("busy")})
	rm := result.(Model)
	if rm.statusMsg != "release failed: busy" {
		t.Errorf("expected error status, got %q", rm.statusMsg)
	}
}

// --- action handoff tests ---

type callRecord struct {
	action string
	args   []string
}

func recordingDeps(calls *[]callRecord) actionDeps {
	return actionDeps{
		supervisorSend: func(sockPath, command string) (string, error) {
			*calls = append(*calls, callRecord{"supervisorSend", []string{sockPath, command}})
			return "ok", nil
		},
		supervisorSock: func(worktreePath string) string {
			return "/tmp/sock/" + worktreePath
		},
		openURL: func(url string) {
			*calls = append(*calls, callRecord{"openURL", []string{url}})
		},
		releaseWorktree: func(wtPath string) error {
			*calls = append(*calls, callRecord{"releaseWorktree", []string{wtPath}})
			return nil
		},
		syncEnv: func(wtPath string) error {
			*calls = append(*calls, callRecord{"syncEnv", []string{wtPath}})
			return nil
		},
		createWorktree: func(existingPath, branch string) error {
			*calls = append(*calls, callRecord{"createWorktree", []string{existingPath, branch}})
			return nil
		},
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
	result := msg.(envSyncResultMsg)
	if result.err != nil {
		t.Errorf("expected nil error, got %v", result.err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].action != "syncEnv" {
		t.Fatalf("expected syncEnv, got %s", calls[0].action)
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
	m.deps.syncEnv = func(string) error {
		return errors.New("permission denied")
	}
	cmd := m.syncEnv()
	msg := cmd()
	result := msg.(envSyncResultMsg)
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
	result := msg.(newWorktreeResultMsg)
	if result.err != nil {
		t.Errorf("expected nil error, got %v", result.err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].action != "createWorktree" {
		t.Fatalf("expected createWorktree, got %s", calls[0].action)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected existing worktree path, got %s", calls[0].args[0])
	}
	if calls[0].args[1] != "feature-new" {
		t.Errorf("expected branch feature-new, got %s", calls[0].args[1])
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
	if calls[0].action != "supervisorSend" {
		t.Fatalf("expected supervisorSend, got %s", calls[0].action)
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
	cmd := m.releaseWorktree()
	cmd()
	if len(calls) != 1 || calls[0].action != "releaseWorktree" {
		t.Fatalf("expected releaseWorktree call, got %v", calls)
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
	if len(calls) != 1 || calls[0].action != "syncEnv" {
		t.Fatalf("expected syncEnv call, got %v", calls)
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
	if len(calls) != 1 || calls[0].action != "createWorktree" {
		t.Fatalf("expected createWorktree call, got %v", calls)
	}
	if calls[0].args[1] != "feature-new" {
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

	result, cmd := m.updateConfirm(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	rm := result.(Model)
	if rm.confirmKind != "" {
		t.Errorf("expected confirmKind cleared, got %q", rm.confirmKind)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from confirm")
	}
	cmd()
	if len(calls) != 1 || calls[0].action != "releaseWorktree" {
		t.Fatalf("expected releaseWorktree call, got %v", calls)
	}
	if calls[0].args[0] != "/home/dev/api" {
		t.Errorf("expected /home/dev/api, got %s", calls[0].args[0])
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
	cmd := m.releaseWorktree()
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
