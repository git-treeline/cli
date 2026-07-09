package cmd

import (
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func TestNukePlan_Empty(t *testing.T) {
	if !(nukePlan{}).empty() {
		t.Error("zero-value plan should be empty")
	}
	if (nukePlan{ports: []portTarget{{port: 3000, pid: 1}}}).empty() {
		t.Error("plan with a port target is not empty")
	}
	if (nukePlan{sockets: []string{"/tmp/x.sock"}}).empty() {
		t.Error("plan with a socket is not empty")
	}
}

func TestBuildNukePlan_EmptyRegistry(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	plan := buildNukePlan(reg)
	if !plan.empty() {
		t.Errorf("expected empty plan for empty registry, got %+v", plan)
	}
}
