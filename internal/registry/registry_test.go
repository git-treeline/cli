package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/platform"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	return New(filepath.Join(dir, "registry.json"))
}

func TestRegistry_AllocateAndFind(t *testing.T) {
	reg := newTestRegistry(t)

	entry := Allocation{
		"project":       "salt",
		"worktree":      "/tmp/wt/branch-a",
		"worktree_name": "branch-a",
		"port":          float64(3010),
		"ports":         []any{float64(3010), float64(3011)},
		"database":      "salt_development_branch_a",
	}

	if err := reg.Allocate(entry); err != nil {
		t.Fatal(err)
	}

	found := reg.Find("/tmp/wt/branch-a")
	if found == nil {
		t.Fatal("expected allocation, got nil")
	}
	if found["worktree_name"] != "branch-a" {
		t.Errorf("expected branch-a, got %v", found["worktree_name"])
	}
}

func TestRegistry_UsedPorts_MultiPort(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{
		"project":  "a",
		"worktree": "/wt/a",
		"ports":    []any{float64(3010), float64(3011)},
	})
	_ = reg.Allocate(Allocation{
		"project":  "b",
		"worktree": "/wt/b",
		"port":     float64(3020),
	})

	ports := reg.UsedPorts()
	if len(ports) != 3 {
		t.Errorf("expected 3 ports, got %d: %v", len(ports), ports)
	}
}

func TestRegistry_UsedPorts_BackwardCompat(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{
		"project":  "old",
		"worktree": "/wt/old",
		"port":     float64(3010),
	})

	ports := reg.UsedPorts()
	if len(ports) != 1 || ports[0] != 3010 {
		t.Errorf("expected [3010], got %v", ports)
	}
}

func TestRegistry_Release(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/x"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/y"})

	removed, _ := reg.Release("/wt/x")
	if !removed {
		t.Error("expected removal")
	}
	if found := reg.Find("/wt/x"); found != nil {
		t.Error("expected nil after release")
	}
	if found := reg.Find("/wt/y"); found == nil {
		t.Error("expected /wt/y to still exist")
	}
}

func TestRegistry_Prune(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "worktree-a")
	_ = os.MkdirAll(existing, 0o755)

	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": existing})
	_ = reg.Allocate(Allocation{"worktree": "/does/not/exist"})

	count, _ := reg.Prune()
	if count != 1 {
		t.Errorf("expected 1 pruned, got %d", count)
	}

	if len(reg.Allocations()) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(reg.Allocations()))
	}
}

func TestRegistry_AllocateReplaces(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "port": float64(3010)})
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "port": float64(3020)})

	allocs := reg.Allocations()
	if len(allocs) != 1 {
		t.Errorf("expected 1 allocation after re-allocate, got %d", len(allocs))
	}
	if allocs[0]["port"] != float64(3020) {
		t.Errorf("expected updated port 3020, got %v", allocs[0]["port"])
	}
}

func TestRegistry_PruneStale_RemovesMissingDirs(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/does/not/exist/at/all"})

	count, err := reg.PruneStale()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 pruned, got %d", count)
	}
}

func TestRegistry_FindMergedAllocations(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "worktree_name": "dir-a", "project": "p1"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/b", "worktree_name": "dir-b", "project": "p1"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/c", "worktree_name": "dir-c", "project": "p1"})

	wtBranches := map[string]string{
		"/wt/a": "feature-a",
		"/wt/b": "feature-b",
		"/wt/c": "feature-c",
	}

	merged := reg.FindMergedAllocations([]string{"feature-a", "feature-c"}, wtBranches)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged allocations, got %d", len(merged))
	}

	paths := map[string]bool{}
	for _, a := range merged {
		paths[GetString(a, "worktree")] = true
	}
	if !paths["/wt/a"] || !paths["/wt/c"] {
		t.Errorf("expected /wt/a and /wt/c, got %v", paths)
	}
}

func TestRegistry_FindMergedAllocations_NoneMatch(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "worktree_name": "dir-a"})

	wtBranches := map[string]string{"/wt/a": "feature-a"}
	merged := reg.FindMergedAllocations([]string{"other-branch"}, wtBranches)
	if len(merged) != 0 {
		t.Errorf("expected 0 matches, got %d", len(merged))
	}
}

