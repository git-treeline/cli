package allocator

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
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
		yml += "port_count: " + itoa(portsNeeded) + "\n"
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
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\nport_count: 5\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	_, err := al.Allocate("/wt/x", "x", false)
	if err == nil {
		t.Fatal("expected error when port_count > increment")
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
		Branch:       "feature-auth",
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
	if parsed["branch"] != "feature-auth" {
		t.Errorf("expected feature-auth, got %v", parsed["branch"])
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
	if len(alloc.Ports) != 2 {
		t.Errorf("expected 2 ports, got %d", len(alloc.Ports))
	}
	if alloc.Ports[1] != alloc.Ports[0]+1 {
		t.Errorf("expected contiguous ports, got %v", alloc.Ports)
	}
	if alloc.Port != alloc.Ports[0] {
		t.Errorf("expected Port to match Ports[0], got %d vs %d", alloc.Port, alloc.Ports[0])
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

func TestAllocate_MainWorktreeSkipsOccupiedPorts(t *testing.T) {
	al, reg := testAllocator(t, 2, "")
	// Simulate another allocation holding the base ports
	other := &Allocation{
		Project: "other", Worktree: "/wt/other", WorktreeName: "other",
		Port: 3000, Ports: []int{3000, 3001},
	}
	_ = reg.Allocate(other.ToRegistryEntry())

	alloc, err := al.Allocate("/repo/main", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port == 3000 || alloc.Port == 3001 {
		t.Errorf("expected main to skip occupied base ports, got %d", alloc.Port)
	}
	if alloc.Ports[1] != alloc.Ports[0]+1 {
		t.Errorf("expected contiguous ports, got %v", alloc.Ports)
	}
	if alloc.Database != "test_dev" {
		t.Errorf("main should still get template database, got %s", alloc.Database)
	}
}

func TestReuseExisting_PortCountMismatch(t *testing.T) {
	al, reg := testAllocator(t, 2, "")

	alloc, err := al.Allocate("/tmp/wt-ports-test", "wt-test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(alloc.Ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(alloc.Ports))
	}
	_ = reg.Allocate(alloc.ToRegistryEntry())

	// Now create a new allocator with port_count: 1 (config changed)
	al2, _ := testAllocator(t, 1, "")
	// Point it at the same registry
	al2.Registry = reg

	alloc2, err := al2.Allocate("/tmp/wt-ports-test", "wt-test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(alloc2.Ports) != 1 {
		t.Errorf("expected 1 port after config change, got %d: %v", len(alloc2.Ports), alloc2.Ports)
	}
	if alloc2.Reused {
		t.Error("expected fresh allocation, not reuse")
	}
}

func TestAllocateMain_UsesReservation(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 10,
			"reservations": {"myapp": 4000}
		},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/repo/main", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port != 4000 {
		t.Errorf("expected reserved port 4000, got %d", alloc.Port)
	}
}

func TestAllocateMain_NoReservation_FallsBack(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 10,
			"reservations": {"other": 4000}
		},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/repo/main", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port == 4000 {
		t.Error("expected dynamic port, not another project's reservation")
	}
}

func TestAllocateNew_SkipsReservedPorts(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 10,
			"reservations": {"other": 3010}
		},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/repo/wt", "wt", false)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port == 3010 {
		t.Error("worktree should skip reserved port 3010")
	}
}

func TestReservation_WithMultiplePorts(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 10,
			"reservations": {"myapp": 5000}
		},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\nport_count: 2\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/repo/main", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port != 5000 {
		t.Errorf("expected reserved port 5000, got %d", alloc.Port)
	}
	if len(alloc.Ports) != 2 || alloc.Ports[1] != 5001 {
		t.Errorf("expected [5000, 5001], got %v", alloc.Ports)
	}
}

func TestBranchReservation_MatchesWorktree(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New(filepath.Join(dir, "registry.json"))

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 10,
			"reservations": {"myapp/staging": 6000}
		},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/wt/staging", "staging", false, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port != 6000 {
		t.Errorf("expected branch-reserved port 6000, got %d", alloc.Port)
	}
}

func TestBranchReservation_PriorityOverProject(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New(filepath.Join(dir, "registry.json"))

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 10,
			"reservations": {"myapp": 4000, "myapp/main": 4500}
		},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/repo/main", "main", true, "main")
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port != 4500 {
		t.Errorf("expected branch-specific 4500 to take priority, got %d", alloc.Port)
	}
}

func TestProjectReservation_DoesNotMatchWorktree(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New(filepath.Join(dir, "registry.json"))

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 10,
			"reservations": {"myapp": 4000}
		},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, err := al.Allocate("/wt/feature-x", "feature-x", false, "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port == 4000 {
		t.Error("project-only reservation should not match a worktree allocation")
	}
}

