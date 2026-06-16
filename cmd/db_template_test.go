package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.email", "test@example.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
}

func TestCheckCleanWorktree_Clean(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	if err := checkCleanWorktree(dir); err != nil {
		t.Errorf("expected clean worktree, got error: %v", err)
	}
}

func TestCheckCleanWorktree_StagedChanges(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	f := filepath.Join(dir, "file.txt")
	_ = os.WriteFile(f, []byte("hello"), 0o644)
	_ = exec.Command("git", "-C", dir, "add", "file.txt").Run()

	if err := checkCleanWorktree(dir); err == nil {
		t.Error("expected error for staged changes, got nil")
	}
}

func TestCheckCleanWorktree_UnstagedChanges(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	// Commit a file first, then modify it without staging
	f := filepath.Join(dir, "file.txt")
	_ = os.WriteFile(f, []byte("original"), 0o644)
	_ = exec.Command("git", "-C", dir, "add", "file.txt").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "add file").Run()
	_ = os.WriteFile(f, []byte("modified"), 0o644)

	if err := checkCleanWorktree(dir); err == nil {
		t.Error("expected error for unstaged changes, got nil")
	}
}

func TestCheckCleanWorktree_UntrackedFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	_ = os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644)

	if err := checkCleanWorktree(dir); err != nil {
		t.Errorf("expected untracked files to be ignored, got error: %v", err)
	}
}
