package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func releaseSafetyEnvironment(t *testing.T) {
	t.Helper()
	previousAcquire := acquireCommandLifecycleLocks
	acquireCommandLifecycleLocks = func(context.Context, []string) (*registry.LifecycleLocks, error) {
		return &registry.LifecycleLocks{}, nil
	}
	t.Cleanup(func() { acquireCommandLifecycleLocks = previousAcquire })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GTL_HOME", t.TempDir())
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	oldRemove, oldForce, oldDrop, oldDry := releaseRemoveWorktree, releaseForce, releaseDropDB, releaseDryRun
	releaseRemoveWorktree, releaseForce, releaseDropDB, releaseDryRun = true, true, false, false
	t.Cleanup(func() {
		releaseRemoveWorktree, releaseForce, releaseDropDB, releaseDryRun = oldRemove, oldForce, oldDrop, oldDry
	})
}

func releaseSafetyGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Git fixture %v: %s: %v", args, out, err)
	}
}

func releaseSafetyFixture(t *testing.T) (string, string) {
	t.Helper()
	releaseSafetyEnvironment(t)
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	releaseSafetyGit(t, repo, "init", "-qb", "main")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseSafetyGit(t, repo, "add", "tracked")
	releaseSafetyGit(t, repo, "commit", "-qm", "fixture")
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "linked")
	releaseSafetyGit(t, repo, "worktree", "add", "-qb", "linked", linked)
	return repo, linked
}

func releaseSafetyHook(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("hooks:\n  pre_release:\n    - touch hook-ran\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func releaseSafetyUnchanged(t *testing.T, reg *registry.Registry, before []byte, roots ...string) {
	t.Helper()
	after, err := os.ReadFile(reg.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("registry changed on refusal: %v", err)
	}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("refused directory changed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "hook-ran")); !os.IsNotExist(err) {
			t.Fatalf("pre-release hook ran: %v", err)
		}
	}
}

func TestReleaseSafetyCorruptRegistryNeverFallsBackToRemoval(t *testing.T) {
	for _, source := range []string{`{corrupt registry`, `{}`, `null`, `{"version":99,"allocations":[]}`} {
		t.Run(source, func(t *testing.T) {
			_, linked := releaseSafetyFixture(t)
			reg := registry.New("")
			before := []byte(source)
			if err := os.WriteFile(reg.Path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			err := runReleaseSingle([]string{linked})
			if err == nil || !strings.Contains(err.Error(), "cannot read release allocation") {
				t.Fatalf("strict registry failure lost: %v", err)
			}
			for _, all := range []bool{false, true} {
				if err := runReleaseBatch("fixture", all); err == nil {
					t.Fatal("batch release accepted invalid registry")
				}
			}
			releaseSafetyUnchanged(t, reg, before, linked)
		})
	}
}

func TestReleaseSafetyMissingRegistryAllowsUnallocatedPreflight(t *testing.T) {
	_, linked := releaseSafetyFixture(t)
	releaseDryRun = true
	if err := runReleaseSingle([]string{linked}); err != nil {
		t.Fatalf("unallocated linked preflight refused: %v", err)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatalf("dry-run changed worktree: %v", err)
	}
	if _, err := os.Stat(registry.New("").Path); !os.IsNotExist(err) {
		t.Fatalf("preflight wrote registry: %v", err)
	}
}

func TestReleaseSafetyAllocatedRefusalsPrecedeEffects(t *testing.T) {
	for _, kind := range []string{"primary", "primary alias", "subdirectory", "ordinary directory"} {
		t.Run(kind, func(t *testing.T) {
			repo, linked := releaseSafetyFixture(t)
			target := repo
			switch kind {
			case "primary alias":
				target = filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(repo, target); err != nil {
					t.Fatal(err)
				}
			case "subdirectory":
				target = filepath.Join(linked, "child")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "ordinary directory":
				target = t.TempDir()
			}
			releaseSafetyHook(t, target)
			reg := registry.New("")
			if err := reg.Allocate(registry.Allocation{"worktree": target, "project": "r13-disposable-fixture", "port": 49152}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(reg.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := runReleaseSingle([]string{target}); err == nil {
				t.Fatal("invalid allocated target was accepted")
			}
			releaseSafetyUnchanged(t, reg, before, target)
		})
	}
}

func TestReleaseSafetyBatchPreflightsEveryTargetBeforeHooks(t *testing.T) {
	repo, linked := releaseSafetyFixture(t)
	reg := registry.New("")
	allocs := []registry.Allocation{{"worktree": linked}, {"worktree": repo}}
	for _, alloc := range allocs {
		releaseSafetyHook(t, registry.GetString(alloc, "worktree"))
		if err := reg.Allocate(alloc); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(reg.Path)
	if err != nil {
		t.Fatal(err)
	}
	count, err := releaseAndTeardown(reg, allocs, false, true, true)
	if err == nil || count != 0 || !strings.Contains(err.Error(), "main worktree") {
		t.Fatalf("batch refusal: count %d, error %v", count, err)
	}
	releaseSafetyUnchanged(t, reg, before, repo, linked)
}

func TestReleaseSafetyBatchRefusesStaleSnapshotsBeforeEffects(t *testing.T) {
	_, linked := releaseSafetyFixture(t)
	reg := registry.New("")
	releaseSafetyHook(t, linked)
	if err := reg.Allocate(registry.Allocation{"worktree": linked, "port": 49152}); err != nil {
		t.Fatal(err)
	}
	stale, err := reg.ReleasePreflight()
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.UpdateField(linked, "project", "changed-project"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(reg.Path)
	if err != nil {
		t.Fatal(err)
	}
	count, err := releaseAndTeardown(reg, stale, false, true, true)
	if err == nil || count != 0 || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale batch input accepted: count %d, error %v", count, err)
	}
	releaseSafetyUnchanged(t, reg, before, linked)
}

func TestReleaseSafetyRemovalErrorsReachCaller(t *testing.T) {
	for _, state := range []string{"dirty", "untracked", "locked"} {
		t.Run(state, func(t *testing.T) {
			repo, linked := releaseSafetyFixture(t)
			file := "tracked"
			if state == "untracked" {
				file = "untracked"
			}
			if state == "locked" {
				releaseSafetyGit(t, repo, "worktree", "lock", linked)
			} else if err := os.WriteFile(filepath.Join(linked, file), []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := removeWorktreeDir(linked, false)
			if err == nil || !strings.Contains(err.Error(), "failed to remove worktree") {
				t.Fatalf("removal failure swallowed: %v", err)
			}
			if _, err := os.Stat(linked); err != nil {
				t.Fatalf("refused tree disappeared: %v", err)
			}
		})
	}
}

func TestReleaseSafetyInsideWorktreeDeclineIsReported(t *testing.T) {
	_, linked := releaseSafetyFixture(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(linked); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.WriteString("n\n")
	_ = writer.Close()
	previous := os.Stdin
	os.Stdin = input
	t.Cleanup(func() { os.Stdin = previous; _ = input.Close() })
	if err := removeWorktreeDir(linked, false); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("declined removal reported success: %v", err)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatalf("declined tree disappeared: %v", err)
	}
}
