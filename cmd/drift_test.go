package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
)

func TestDetectProjectDrift_NoDrift(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree": dir,
		"project":  "myapp",
		"port":     3002,
	})

	yamlName, regName, drifted := detectProjectDriftWith(dir, reg)
	if drifted {
		t.Errorf("expected no drift, got yaml=%q reg=%q", yamlName, regName)
	}
}

func TestDetectProjectDrift_Drifted(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new-name\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree": dir,
		"project":  "old-name",
		"port":     3002,
	})

	yamlName, regName, drifted := detectProjectDriftWith(dir, reg)
	if !drifted {
		t.Fatal("expected drift")
	}
	if yamlName != "new-name" {
		t.Errorf("yaml name: got %q, want %q", yamlName, "new-name")
	}
	if regName != "old-name" {
		t.Errorf("registry name: got %q, want %q", regName, "old-name")
	}
}

func TestDetectProjectDrift_NoAllocation(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)

	reg := newTestRegistry(t)

	_, _, drifted := detectProjectDriftWith(dir, reg)
	if drifted {
		t.Error("expected no drift when no allocation exists")
	}
}

func TestDetectProjectDrift_EmptyRegistryProject(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree": dir,
		"port":     3002,
	})

	_, _, drifted := detectProjectDriftWith(dir, reg)
	if drifted {
		t.Error("expected no drift when registry has no project field")
	}
}

func TestDoctorProjectDrift_NoDrift(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree": dir,
		"project":  "myapp",
		"port":     3002,
	})

	result := doctorProjectDriftJSONWith(dir, reg)
	if result != nil {
		t.Errorf("expected nil for no drift, got %v", result)
	}
}

func TestDoctorProjectDrift_Drifted(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: renamed\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree": dir,
		"project":  "original",
		"port":     3002,
	})

	result := doctorProjectDriftJSONWith(dir, reg)
	if result == nil {
		t.Fatal("expected drift result")
	}
	if result["status"] != "drift" {
		t.Errorf("status: got %q, want %q", result["status"], "drift")
	}
	if result["yaml_project"] != "renamed" {
		t.Errorf("yaml_project: got %q", result["yaml_project"])
	}
	if result["registry_name"] != "original" {
		t.Errorf("registry_name: got %q", result["registry_name"])
	}
}

func TestRevertProjectInYAML(t *testing.T) {
	dir := t.TempDir()
	ymlPath := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(ymlPath, []byte("project: wrong-name\nport_count: 2\n"), 0o644)

	if err := revertProjectInYAML(dir, "correct-name"); err != nil {
		t.Fatalf("revert failed: %v", err)
	}

	data, _ := os.ReadFile(ymlPath)
	content := string(data)
	if !strings.Contains(content, "project: correct-name") {
		t.Errorf("expected reverted project name, got:\n%s", content)
	}
	if !strings.Contains(content, "port_count: 2") {
		t.Errorf("expected other fields preserved, got:\n%s", content)
	}
}

func TestDoctorProjectDriftWith_NoDrift(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree": dir,
		"project":  "myapp",
		"port":     3002,
	})

	if doctorProjectDriftWith(dir, reg) {
		t.Error("expected no drift reported")
	}
}

func TestDoctorProjectDriftWith_Drifted(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree": dir,
		"project":  "old",
		"port":     3002,
	})

	if !doctorProjectDriftWith(dir, reg) {
		t.Error("expected drift reported")
	}
}

// --- resolver tests ---

type mockDB struct {
	existing map[string]bool
	dropErr  error
	existErr error
	renamed  []string
	dropped  []string
}

func newMockDB(names ...string) *mockDB {
	m := &mockDB{existing: map[string]bool{}}
	for _, n := range names {
		m.existing[n] = true
	}
	return m
}

func (m *mockDB) Clone(template, target string) error   { return nil }
func (m *mockDB) Restore(target, dumpFile string) error { return nil }
func (m *mockDB) Exists(name string) (bool, error)      { return m.existing[name], m.existErr }
func (m *mockDB) Drop(target string) error {
	if m.dropErr != nil {
		return m.dropErr
	}
	m.dropped = append(m.dropped, target)
	delete(m.existing, target)
	return nil
}
func (m *mockDB) Rename(old, new string) error {
	m.renamed = append(m.renamed, old+"->"+new)
	if m.existing[old] {
		delete(m.existing, old)
		m.existing[new] = true
	}
	return nil
}

