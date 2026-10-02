package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func TestRemovedAllocations(t *testing.T) {
	before := []registry.Allocation{
		{"worktree": "/wt/a"},
		{"worktree": "/wt/b"},
		{"worktree": "/wt/c"},
	}
	after := []registry.Allocation{
		{"worktree": "/wt/b"},
	}

	removed := removedAllocations(before, after)
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed, got %d", len(removed))
	}
	got := map[string]bool{}
	for _, a := range removed {
		got[a["worktree"].(string)] = true
	}
	if !got["/wt/a"] || !got["/wt/c"] {
		t.Errorf("expected /wt/a and /wt/c removed, got %v", got)
	}
	if got["/wt/b"] {
		t.Error("/wt/b survived and must not be reported removed")
	}
}

func TestRemovedAllocations_NoneRemoved(t *testing.T) {
	set := []registry.Allocation{{"worktree": "/wt/a"}}
	if removed := removedAllocations(set, set); len(removed) != 0 {
		t.Errorf("expected nothing removed, got %d", len(removed))
	}
}

// prunableAllocations selects exactly the allocations whose worktree directory
// no longer exists on disk; live directories are kept.
func TestPrunableAllocations_SelectsMissingDirs(t *testing.T) {
	// Resolve the temp dir so the paths stored by Allocate (which canonicalizes
	// through symlinked ancestors, e.g. macOS /var -> /private/var) compare
	// equal to the literals below.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "live") // exists
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone") // never created

	reg := registry.New(filepath.Join(dir, "registry.json"))
	for _, a := range []registry.Allocation{
		{"worktree": live, "project": "a"},
		{"worktree": gone, "project": "b"},
	} {
		if err := reg.Allocate(a); err != nil {
			t.Fatal(err)
		}
	}

	got := prunableAllocations(reg)
	if len(got) != 1 {
		t.Fatalf("expected exactly one prunable alloc, got %d: %+v", len(got), got)
	}
	if registry.GetString(got[0], "worktree") != gone {
		t.Errorf("expected the missing dir to be prunable, got %+v", got[0])
	}
}

func TestPrunableAllocations_LegacyPathlessReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	legacy := registry.RegistryData{
		Version:     2,
		Allocations: []registry.Allocation{{"project": "legacy-without-worktree"}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	reg := registry.New(path)
	if got := prunableAllocations(reg); len(got) != 0 {
		t.Fatalf("legacy pathless allocation should be retained by read-only preview: %+v", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatal("read-only prune preview rewrote the legacy registry fixture")
	}
}

// gcDanglingEdges drops an edge whose endpoints resolve to no live worktree
// (empty index) and reports nothing when there are no edges at all.
func TestGCDanglingEdges_RemovesUnresolvable(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	a := registry.RepoRef{Repo: "acme/api", Branch: "feature"}
	b := registry.RepoRef{Repo: "acme/web", Branch: "main"}
	if _, err := reg.Relate(a, b, ""); err != nil {
		t.Fatal(err)
	}
	if len(reg.AllEdges()) != 1 {
		t.Fatalf("setup: expected 1 edge, got %d", len(reg.AllEdges()))
	}

	// No allocations => buildWorktreeIndex resolves nothing => edge is dangling.
	gcDanglingEdges(reg)

	if len(reg.AllEdges()) != 0 {
		t.Errorf("expected dangling edge to be garbage-collected, got %d", len(reg.AllEdges()))
	}
}

func TestGCDanglingEdges_NoEdgesNoop(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	gcDanglingEdges(reg) // must not panic or error on an empty registry
	if len(reg.AllEdges()) != 0 {
		t.Errorf("expected no edges, got %d", len(reg.AllEdges()))
	}
}

// surfaceOrphanedBranches is informational and must be a safe no-op when there
// are no allocations to inspect.
func TestSurfaceOrphanedBranches_EmptyRegistry(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	surfaceOrphanedBranches(reg) // no panic, nothing to surface
}
