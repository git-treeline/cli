package cmd

import (
	"os"
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
