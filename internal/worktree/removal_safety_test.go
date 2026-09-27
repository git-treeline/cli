package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func removalEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
}

func removalFixture(t *testing.T) (string, string) {
	t.Helper()
	removalEnvironment(t)
	repo := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "tracked")
	run(t, repo, "git", "commit", "-qm", "tracked fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	run(t, repo, "git", "worktree", "add", "-qb", "linked", linked)
	linked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}
	return repo, linked
}

func TestRemovalSafetyRejectsPrimaryAndNonRoots(t *testing.T) {
	repo, linked := removalFixture(t)
	alias := filepath.Join(t.TempDir(), "primary-alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(linked, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"primary": repo, "primary alias": alias, "subdirectory": subdir,
		"ordinary directory": t.TempDir(), "missing": filepath.Join(t.TempDir(), "missing"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateRemoval(target); err == nil {
				t.Fatal("validation accepted a non-removable target")
			}
			if err := Remove(target, true); err == nil {
				t.Fatal("force removed a non-removable target")
			}
		})
	}
	if got, err := os.ReadFile(filepath.Join(repo, "tracked")); err != nil || string(got) != "original" {
		t.Fatalf("primary bytes changed: %q, %v", got, err)
	}
}

func TestRemovalSafetyPreservesGitRefusals(t *testing.T) {
	for _, state := range []string{"dirty", "untracked", "locked", "locked force"} {
		t.Run(state, func(t *testing.T) {
			repo, linked := removalFixture(t)
			file := "tracked"
			if state == "untracked" {
				file = "untracked"
			}
			if strings.HasPrefix(state, "locked") {
				run(t, repo, "git", "worktree", "lock", linked)
			} else if err := os.WriteFile(filepath.Join(linked, file), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateRemoval(linked); err != nil {
				t.Fatalf("root validation: %v", err)
			}
			err := Remove(linked, state == "locked force")
			if err == nil || !strings.Contains(err.Error(), "git worktree failed:") {
				t.Fatalf("Git refusal lost: %v", err)
			}
			if strings.HasPrefix(state, "locked") && !strings.Contains(err.Error(), "locked") {
				t.Fatalf("locked reason lost: %v", err)
			}
			if _, err := os.Stat(linked); err != nil {
				t.Fatalf("refused worktree disappeared: %v", err)
			}
		})
	}
}

func TestRemovalSafetyIgnoresPoisonedGitEnvironment(t *testing.T) {
	repo, linked := removalFixture(t)
	other := initTestRepo(t)
	index := filepath.Join(t.TempDir(), "index")
	if err := os.WriteFile(index, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"GIT_DIR": filepath.Join(other, ".git"), "GIT_COMMON_DIR": filepath.Join(other, ".git"),
		"GIT_WORK_TREE": other, "GIT_INDEX_FILE": index, "GIT_EXEC_PATH": "/missing-fixture",
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.worktree", "GIT_CONFIG_VALUE_0": other,
		"GIT_CONFIG_PARAMETERS": "invalid injected config", "GIT_CONFIG_GLOBAL": "/missing-fixture",
	} {
		t.Setenv(key, value)
	}
	if _, err := ValidateRemoval(repo); err == nil {
		t.Fatal("poisoned environment disguised the primary")
	}
	if primary, err := ValidateRemoval(linked); err != nil || primary != repo {
		t.Fatalf("validation redirected: %q, %v", primary, err)
	}
	if err := Remove(linked, false); err != nil {
		t.Fatalf("disposable linked removal: %v", err)
	}
	if _, err := os.Stat(linked); !os.IsNotExist(err) {
		t.Fatalf("linked worktree remains: %v", err)
	}
	if got, err := os.ReadFile(index); err != nil || string(got) != "untouched" {
		t.Fatalf("injected index changed: %q, %v", got, err)
	}
	for _, root := range []string{repo, other} {
		if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
			t.Fatalf("unrelated/primary repository changed: %v", err)
		}
	}
}

func TestRemovalSafetyNormalAndForcedLinkedRemoval(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "forced dirty"}[force], func(t *testing.T) {
			repo, linked := removalFixture(t)
			alias := filepath.Join(t.TempDir(), "linked-alias")
			if err := os.Symlink(linked, alias); err != nil {
				t.Fatal(err)
			}
			if force {
				if err := os.WriteFile(filepath.Join(linked, "tracked"), []byte("disposable"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := Remove(alias, force); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(linked); !os.IsNotExist(err) {
				t.Fatalf("linked directory remains: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(repo, "tracked")); err != nil || string(got) != "original" {
				t.Fatalf("primary bytes changed: %q, %v", got, err)
			}
		})
	}
}