func TestRegistry_ReleaseMany(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "worktree_name": "a"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/b", "worktree_name": "b"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/c", "worktree_name": "c"})

	count, err := reg.ReleaseMany([]string{"/wt/a", "/wt/c"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 released, got %d", count)
	}

	allocs := reg.Allocations()
	if len(allocs) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(allocs))
	}
	if GetString(allocs[0], "worktree_name") != "b" {
		t.Errorf("expected 'b' remaining, got %v", allocs[0]["worktree_name"])
	}
}

func TestRegistry_UpdateField(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "branch": "old-branch"})

	if err := reg.UpdateField("/wt/a", "branch", "new-branch"); err != nil {
		t.Fatal(err)
	}

	found := reg.Find("/wt/a")
	if found == nil {
		t.Fatal("expected allocation")
	}
	if GetString(found, "branch") != "new-branch" {
		t.Errorf("expected new-branch, got %v", found["branch"])
	}
}

func TestRegistry_UpdateField_NoMatch(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "branch": "main"})

	if err := reg.UpdateField("/wt/nonexistent", "branch", "other"); err != nil {
		t.Fatal(err)
	}

	found := reg.Find("/wt/a")
	if GetString(found, "branch") != "main" {
		t.Errorf("expected main unchanged, got %v", found["branch"])
	}
}

func TestRegistry_FindByProject_Match(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "project": "myapp"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/b", "project": "other"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/c", "project": "myapp"})

	found := reg.FindByProject("myapp")
	if len(found) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(found))
	}
}

func TestRegistry_FindByProject_NoMatch(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "project": "other"})

	found := reg.FindByProject("myapp")
	if len(found) != 0 {
		t.Errorf("expected 0 matches, got %d", len(found))
	}
}

func TestRegistry_FindByProject_Empty(t *testing.T) {
	reg := newTestRegistry(t)
	found := reg.FindByProject("anything")
	if len(found) != 0 {
		t.Errorf("expected 0 matches for empty registry, got %d", len(found))
	}
}

func TestFindProjectBranch(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := New(regPath)

	_ = reg.Allocate(Allocation{
		"project": "api", "worktree": "/tmp/api-main", "branch": "main", "port": float64(3000), "ports": []any{float64(3000)},
	})
	_ = reg.Allocate(Allocation{
		"project": "api", "worktree": "/tmp/api-feat", "branch": "feature-x", "port": float64(3010), "ports": []any{float64(3010)},
	})

	found := reg.FindProjectBranch("api", "feature-x")
	if found == nil {
		t.Fatal("expected to find api/feature-x")
	}
	if GetString(found, "worktree") != "/tmp/api-feat" {
		t.Errorf("wrong worktree: %s", GetString(found, "worktree"))
	}

	notFound := reg.FindProjectBranch("api", "no-such-branch")
	if notFound != nil {
		t.Error("expected nil for non-existent branch")
	}
}

func TestLinks(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := New(regPath)

	wt := filepath.Join(dir, "my-wt")
	_ = os.MkdirAll(wt, 0o755)
	_ = reg.Allocate(Allocation{
		"project": "frontend", "worktree": wt, "branch": "main", "port": float64(3000), "ports": []any{float64(3000)},
	})

	if err := reg.SetLink(wt, "api", "feature-payments"); err != nil {
		t.Fatalf("SetLink: %v", err)
	}

	links := reg.GetLinks(wt)
	if links["api"] != "feature-payments" {
		t.Errorf("expected api -> feature-payments, got %v", links)
	}

	if err := reg.SetLink(wt, "api", "develop"); err != nil {
		t.Fatalf("SetLink override: %v", err)
	}
	links = reg.GetLinks(wt)
	if links["api"] != "develop" {
		t.Errorf("expected api -> develop, got %v", links)
	}

	if err := reg.RemoveLink(wt, "api"); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}
	links = reg.GetLinks(wt)
	if len(links) != 0 {
		t.Errorf("expected empty links, got %v", links)
	}
}

func TestRegistry_UsedRedisDbs(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "redis_db": float64(1)})
	_ = reg.Allocate(Allocation{"worktree": "/wt/b", "redis_db": float64(3)})
	_ = reg.Allocate(Allocation{"worktree": "/wt/c"})

	dbs := reg.UsedRedisDbs()
	if len(dbs) != 2 {
		t.Fatalf("expected 2 redis dbs, got %d: %v", len(dbs), dbs)
	}
	found := map[int]bool{}
	for _, d := range dbs {
		found[d] = true
	}
	if !found[1] || !found[3] {
		t.Errorf("expected dbs 1 and 3, got %v", dbs)
	}
}

