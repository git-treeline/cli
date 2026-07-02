package cmd

import (
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func TestSplitRepoBranch(t *testing.T) {
	cases := []struct {
		in     string
		repo   string
		branch string
		ok     bool
	}{
		{"acme/api#feature-payments", "acme/api", "feature-payments", true},
		{"acme/api#feature/nested", "acme/api", "feature/nested", true},
		{"just-a-branch", "", "", false},
		{"acme/api#", "", "", false},
		{"#branch", "", "", false},
		{"/some/path", "", "", false},
	}
	for _, tc := range cases {
		repo, branch, ok := splitRepoBranch(tc.in)
		if ok != tc.ok || repo != tc.repo || branch != tc.branch {
			t.Errorf("splitRepoBranch(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, repo, branch, ok, tc.repo, tc.branch, tc.ok)
		}
	}
}

func TestResolveRepoRef_ExplicitForm(t *testing.T) {
	// The owner/name#branch form is resolvable without any git repo present.
	ref, err := resolveRepoRef("acme/api#feature", "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	want := registry.RepoRef{Repo: "acme/api", Branch: "feature"}
	if ref != want {
		t.Errorf("got %+v, want %+v", ref, want)
	}
}

// TestBuildRelated_EdgeResolution exercises the read-surface builder end to end
// against a fabricated worktree index, without needing real git repos: an edge
// to a live (repo, branch) resolves to a path; an edge to an absent one is
// reported dangling with a null path.
func TestBuildRelated_EdgeResolution(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New(filepath.Join(dir, "registry.json"))

	self := registry.RepoRef{Repo: "acme/web", Branch: "main"}
	live := registry.RepoRef{Repo: "acme/api", Branch: "feature"}
	gone := registry.RepoRef{Repo: "acme/mobile", Branch: "spec"}

	if _, err := reg.Relate(self, live, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Relate(self, gone, "consumes-api"); err != nil {
		t.Fatal(err)
	}

	// Fabricate an index where only `live` is checked out.
	livePath := "/wt/api-feature"
	idx := &worktreeIndex{
		refByPath: map[string]registry.RepoRef{
			"/wt/web-main": self,
			livePath:       live,
		},
		pathByRef: map[registry.RepoRef]string{
			self: "/wt/web-main",
			live: livePath,
		},
	}

	entries := buildRelated(reg, idx, "/wt/web-main", self)
	if len(entries) != 2 {
		t.Fatalf("expected 2 related entries, got %d", len(entries))
	}

	byRepo := map[string]relatedEntry{}
	for _, e := range entries {
		byRepo[e.Repo] = e
	}

	liveEntry, ok := byRepo["acme/api"]
	if !ok {
		t.Fatal("missing live edge entry")
	}
	if liveEntry.Dangling {
		t.Error("live edge should not be dangling")
	}
	if liveEntry.CreatedAt == "" {
		t.Error("edge entry should carry createdAt for recency ordering")
	}
	if liveEntry.Path == nil || *liveEntry.Path != livePath {
		t.Errorf("live edge path = %v, want %q", liveEntry.Path, livePath)
	}
	if liveEntry.Source != "edge" {
		t.Errorf("expected source 'edge', got %q", liveEntry.Source)
	}

	goneEntry, ok := byRepo["acme/mobile"]
	if !ok {
		t.Fatal("missing dangling edge entry")
	}
	if !goneEntry.Dangling {
		t.Error("edge to absent worktree should be dangling")
	}
	if goneEntry.Path != nil {
		t.Errorf("dangling edge path should be nil, got %v", goneEntry.Path)
	}
	if goneEntry.Type != "consumes-api" {
		t.Errorf("expected type 'consumes-api', got %q", goneEntry.Type)
	}
}
