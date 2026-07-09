package cmd

import (
	"os"
	"path/filepath"
	"strings"
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

// buildNukePlanWith should target only ports that have a live holder that is
// neither dead (pid<=0) nor this very process, and should de-dupe repeated
// ports across the registry.
func TestBuildNukePlanWith_SelectsLiveForeignHolders(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/a",
		"project":  "a",
		"ports":    []any{float64(3000), float64(3001), float64(3002)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/b",
		"project":  "b",
		"port":     float64(3000), // duplicate port, must not double-count
	}); err != nil {
		t.Fatal(err)
	}

	holder := func(port int) (int, string) {
		switch port {
		case 3000:
			return 4111, "node" // live foreign process — target it
		case 3001:
			return 0, "" // nothing holding it — skip
		case 3002:
			return os.Getpid(), "gtl" // it's us — skip
		}
		return 0, ""
	}

	plan := buildNukePlanWith(reg, holder)
	if len(plan.ports) != 1 {
		t.Fatalf("expected exactly one targeted port, got %+v", plan.ports)
	}
	tgt := plan.ports[0]
	if tgt.port != 3000 || tgt.pid != 4111 || tgt.name != "node" {
		t.Errorf("unexpected target: %+v", tgt)
	}
}

// executeNukePlanWith kills only ports still held by a foreign process at
// execution time; a port freed between planning and execution (re-check returns
// pid 0) must not be killed, and our own pid is never killed.
func TestExecuteNukePlanWith_KillsRightSet(t *testing.T) {
	plan := nukePlan{ports: []portTarget{
		{port: 3000, pid: 4111, name: "node"},
		{port: 3001, pid: 4222, name: "ruby"}, // will be reported freed on re-check
		{port: 3002, pid: 4333, name: "self"}, // re-check returns our pid
	}}

	holder := func(port int) (int, string) {
		switch port {
		case 3000:
			return 4111, "node"
		case 3001:
			return 0, "" // freed by a graceful shutdown — skip
		case 3002:
			return os.Getpid(), "gtl"
		}
		return 0, ""
	}

	var killedPids []int
	kill := func(pid int) bool {
		killedPids = append(killedPids, pid)
		return true
	}

	killed, cleared := executeNukePlanWith(plan, holder, kill)
	if killed != 1 {
		t.Errorf("expected killed=1, got %d", killed)
	}
	if cleared != 0 {
		t.Errorf("expected cleared=0 (no sockets), got %d", cleared)
	}
	if len(killedPids) != 1 || killedPids[0] != 4111 {
		t.Errorf("expected only pid 4111 killed, got %v", killedPids)
	}
}

// Without --force, a declined confirmation must run nothing destructive.
func TestRunNuke_DeclinedConfirmationDoesNothing(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/a",
		"project":  "a",
		"ports":    []any{float64(3000)},
	}); err != nil {
		t.Fatal(err)
	}
	holder := func(int) (int, string) { return 4111, "node" }
	killed := false
	kill := func(int) bool { killed = true; return true }

	k, c, ran := runNuke(reg, false, strings.NewReader("n\n"), holder, kill)
	if ran {
		t.Error("expected ran=false when confirmation declined")
	}
	if killed {
		t.Error("kill seam must not be invoked when confirmation declined")
	}
	if k != 0 || c != 0 {
		t.Errorf("expected no work done, got killed=%d cleared=%d", k, c)
	}
}

// --force skips the prompt and executes.
func TestRunNuke_ForceExecutes(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/a",
		"project":  "a",
		"ports":    []any{float64(3000)},
	}); err != nil {
		t.Fatal(err)
	}
	holder := func(int) (int, string) { return 4111, "node" }
	var killedPids []int
	kill := func(pid int) bool { killedPids = append(killedPids, pid); return true }

	_, _, ran := runNuke(reg, true, nil, holder, kill)
	if !ran {
		t.Fatal("expected ran=true with --force")
	}
	if len(killedPids) != 1 || killedPids[0] != 4111 {
		t.Errorf("expected pid 4111 killed, got %v", killedPids)
	}
}

// An empty plan short-circuits: nothing runs, ran=false.
func TestRunNuke_EmptyPlan(t *testing.T) {
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	holder := func(int) (int, string) { return 0, "" }
	kill := func(int) bool { t.Fatal("kill must not be called for empty plan"); return false }
	if _, _, ran := runNuke(reg, true, nil, holder, kill); ran {
		t.Error("expected ran=false for empty registry")
	}
}
