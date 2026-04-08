package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
}

func TestEnsureGitignored_OutsideRepo(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	sibling := filepath.Join(filepath.Dir(repo), "sibling-wt")

	pat, err := EnsureGitignored(repo, sibling)
	if err != nil {
		t.Fatal(err)
	}
	if pat != "" {
		t.Errorf("expected empty pattern for sibling path, got %q", pat)
	}
}

func TestEnsureGitignored_InsideRepo(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	wtPath := filepath.Join(repo, ".worktrees", "feat-x")

	pat, err := EnsureGitignored(repo, wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if pat != "/.worktrees/" {
		t.Errorf("expected /.worktrees/, got %q", pat)
	}

	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/.worktrees/") {
		t.Errorf("expected /.worktrees/ in .gitignore, got: %s", data)
	}
}

func TestEnsureGitignored_AlreadyPresent(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("/.worktrees/\n"), 0o644)
	wtPath := filepath.Join(repo, ".worktrees", "feat-y")

	pat, err := EnsureGitignored(repo, wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if pat != "" {
		t.Errorf("expected empty pattern (already present), got %q", pat)
	}

	data, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if strings.Count(string(data), "/.worktrees/") != 1 {
		t.Errorf("expected exactly one entry, got: %s", data)
	}
}

func TestEnsureGitignored_AppendsToExisting(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o644)
	wtPath := filepath.Join(repo, ".worktrees", "feat-z")

	pat, err := EnsureGitignored(repo, wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if pat != "/.worktrees/" {
		t.Errorf("expected /.worktrees/, got %q", pat)
	}

	data, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	content := string(data)
	if !strings.Contains(content, "node_modules/") {
		t.Error("existing entries should be preserved")
	}
	if !strings.Contains(content, "/.worktrees/") {
		t.Error("expected /.worktrees/ to be appended")
	}
}
