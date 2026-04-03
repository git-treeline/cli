package allocator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/registry"
)

func testAllocator(t *testing.T, portsNeeded int, yamlExtra string) (*Allocator, *registry.Registry) {
	t.Helper()

	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3000,"increment":10},"redis":{"strategy":"prefixed","url":"redis://localhost:6379"}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	yml := "project: test\n"
	if portsNeeded > 0 {
		yml += "ports_needed: " + itoa(portsNeeded) + "\n"
	}
	yml += "database:\n  adapter: postgresql\n  template: test_dev\n  pattern: \"{template}_{worktree}\"\n"
	yml += yamlExtra
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte(yml), 0o644)
	pc := config.LoadProjectConfig(projDir)

	return New(uc, pc, reg), reg
}

func itoa(n int) string {
	return string(rune('0') + rune(n))
}

func TestAllocate_SinglePort(t *testing.T) {
	al, _ := testAllocator(t, 1, "")
	alloc, err := al.Allocate("/wt/branch-a", "branch-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port < 3010 {
		t.Errorf("expected port >= 3010, got %d", alloc.Port)
	}
	if len(alloc.Ports) != 1 {
		t.Errorf("expected 1 port, got %d", len(alloc.Ports))
	}
	if alloc.Reused {
		t.Error("expected Reused=false for new allocation")
	}
}

func TestAllocate_MultiPort(t *testing.T) {
	al, _ := testAllocator(t, 2, "")
	alloc, err := al.Allocate("/wt/branch-a", "branch-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(alloc.Ports) != 2 {
		t.Errorf("expected 2 ports, got %d", len(alloc.Ports))
	}
	if alloc.Ports[1] != alloc.Ports[0]+1 {
		t.Errorf("expected contiguous ports, got %v", alloc.Ports)
	}
}

func TestAllocate_Idempotent(t *testing.T) {
	al, reg := testAllocator(t, 1, "")

	first, err := al.Allocate("/wt/branch-a", "branch-a", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = reg.Allocate(first.ToRegistryEntry())

	second, err := al.Allocate("/wt/branch-a", "branch-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused {
		t.Error("expected Reused=true on second allocation")
	}
	if second.Port != first.Port {
		t.Errorf("expected same port %d, got %d", first.Port, second.Port)
	}
}

func TestAllocate_IdempotentPreservesAllFields(t *testing.T) {
	al, reg := testAllocator(t, 2, "")

	first, _ := al.Allocate("/wt/branch-a", "branch-a", false)
	_ = reg.Allocate(first.ToRegistryEntry())

	second, _ := al.Allocate("/wt/branch-a", "branch-a", false)
	if second.Database != first.Database {
		t.Errorf("expected database %s, got %s", first.Database, second.Database)
	}
	if len(second.Ports) != 2 {
		t.Errorf("expected 2 ports preserved, got %d", len(second.Ports))
	}
	if second.RedisPrefix != first.RedisPrefix {
		t.Errorf("expected redis prefix %s, got %s", first.RedisPrefix, second.RedisPrefix)
	}
}

func TestAllocate_SkipsUsedPorts(t *testing.T) {
	al, reg := testAllocator(t, 1, "")
	_ = reg.Allocate(registry.Allocation{
		"worktree": "/wt/existing",
		"port":     float64(3010),
		"ports":    []any{float64(3010)},
	})

	alloc, err := al.Allocate("/wt/new", "new", false)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port == 3010 {
		t.Error("expected to skip used port 3010")
	}
}

func TestAllocate_MultiPort_NonOverlapping(t *testing.T) {
	al, reg := testAllocator(t, 2, "")
	_ = reg.Allocate(registry.Allocation{
		"worktree": "/wt/existing",
		"port":     float64(3010),
		"ports":    []any{float64(3010), float64(3011)},
	})

	alloc, err := al.Allocate("/wt/new", "new", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range alloc.Ports {
		if p == 3010 || p == 3011 {
			t.Errorf("port %d overlaps with existing allocation", p)
		}
	}
}

func TestAllocate_PortsNeededExceedsIncrement(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3000,"increment":2}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\nports_needed: 5\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	_, err := al.Allocate("/wt/x", "x", false)
	if err == nil {
		t.Fatal("expected error when ports_needed > increment")
	}
}

func TestAllocate_DatabaseName(t *testing.T) {
	al, _ := testAllocator(t, 1, "")
	alloc, err := al.Allocate("/wt/feature-branch", "feature-branch", false)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Database != "test_dev_feature_branch" {
		t.Errorf("expected test_dev_feature_branch, got %s", alloc.Database)
	}
}

func TestAllocate_RedisPrefix(t *testing.T) {
	al, _ := testAllocator(t, 1, "")
	alloc, err := al.Allocate("/wt/my-branch", "my-branch", false)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.RedisPrefix != "test:my-branch" {
		t.Errorf("expected test:my-branch, got %s", alloc.RedisPrefix)
	}
}

func TestAllocate_RedisDatabaseStrategy(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3000,"increment":10},"redis":{"strategy":"database","url":"redis://localhost:6379"}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\ndatabase:\n  adapter: postgresql\n  template: test_dev\n  pattern: \"{template}_{worktree}\"\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/wt/a", "a", false)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.RedisDB != 1 {
		t.Errorf("expected redis db 1, got %d", alloc.RedisDB)
	}
}

func TestToRegistryEntry_Format(t *testing.T) {
	alloc := &Allocation{
		Project:      "salt",
		Worktree:     "/wt/branch",
		WorktreeName: "branch",
		Port:         3010,
		Ports:        []int{3010, 3011},
		Database:     "salt_dev_branch",
		RedisPrefix:  "salt:branch",
	}

	entry := alloc.ToRegistryEntry()
	raw, _ := json.Marshal(entry)
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)

	if parsed["project"] != "salt" {
		t.Errorf("expected salt, got %v", parsed["project"])
	}
	if ports, ok := parsed["ports"].([]any); ok {
		if len(ports) != 2 {
			t.Errorf("expected 2 ports, got %d", len(ports))
		}
	} else {
		t.Error("expected ports array")
	}
}

func TestAllocate_MainWorktree(t *testing.T) {
	al, _ := testAllocator(t, 2, "")
	alloc, err := al.Allocate("/repo/main", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port != 3000 {
		t.Errorf("expected base port 3000 for main, got %d", alloc.Port)
	}
	if len(alloc.Ports) != 2 {
		t.Errorf("expected 2 ports, got %d", len(alloc.Ports))
	}
	if alloc.Ports[0] != 3000 || alloc.Ports[1] != 3001 {
		t.Errorf("expected ports [3000,3001], got %v", alloc.Ports)
	}
	if alloc.Database != "test_dev" {
		t.Errorf("expected template database 'test_dev', got %s", alloc.Database)
	}
	if alloc.RedisDB != 0 {
		t.Errorf("expected redis db 0 for main, got %d", alloc.RedisDB)
	}
	if alloc.RedisPrefix != "" {
		t.Errorf("expected empty redis prefix for main, got %s", alloc.RedisPrefix)
	}
}

func TestIsPortFree(t *testing.T) {
	if !IsPortFree(49999) {
		t.Skip("port 49999 is in use, skipping")
	}
}

func TestCheckPortsListening_NothingRunning(t *testing.T) {
	if CheckPortsListening([]int{49998, 49999}) {
		t.Skip("unexpected listener on test ports")
	}
}