func TestReuseExisting_ReservationMismatch(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New(filepath.Join(dir, "registry.json"))

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {"base": 3000, "increment": 10},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)

	// First allocation: dynamic port
	alloc1, _ := al.Allocate("/repo/main", "main", true)
	_ = reg.Allocate(alloc1.ToRegistryEntry())
	if alloc1.Port == 4000 {
		t.Fatal("unexpected port 4000 before reservation")
	}

	// Add a reservation and create a new allocator
	_ = os.WriteFile(confPath, []byte(`{
		"port": {"base": 3000, "increment": 10, "reservations": {"myapp": 4000}},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc2 := config.LoadUserConfig(confPath)
	al2 := New(uc2, pc, reg)

	// Second allocation should NOT reuse — reservation mismatch
	alloc2, _ := al2.Allocate("/repo/main", "main", true, "main")
	if alloc2.Reused {
		t.Error("expected fresh allocation after adding reservation, got reuse")
	}
	if alloc2.Port != 4000 {
		t.Errorf("expected reserved port 4000, got %d", alloc2.Port)
	}
}

func TestReuseExisting_PortConflictWithOtherReservation(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New(filepath.Join(dir, "registry.json"))

	// Set up: worktree dynamically got port 3010
	_ = reg.Allocate(registry.Allocation{
		"project":  "other",
		"worktree": "/wt/branch-a",
		"port":     float64(3010),
		"ports":    []any{float64(3010)},
	})

	// Now a DIFFERENT project reserves 3010
	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {"base": 3000, "increment": 10, "reservations": {"reserved-app": 3010}},
		"redis": {"strategy": "prefixed", "url": "redis://localhost:6379"}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: other\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc, _ := al.Allocate("/wt/branch-a", "branch-a", false)
	if alloc.Reused {
		t.Error("expected fresh allocation — current port 3010 is reserved by another project")
	}
	if alloc.Port == 3010 {
		t.Error("new allocation should not use port 3010 (reserved by reserved-app)")
	}
}

func TestReservedPorts_CoversFullBlock(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{
		"port": {
			"base": 3000,
			"increment": 3,
			"reservations": {"a": 5000, "b": 6000}
		}
	}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	reserved := uc.ReservedPorts()
	for _, base := range []int{5000, 6000} {
		for i := range 3 {
			if !reserved[base+i] {
				t.Errorf("expected port %d to be reserved", base+i)
			}
		}
	}
	if reserved[5003] {
		t.Error("port 5003 should not be reserved")
	}
}

func TestToInterpolationMap_Baseline(t *testing.T) {
	alloc := &Allocation{
		Port:         3010,
		Ports:        []int{3010, 3011},
		Database:     "mydb_branch",
		WorktreeName: "branch",
	}
	m := alloc.ToInterpolationMap()
	if m["port"] != 3010 {
		t.Errorf("expected port=3010, got %v", m["port"])
	}
	if m["database"] != "mydb_branch" {
		t.Errorf("expected database=mydb_branch, got %v", m["database"])
	}
	if m["worktree_name"] != "branch" {
		t.Errorf("expected worktree_name=branch, got %v", m["worktree_name"])
	}
	if m["port_1"] != 3010 {
		t.Errorf("expected port_1=3010, got %v", m["port_1"])
	}
	if m["port_2"] != 3011 {
		t.Errorf("expected port_2=3011, got %v", m["port_2"])
	}
}

func TestToInterpolationMap_RedisDB(t *testing.T) {
	alloc := &Allocation{
		Port: 3010, Ports: []int{3010},
		RedisDB: 5,
	}
	m := alloc.ToInterpolationMap()
	if m["redis_db"] != 5 {
		t.Errorf("expected redis_db=5, got %v", m["redis_db"])
	}
	if _, ok := m["redis_prefix"]; ok {
		t.Error("expected no redis_prefix when RedisDB > 0")
	}
}

func TestToInterpolationMap_RedisPrefix(t *testing.T) {
	alloc := &Allocation{
		Port: 3010, Ports: []int{3010},
		RedisPrefix: "myapp:branch",
	}
	m := alloc.ToInterpolationMap()
	if m["redis_prefix"] != "myapp:branch" {
		t.Errorf("expected redis_prefix=myapp:branch, got %v", m["redis_prefix"])
	}
	if _, ok := m["redis_db"]; ok {
		t.Error("expected no redis_db when RedisDB == 0")
	}
}

func TestBuildRedisURL_WithDB(t *testing.T) {
	al, _ := testAllocator(t, 1, "")

	alloc := &Allocation{
		Port: 3010, Ports: []int{3010},
		RedisDB: 3,
	}
	url := al.BuildRedisURL(alloc)
	if url != "redis://localhost:6379/3" {
		t.Errorf("expected redis://localhost:6379/3, got %s", url)
	}
}

func TestBuildRedisURL_WithoutDB(t *testing.T) {
	al, _ := testAllocator(t, 1, "")

	alloc := &Allocation{
		Port: 3010, Ports: []int{3010},
		RedisPrefix: "myapp:x",
	}
	url := al.BuildRedisURL(alloc)
	if url != "redis://localhost:6379" {
		t.Errorf("expected redis://localhost:6379, got %s", url)
	}
}

func TestBuildRedisURL_TrailingSlash(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3000,"increment":10},"redis":{"url":"redis://localhost:6379/"}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)
	alloc := &Allocation{Port: 3010, Ports: []int{3010}, RedisDB: 2}
	url := al.BuildRedisURL(alloc)
	if url != "redis://localhost:6379/2" {
		t.Errorf("expected trailing slash trimmed, got %s", url)
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

func TestReuseExisting_PortConflict(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")

	regData := `{"version":1,"allocations":[{"project":"test","worktree":"/tmp/test-wt","worktree_name":"test-wt","port":49990,"ports":[49990],"database":"","database_adapter":"postgresql"}]}`
	_ = os.WriteFile(regPath, []byte(regData), 0o644)
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":49980,"increment":10}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)

	ln, err := net.Listen("tcp", "127.0.0.1:49990")
	if err != nil {
		t.Skip("cannot bind test port 49990")
	}
	defer func() { _ = ln.Close() }()

	alloc, err := al.Allocate("/tmp/test-wt", "test-wt", false)
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if alloc.Port == 49990 {
		t.Errorf("expected re-allocation away from occupied port 49990, got %d", alloc.Port)
	}
	if alloc.Reused {
		t.Error("expected Reused=false after port conflict")
	}
}

func TestBrowserBlockedPorts_NotAllocated(t *testing.T) {
	for _, port := range []int{6000, 6665, 6666, 6667} {
		if !browserBlockedPorts[port] {
			t.Errorf("expected port %d in blocked set", port)
		}
	}

	for _, port := range []int{4100, 9000} {
		if browserBlockedPorts[port] {
			t.Errorf("port %d should not be in blocked set", port)
		}
	}
}

func TestIsCommonDevPort(t *testing.T) {
	for _, port := range []int{3000, 4000, 5000, 5173, 8000, 8080} {
		if !IsCommonDevPort(port) {
			t.Errorf("expected %d to be a common dev port", port)
		}
	}
	for _, port := range []int{4100, 4200, 9000, 3010, 3020} {
		if IsCommonDevPort(port) && port != 4200 {
			t.Errorf("expected %d to NOT be a common dev port", port)
		}
	}
}

func TestAllocator_SkipsCommonDevPorts(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3000,"increment":10}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)

	alloc, err := al.Allocate("/repo/main", "main", true)
	if err != nil {
		t.Fatal(err)
	}

	if IsCommonDevPort(alloc.Port) {
		t.Errorf("main worktree allocated common dev port %d — should have skipped it", alloc.Port)
	}
}

func TestAllocator_SkipsCommonDevPorts_Worktree(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":4990,"increment":10}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)

	alloc, err := al.Allocate("/repo/wt/feat", "feat", false, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if IsCommonDevPort(alloc.Port) {
		t.Errorf("worktree allocated common dev port %d — should have skipped it", alloc.Port)
	}
}

func TestAllocator_SafeBaseDoesNotSkip(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":4100,"increment":10}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)

	alloc, err := al.Allocate("/repo/main", "main", true)
	if err != nil {
		t.Fatal(err)
	}

	if alloc.Port != 4100 {
		t.Errorf("expected port 4100 with safe base, got %d", alloc.Port)
	}
}

func TestAllocator_FailsWhenBaseEqualsRouterPort(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3001},"router":{"port":3001}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)

	_, err := al.Allocate("/repo/main", "main", true)
	if err == nil {
		t.Fatal("expected error when port.base == router.port")
	}
	if !strings.Contains(err.Error(), "conflicts with router.port") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

func TestAllocator_DefaultRouterPortConflict(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3001}}`), 0o644)
	uc := config.LoadUserConfig(confPath)

	projDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projDir, 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := config.LoadProjectConfig(projDir)

	al := New(uc, pc, reg)

	_, err := al.Allocate("/repo/main", "main", true)
	if err == nil {
		t.Fatal("expected error when port.base equals default router port (3001)")
	}
}
