package cmd

import (
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