func TestRegistry_UsedRedisDbs_Empty(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a"})

	dbs := reg.UsedRedisDbs()
	if len(dbs) != 0 {
		t.Errorf("expected 0 redis dbs, got %v", dbs)
	}
}

func TestRegistry_UsedRedisDbs_SkipsNonFloat(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a", "redis_db": "not-a-number"})
	_ = reg.Allocate(Allocation{"worktree": "/wt/b", "redis_db": float64(2)})

	dbs := reg.UsedRedisDbs()
	if len(dbs) != 1 || dbs[0] != 2 {
		t.Errorf("expected [2], got %v", dbs)
	}
}

func TestRegistry_Prune_AllStale(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/no/such/dir/a", "project": "p"})
	_ = reg.Allocate(Allocation{"worktree": "/no/such/dir/b", "project": "p"})
	_ = reg.Allocate(Allocation{"worktree": "/no/such/dir/c", "project": "p"})

	count, err := reg.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 pruned, got %d", count)
	}
	if len(reg.Allocations()) != 0 {
		t.Errorf("expected 0 remaining, got %d", len(reg.Allocations()))
	}
}

func TestRegistry_Prune_NoneStale(t *testing.T) {
	dir := t.TempDir()
	wt1 := filepath.Join(dir, "a")
	wt2 := filepath.Join(dir, "b")
	_ = os.MkdirAll(wt1, 0o755)
	_ = os.MkdirAll(wt2, 0o755)

	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": wt1})
	_ = reg.Allocate(Allocation{"worktree": wt2})

	count, err := reg.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 pruned, got %d", count)
	}
	if len(reg.Allocations()) != 2 {
		t.Errorf("expected 2 remaining, got %d", len(reg.Allocations()))
	}
}

func TestRegistry_Prune_MixedWithMetadata(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "live-wt")
	_ = os.MkdirAll(existing, 0o755)

	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": existing, "project": "myapp", "port": float64(3000), "database": "myapp_dev"})
	_ = reg.Allocate(Allocation{"worktree": "/gone/stale", "project": "myapp", "port": float64(3010), "database": "myapp_dev_stale"})

	count, _ := reg.Prune()
	if count != 1 {
		t.Errorf("expected 1 pruned, got %d", count)
	}

	remaining := reg.Allocations()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if GetString(remaining[0], "database") != "myapp_dev" {
		t.Errorf("wrong allocation survived: %v", remaining[0])
	}
}

func TestRegistry_CorruptJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(regPath, []byte(`{corrupt`), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := New(regPath)

	err := reg.Allocate(Allocation{"worktree": "/wt/a"})
	if err == nil {
		t.Fatal("expected error for corrupt registry, got nil")
	}

	raw, _ := os.ReadFile(regPath)
	if string(raw) != `{corrupt` {
		t.Errorf("corrupt file was overwritten: %s", raw)
	}
}

func TestRegistry_ReleaseMany_Empty(t *testing.T) {
	reg := newTestRegistry(t)
	_ = reg.Allocate(Allocation{"worktree": "/wt/a"})

	count, err := reg.ReleaseMany([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 released, got %d", count)
	}
	if len(reg.Allocations()) != 1 {
		t.Error("expected allocation to remain")
	}
}

func TestRegistry_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "sub", "registry.json")
	reg := New(regPath)

	entry := Allocation{
		"project":  "test",
		"worktree": "/tmp/wt/perms",
		"port":     float64(3000),
	}
	if err := reg.Allocate(entry); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	dirInfo, err := os.Stat(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != platform.DirMode {
		t.Errorf("data dir mode = %o, want %o", got, platform.DirMode)
	}

	fileInfo, err := os.Stat(regPath)
	if err != nil {
		t.Fatalf("stat registry: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != platform.PrivateFileMode {
		t.Errorf("registry file mode = %o, want %o", got, platform.PrivateFileMode)
	}

	lockInfo, err := os.Stat(regPath + ".lock")
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if got := lockInfo.Mode().Perm(); got != platform.PrivateFileMode {
		t.Errorf("lock file mode = %o, want %o", got, platform.PrivateFileMode)
	}
}
