package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkClone(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mkWorktreeMarker(t *testing.T, dir, parent string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"),
		[]byte("gitdir: "+parent+"/.git/worktrees/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_HealthyRegistry(t *testing.T) {
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)
	a := filepath.Join(dir, "wt-a")
	b := filepath.Join(dir, "wt-b")
	mkClone(t, a)
	mkClone(t, b)

	_ = reg.Allocate(Allocation{
		"project": "alpha", "branch": "main", "worktree": a, "port": float64(3002),
	})
	_ = reg.Allocate(Allocation{
		"project": "beta", "branch": "main", "worktree": b, "port": float64(3004),
	})

	issues, err := reg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got: %+v", issues)
	}
}

func TestValidate_FlagsMissingWorktree(t *testing.T) {
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)
	keep := filepath.Join(dir, "keep")
	gone := filepath.Join(dir, "gone")
	mkClone(t, keep)

	_ = reg.Allocate(Allocation{
		"project": "k", "branch": "main", "worktree": keep, "port": float64(3002),
	})
	_ = reg.Allocate(Allocation{
		"project": "g", "branch": "main", "worktree": gone, "port": float64(3004),
	})

	issues, err := reg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, iss := range issues {
		if iss.Kind == "missing_worktree" && iss.Worktree == gone {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing_worktree issue for %s, got: %+v", gone, issues)
	}
}

func TestValidate_FlagsCrossEntryDuplicatePort(t *testing.T) {
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	mkClone(t, a)
	mkClone(t, b)

	_ = reg.Allocate(Allocation{"project": "a", "branch": "x", "worktree": a, "port": float64(3022)})
	_ = reg.Allocate(Allocation{"project": "b", "branch": "y", "worktree": b, "port": float64(3022)})

	issues, err := reg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	dup := false
	for _, iss := range issues {
		if iss.Kind == "duplicate_port" && strings.Contains(iss.Detail, "3022") {
			dup = true
		}
	}
	if !dup {
		t.Errorf("expected duplicate_port issue mentioning 3022, got: %+v", issues)
	}
}

func TestValidate_DoesNotFlagSamePortInLegacyAndPortsArray(t *testing.T) {
	// Real-world entries have both `port: 3022` and `ports: [3022]` set on the
	// same allocation — that's not a duplicate, just legacy field redundancy.
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)
	a := filepath.Join(dir, "a")
	mkClone(t, a)

	_ = reg.Allocate(Allocation{
		"project": "p", "branch": "main", "worktree": a,
		"port":  float64(3022),
		"ports": []any{float64(3022)},
	})
	issues, err := reg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range issues {
		if iss.Kind == "duplicate_port" {
			t.Errorf("port + ports[] on the same entry should not look like duplicates: %+v", iss)
		}
	}
}

func TestValidate_FlagsDuplicateBranch(t *testing.T) {
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	mkClone(t, a)
	mkClone(t, b)

	_ = reg.Allocate(Allocation{"project": "salt", "branch": "feature", "worktree": a, "port": float64(3010)})
	_ = reg.Allocate(Allocation{"project": "salt", "branch": "feature", "worktree": b, "port": float64(3012)})

	issues, err := reg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	dup := false
	for _, iss := range issues {
		if iss.Kind == "duplicate_branch" {
			dup = true
		}
	}
	if !dup {
		t.Errorf("expected duplicate_branch, got: %+v", issues)
	}
}

func TestValidate_FlagsMissingPorts(t *testing.T) {
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)
	a := filepath.Join(dir, "a")
	mkClone(t, a)

	_ = reg.Allocate(Allocation{"project": "p", "branch": "main", "worktree": a})
	issues, err := reg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	missing := false
	for _, iss := range issues {
		if iss.Kind == "missing_ports" {
			missing = true
		}
	}
	if !missing {
		t.Errorf("expected missing_ports, got: %+v", issues)
	}
}

func TestPruneStale_PreservesStandaloneClones(t *testing.T) {
	// Regression: gtl prune --stale used to flag every entry that wasn't in
	// `git worktree list` of the current repo as stale. Standalone clones
	// (Conductor workspaces) have their own .git directory and are not part
	// of any parent repo's worktree list, so they got nuked. Now PruneStale
	// recognizes them by ".git is a directory" and preserves them.
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)

	clone := filepath.Join(dir, "conductor-workspace")
	mkClone(t, clone)

	_ = reg.Allocate(Allocation{
		"project": "clients", "branch": "feature/x", "worktree": clone, "port": float64(3022),
	})

	count, err := reg.PruneStale()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 prunes, got %d", count)
	}
	if reg.Find(clone) == nil {
		t.Error("standalone clone entry got pruned — Conductor regression")
	}
}

func TestPruneStale_RemovesMissingDirectory(t *testing.T) {
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)

	gone := filepath.Join(dir, "gone")
	_ = reg.Allocate(Allocation{
		"project": "g", "branch": "main", "worktree": gone, "port": float64(3022),
	})

	count, err := reg.PruneStale()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 prune for missing dir, got %d", count)
	}
}

func TestBackup_CreatesCopy(t *testing.T) {
	reg := newTestRegistry(t)
	dir := filepath.Dir(reg.Path)
	clone := filepath.Join(dir, "wt")
	mkClone(t, clone)
	_ = reg.Allocate(Allocation{"project": "p", "branch": "main", "worktree": clone, "port": float64(3010)})

	path, err := reg.Backup("test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".bak-test") {
		t.Errorf("expected .bak-test suffix, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("backup file not created: %v", err)
	}
}
