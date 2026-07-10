package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

// teardownRuntimeState must stop the supervisor for each released worktree
// that has a path, skip entries with no worktree, and clean hosts exactly once.
func TestTeardownRuntimeStateWith_StopsRightWorktrees(t *testing.T) {
	released := []registry.Allocation{
		{"worktree": "/wt/a", "project": "a"},
		{"project": "no-path"}, // no worktree — must be skipped
		{"worktree": "/wt/b", "project": "b"},
	}

	var stopped []string
	stop := func(wt string) { stopped = append(stopped, wt) }

	var cleanCalls int
	var cleanArg []registry.Allocation
	clean := func(rel []registry.Allocation) { cleanCalls++; cleanArg = rel }

	teardownRuntimeStateWith(released, stop, clean)

	if len(stopped) != 2 || stopped[0] != "/wt/a" || stopped[1] != "/wt/b" {
		t.Errorf("expected supervisors stopped for /wt/a and /wt/b, got %v", stopped)
	}
	if cleanCalls != 1 {
		t.Errorf("expected hosts cleanup called once, got %d", cleanCalls)
	}
	if len(cleanArg) != 3 {
		t.Errorf("hosts cleanup should receive the full released set, got %d", len(cleanArg))
	}
}

// writeWorktreeHooks creates a temp worktree dir with a .treeline.yml
// declaring the given release hooks and returns its path.
func writeWorktreeHooks(t *testing.T, yml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Batch releases must run each worktree's pre_release hook and capture its
// post_release commands; allocations without a worktree pass through untouched.
func TestRunPreReleaseHooks_RunsHooksPerWorktree(t *testing.T) {
	wt := writeWorktreeHooks(t, "hooks:\n  pre_release:\n    - echo pre\n  post_release:\n    - echo post\n")
	allocs := []registry.Allocation{
		{"worktree": wt, "project": "a"},
		{"project": "no-path"},
	}

	type call struct {
		name string
		cmds []string
		dir  string
	}
	var calls []call
	kept, post := runPreReleaseHooks(allocs, false, func(name string, cmds []string, dir string) error {
		calls = append(calls, call{name, cmds, dir})
		return nil
	})

	if len(kept) != 2 {
		t.Fatalf("expected both allocations kept, got %d", len(kept))
	}
	if len(calls) != 1 || calls[0].name != "pre_release" || calls[0].dir != wt {
		t.Fatalf("expected one pre_release call for %s, got %+v", wt, calls)
	}
	if len(calls[0].cmds) != 1 || calls[0].cmds[0] != "echo pre" {
		t.Errorf("expected pre_release commands from .treeline.yml, got %v", calls[0].cmds)
	}
	if len(post) != 1 || post[0].worktree != wt || len(post[0].cmds) != 1 || post[0].cmds[0] != "echo post" {
		t.Errorf("expected post_release commands captured for %s, got %+v", wt, post)
	}
}

// A failing pre_release hook must drop only that allocation from the batch —
// the others still release — and its post_release hook must not be captured.
func TestRunPreReleaseHooks_FailureSkipsOnlyThatWorktree(t *testing.T) {
	bad := writeWorktreeHooks(t, "hooks:\n  pre_release:\n    - exit 1\n  post_release:\n    - echo post\n")
	good := writeWorktreeHooks(t, "hooks:\n  pre_release:\n    - echo pre\n")
	allocs := []registry.Allocation{
		{"worktree": bad, "project": "bad"},
		{"worktree": good, "project": "good"},
	}

	kept, post := runPreReleaseHooks(allocs, false, func(name string, cmds []string, dir string) error {
		if dir == bad {
			return os.ErrPermission
		}
		return nil
	})

	if len(kept) != 1 || kept[0]["worktree"] != good {
		t.Fatalf("expected only the good allocation kept, got %v", kept)
	}
	if len(post) != 0 {
		t.Errorf("failed worktree's post_release must not be captured, got %+v", post)
	}
}

// With force, a failing pre_release hook must warn but not block the release —
// the allocation stays in the batch and its post_release hook still runs.
func TestRunPreReleaseHooks_ForceOverridesFailure(t *testing.T) {
	bad := writeWorktreeHooks(t, "hooks:\n  pre_release:\n    - exit 1\n  post_release:\n    - echo post\n")
	allocs := []registry.Allocation{
		{"worktree": bad, "project": "bad"},
	}

	kept, post := runPreReleaseHooks(allocs, true, func(name string, cmds []string, dir string) error {
		return os.ErrPermission
	})

	if len(kept) != 1 || kept[0]["worktree"] != bad {
		t.Fatalf("force must keep the allocation despite the hook failure, got %v", kept)
	}
	if len(post) != 1 || post[0].worktree != bad {
		t.Errorf("post_release must still be captured under force, got %+v", post)
	}
}

// Worktrees with no .treeline.yml (or a missing dir) must release normally.
func TestRunPreReleaseHooks_NoConfigIsNoop(t *testing.T) {
	allocs := []registry.Allocation{
		{"worktree": filepath.Join(t.TempDir(), "gone"), "project": "a"},
	}
	kept, post := runPreReleaseHooks(allocs, false, func(name string, cmds []string, dir string) error {
		if len(cmds) != 0 {
			t.Errorf("expected no hook commands without config, got %v", cmds)
		}
		return nil
	})
	if len(kept) != 1 {
		t.Errorf("expected allocation kept, got %d", len(kept))
	}
	if len(post) != 0 {
		t.Errorf("expected no post hooks, got %+v", post)
	}
}

func TestHostsCleanupNeeded_NoManagedBlock(t *testing.T) {
	released := []registry.Allocation{{"project": "salt", "branch": "feature-x"}}
	if hostsCleanupNeeded(released, nil, "prt.dev") {
		t.Error("no managed hosts means nothing to clean")
	}
	if hostsCleanupNeeded(released, []string{}, "prt.dev") {
		t.Error("empty managed hosts means nothing to clean")
	}
}

func TestHostsCleanupNeeded_MatchingEntry(t *testing.T) {
	released := []registry.Allocation{{"project": "salt", "branch": "feature-x"}}
	// The managed block contains the route hostname for this allocation.
	managed := []string{"salt-feature-x.prt.dev"}
	if !hostsCleanupNeeded(released, managed, "prt.dev") {
		t.Error("expected cleanup needed when a released route is in the managed block")
	}
}

func TestHostsCleanupNeeded_NoMatchingEntry(t *testing.T) {
	released := []registry.Allocation{{"project": "salt", "branch": "feature-x"}}
	managed := []string{"other-project-main.prt.dev"}
	if hostsCleanupNeeded(released, managed, "prt.dev") {
		t.Error("no released route matches the managed block — must not clean")
	}
}

func TestHostsCleanupNeeded_SkipsProjectlessAllocation(t *testing.T) {
	released := []registry.Allocation{{"branch": "feature-x"}} // no project
	managed := []string{"salt-feature-x.prt.dev"}
	if hostsCleanupNeeded(released, managed, "prt.dev") {
		t.Error("allocation without a project must be skipped")
	}
}

func TestIsInsideDir_Exact(t *testing.T) {
	if !isInsideDir("/a/b/c", "/a/b/c") {
		t.Error("expected true for exact match")
	}
}

func TestIsInsideDir_Child(t *testing.T) {
	if !isInsideDir("/a/b/c/d", "/a/b/c") {
		t.Error("expected true for child path")
	}
}

func TestIsInsideDir_Sibling(t *testing.T) {
	if isInsideDir("/a/b/cd", "/a/b/c") {
		t.Error("expected false for sibling with shared prefix")
	}
}

func TestIsInsideDir_Parent(t *testing.T) {
	if isInsideDir("/a/b", "/a/b/c") {
		t.Error("expected false when cwd is parent of dir")
	}
}

func TestIsInsideDir_Unrelated(t *testing.T) {
	if isInsideDir("/x/y", "/a/b") {
		t.Error("expected false for unrelated paths")
	}
}

func TestIsInsideDir_PlatformSeparator(t *testing.T) {
	sep := string(os.PathSeparator)
	dir := sep + "workspace" + sep + "project"
	cwd := dir + sep + "subdir"
	if !isInsideDir(cwd, dir) {
		t.Error("expected true for child using platform separator")
	}
}
