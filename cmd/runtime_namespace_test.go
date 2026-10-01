package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/supervisor"
)

func TestManagedDevRuntimeOutcomeNamespace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GTL_HOME", filepath.Join(home, ".treeline-dev", "gtl"))
	worktree := filepath.Join(home, ".treeline-dev", "workspaces", "fixture")
	t.Setenv("TREELINE_MANAGED", "app")
	t.Setenv("TREELINE_CHANNEL", "")
	stableSocket := supervisor.SocketPath(worktree)
	stable := runtimeOutcomeStore(stableSocket).Path
	if stable != fmt.Sprintf("/tmp/gtl-%d/runtime-outcomes.json", os.Geteuid()) {
		t.Fatalf("stable outcome path changed: %s", stable)
	}
	t.Setenv("TREELINE_CHANNEL", "dev")
	devSocket := supervisor.SocketPath(worktree)
	dev := runtimeOutcomeStore(devSocket).Path
	if dev != fmt.Sprintf("/tmp/gtl-dev-%d/runtime-outcomes.json", os.Geteuid()) || dev == stable || devSocket == stableSocket {
		t.Fatalf("dev runtime state not separated: socket=%s outcomes=%s", devSocket, dev)
	}
	if filepath.Dir(detachedSupervisorLogPath(devSocket)) != os.Getenv("GTL_HOME") {
		t.Fatal("dev log left private GTL_HOME")
	}
	t.Setenv("TREELINE_MANAGED", "")
	if normal := runtimeOutcomeStore(supervisor.SocketPath(worktree)).Path; normal != stable {
		t.Fatalf("standalone GTL_HOME changed namespace: %s", normal)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("path calculation created state: %v %v", entries, err)
	}
}
