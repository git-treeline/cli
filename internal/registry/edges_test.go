package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func ref(repo, branch string) RepoRef {
	return RepoRef{Repo: repo, Branch: branch}
}

func TestRelate_CreatesEdge(t *testing.T) {
	reg := newTestRegistry(t)
	a := ref("acme/web", "main")
	b := ref("acme/api", "feature")

	created, err := reg.Relate(a, b, "")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created=true for a new edge")
	}

	edges := reg.AllEdges()
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].Type != "related" {
		t.Errorf("expected default type 'related', got %q", edges[0].Type)
	}
	if edges[0].CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

func TestRelate_Idempotent(t *testing.T) {
	reg := newTestRegistry(t)
	a := ref("acme/web", "main")
	b := ref("acme/api", "feature")

	if created, _ := reg.Relate(a, b, ""); !created {
		t.Fatal("first relate should create")
	}
	created, err := reg.Relate(a, b, "")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second relate of same pair should be a no-op (created=false)")
	}
	if got := len(reg.AllEdges()); got != 1 {
		t.Errorf("expected 1 edge after duplicate relate, got %d", got)
	}
}

func TestRelate_Canonical_SymmetricSingleRow(t *testing.T) {
	reg := newTestRegistry(t)
	a := ref("acme/web", "main")
	b := ref("acme/api", "feature")

	_, _ = reg.Relate(a, b, "")
	// Relating in the opposite order must collapse to the same row.
	created, _ := reg.Relate(b, a, "")
	if created {
		t.Error("reverse-order relate should be recognized as the same pair")
	}
	if got := len(reg.AllEdges()); got != 1 {
		t.Fatalf("expected 1 canonical edge, got %d", got)
	}

	// EdgesFor resolves from either endpoint.
	if got := len(reg.EdgesFor(a)); got != 1 {
		t.Errorf("EdgesFor(a): expected 1, got %d", got)
	}
	if got := len(reg.EdgesFor(b)); got != 1 {
		t.Errorf("EdgesFor(b): expected 1, got %d", got)
	}
	if other := reg.EdgesFor(a)[0].Other(a); other != b {
		t.Errorf("Other(a): expected %v, got %v", b, other)
	}
}

func TestUnrelate_Idempotent(t *testing.T) {
	reg := newTestRegistry(t)
	a := ref("acme/web", "main")
	b := ref("acme/api", "feature")
	_, _ = reg.Relate(a, b, "")

	removed, err := reg.Unrelate(b, a) // reverse order still matches
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Error("expected removed=true")
	}
	if got := len(reg.AllEdges()); got != 0 {
		t.Errorf("expected 0 edges after unrelate, got %d", got)
	}

	// Removing again is a no-op success.
	removed, err = reg.Unrelate(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Error("unrelating a missing pair should return removed=false")
	}
}

func TestEdgesFor_FiltersByEndpoint(t *testing.T) {
	reg := newTestRegistry(t)
	a := ref("acme/web", "main")
	b := ref("acme/api", "feature")
	c := ref("acme/mobile", "spec")

	_, _ = reg.Relate(a, b, "")
	_, _ = reg.Relate(a, c, "consumes-api")

	if got := len(reg.EdgesFor(a)); got != 2 {
		t.Errorf("EdgesFor(a): expected 2, got %d", got)
	}
	if got := len(reg.EdgesFor(b)); got != 1 {
		t.Errorf("EdgesFor(b): expected 1, got %d", got)
	}
	if got := len(reg.EdgesFor(ref("nobody/none", "x"))); got != 0 {
		t.Errorf("EdgesFor(unknown): expected 0, got %d", got)
	}
}

// TestMigrate_V1RegistryGainsEdges is the core "update without wiping" test: a
// pre-existing v1 registry.json (allocations, no edges, no version bump) must
// load cleanly, keep every allocation, gain an empty edges slice, and persist
// as v2 on the next write — never requiring the user to delete the file.
func TestMigrate_V1RegistryGainsEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	// Hand-authored v1 payload as it exists on disk today.
	v1 := `{
  "version": 1,
  "allocations": [
    {"worktree": "/wt/a", "project": "salt", "branch": "main", "port": 3010},
    {"worktree": "/wt/b", "project": "salt", "branch": "feature", "port": 3020}
  ]
}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := New(path)

	// Allocations survive the load untouched.
	if got := len(reg.Allocations()); got != 2 {
		t.Fatalf("expected 2 allocations preserved, got %d", got)
	}

	// A write (relate) triggers persistence; the file should now be v2 with the
	// original allocations intact and the new edge present.
	if _, err := reg.Relate(ref("acme/web", "main"), ref("acme/api", "x"), ""); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data RegistryData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Version != currentVersion {
		t.Errorf("expected version %d after migration, got %d", currentVersion, data.Version)
	}
	if len(data.Allocations) != 2 {
		t.Errorf("expected 2 allocations to survive migration, got %d", len(data.Allocations))
	}
	if len(data.Edges) != 1 {
		t.Errorf("expected 1 edge after relate, got %d", len(data.Edges))
	}
	// Spot-check an allocation field survived verbatim.
	if GetString(data.Allocations[0], "project") != "salt" {
		t.Error("allocation data was not preserved through migration")
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	data := RegistryData{Version: 1, Allocations: nil, Edges: nil}
	migrate(&data)
	if data.Version != currentVersion {
		t.Errorf("expected version %d, got %d", currentVersion, data.Version)
	}
	if data.Allocations == nil || data.Edges == nil {
		t.Error("expected non-nil slices after migrate")
	}
	// Second pass must not change anything.
	before := len(data.Edges)
	migrate(&data)
	if len(data.Edges) != before || data.Version != currentVersion {
		t.Error("migrate is not idempotent")
	}
}