func TestResolveRegistryDrift_InvalidSubmenuInput_NoDropOrRelease(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new_name\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree":         dir,
		"project":          "old_name",
		"port":             float64(3002),
		"database":         "old_name_development_feat",
		"database_adapter": "postgresql",
	})

	mock := newMockDB("old_name_development_feat")
	origAdapter := adapterFor
	origInput := driftReader
	defer func() { adapterFor = origAdapter; driftReader = origInput }()

	adapterFor = func(name string, args []string) (database.Adapter, error) { return mock, nil }
	driftReader = bufio.NewReader(strings.NewReader("xyz\n")) // invalid input for rename/drop submenu

	err := resolveRegistryDrift(dir, "new_name", "old_name", reg)
	if err == nil {
		t.Fatal("expected error on invalid input")
	}

	// Registry entry must still be present.
	if reg.Find(dir) == nil {
		t.Error("registry entry was released despite invalid input")
	}
	if len(mock.dropped) > 0 {
		t.Errorf("database was dropped despite invalid input: %v", mock.dropped)
	}
	if len(mock.renamed) > 0 {
		t.Errorf("database was renamed despite invalid input: %v", mock.renamed)
	}
}

func TestResolveRegistryDrift_AdapterError_RegistryPreserved(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new_name\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree":         dir,
		"project":          "old_name",
		"port":             float64(3002),
		"database":         "old_name_development_feat",
		"database_adapter": "bad_adapter",
	})

	origAdapter := adapterFor
	defer func() { adapterFor = origAdapter }()
	adapterFor = func(name string, args []string) (database.Adapter, error) {
		return nil, fmt.Errorf("unsupported adapter %s", name)
	}

	err := resolveRegistryDrift(dir, "new_name", "old_name", reg)
	if err == nil || !strings.Contains(err.Error(), "opening database adapter") {
		t.Fatalf("expected adapter error, got: %v", err)
	}
	if reg.Find(dir) == nil {
		t.Error("registry entry was released on adapter error")
	}
}

func TestResolveRegistryDrift_ExistsError_RegistryPreserved(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new_name\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree":         dir,
		"project":          "old_name",
		"port":             float64(3002),
		"database":         "old_name_development_feat",
		"database_adapter": "postgresql",
	})

	mock := newMockDB()
	mock.existErr = fmt.Errorf("connection refused")
	origAdapter := adapterFor
	defer func() { adapterFor = origAdapter }()
	adapterFor = func(name string, args []string) (database.Adapter, error) { return mock, nil }

	err := resolveRegistryDrift(dir, "new_name", "old_name", reg)
	if err == nil || !strings.Contains(err.Error(), "checking database") {
		t.Fatalf("expected 'checking database' error, got: %v", err)
	}
	if reg.Find(dir) == nil {
		t.Error("registry entry was released on Exists error")
	}
}

func TestResolveRegistryDrift_DropError_RegistryPreserved(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new_name\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{
		"worktree":         dir,
		"project":          "old_name",
		"port":             float64(3002),
		"database":         "old_name_development_feat",
		"database_adapter": "postgresql",
	})

	mock := newMockDB("old_name_development_feat")
	mock.dropErr = fmt.Errorf("permission denied")
	origAdapter := adapterFor
	origInput := driftReader
	defer func() { adapterFor = origAdapter; driftReader = origInput }()
	adapterFor = func(name string, args []string) (database.Adapter, error) { return mock, nil }
	// Choose: drop (2), then confirm proceed (y)
	driftReader = bufio.NewReader(strings.NewReader("2\ny\n"))

	err := resolveRegistryDrift(dir, "new_name", "old_name", reg)
	if err == nil || !strings.Contains(err.Error(), "dropping database") {
		t.Fatalf("expected 'dropping database' error, got: %v", err)
	}
	if reg.Find(dir) == nil {
		t.Error("registry entry was released despite drop error")
	}
}

func TestCheckDriftOrAbortWith_NoResolveOption_ForNonSetupCallers(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new_name\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{"worktree": dir, "project": "old_name", "port": float64(3002)})

	origInput := driftReader
	defer func() { driftReader = origInput }()
	// Enter "3" — should be treated as invalid (max=2 when resolveEnabled=false)
	driftReader = bufio.NewReader(strings.NewReader("3\n"))

	err := checkDriftOrAbortWith(dir, reg, false)
	if err == nil {
		t.Fatal("expected error")
	}
	// Registry must be untouched — "3" fell through to abort.
	if reg.Find(dir) == nil {
		t.Error("registry entry was modified when resolve is disabled")
	}
}

func TestCheckDriftOrAbortWith_ResolveOption_OnlyForSetup(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: new_name\n"), 0o644)

	reg := newTestRegistry(t)
	_ = reg.Allocate(registry.Allocation{"worktree": dir, "project": "old_name", "port": float64(3002)})

	origInput := driftReader
	defer func() { driftReader = origInput }()
	// "3" to select Resolve, no DB so just confirm "y"
	driftReader = bufio.NewReader(strings.NewReader("3\ny\n"))

	err := checkDriftOrAbortWith(dir, reg, true /* resolveEnabled */)
	if err != nil {
		t.Fatalf("expected nil after resolve, got: %v", err)
	}
	if reg.Find(dir) != nil {
		t.Error("registry entry should have been released after resolve")
	}
}
