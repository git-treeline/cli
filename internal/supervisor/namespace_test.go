package supervisor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedDevSocketNamespace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GTL_HOME", filepath.Join(home, ".treeline-dev", "gtl"))
	worktree := filepath.Join(home, ".treeline-dev", "workspaces", "fixture")
	digest := sha256.Sum256([]byte(canonicalWorktreePath(worktree)))
	for _, mode := range []struct{ managed, channel, namespace string }{
		{"app", "dev", "gtl-dev"}, {"app", "", "gtl"}, {"app", "stable", "gtl"},
		{"", "dev", "gtl"}, {"other", "dev", "gtl"}, {"", "", "gtl"},
	} {
		t.Run(mode.managed+"/"+mode.channel, func(t *testing.T) {
			t.Setenv("TREELINE_MANAGED", mode.managed)
			t.Setenv("TREELINE_CHANNEL", mode.channel)
			want := fmt.Sprintf("/tmp/%s-%d/gtl-%x.sock", mode.namespace, os.Geteuid(), digest[:8])
			if got := SocketPath(worktree); got != want {
				t.Fatalf("socket=%q want %q", got, want)
			}
			for _, path := range []string{PidPath(want), ChildPidPath(want)} {
				if filepath.Dir(path) != filepath.Dir(want) {
					t.Fatalf("PID sidecar escaped namespace: %q", path)
				}
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("path calculation created state: %v %v", entries, err)
	}
}

func TestManagedDevNeverFallsBackToLegacySockets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TREELINE_MANAGED", "app")
	t.Setenv("TREELINE_CHANNEL", "dev")
	path := filepath.Join(os.Getenv("HOME"), "fixture")
	if got := legacySocketPaths(path); len(got) != 0 {
		t.Fatalf("dev can inspect stable legacy endpoints: %v", got)
	}
	if present, err := LegacyPresent(path); present || err != nil {
		t.Fatalf("dev legacy detection: %v %v", present, err)
	}
	if err := ShutdownLegacy(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dev legacy shutdown: %v", err)
	}
	t.Setenv("TREELINE_CHANNEL", "")
	if got := legacySocketPaths(path); len(got) == 0 || got[0] != LegacySocketPath(path) {
		t.Fatalf("stable legacy behavior changed: %v", got)
	}
}
