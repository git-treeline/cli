package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
