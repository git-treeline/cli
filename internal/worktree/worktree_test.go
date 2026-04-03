package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks (macOS /var -> /private/var)
	dir, _ = filepath.EvalSymlinks(dir)
	run(t, dir, "git", "init", "--initial-branch=main")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")
	return dir
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %s", name, args, string(out))
	}
}

func TestCreateNewBranch(t *testing.T) {
	repo := initTestRepo(t)
	wtDir := t.TempDir()
	wtDir, _ = filepath.EvalSymlinks(wtDir)
	wtPath := filepath.Join(wtDir, "feature-branch")

	// Create must run from within the repo
	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	err := Create(wtPath, "feature-branch", true, "main")
	if err != nil {
		t.Fatalf("Create new branch: %v", err)
	}
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("worktree directory was not created")
	}

	cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = repo
	_ = cmd.Run()
}

func TestCreateExistingBranch(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "existing-branch")

	wtDir := t.TempDir()
	wtDir, _ = filepath.EvalSymlinks(wtDir)
	wtPath := filepath.Join(wtDir, "existing-branch")

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	err := Create(wtPath, "existing-branch", false, "")
	if err != nil {
		t.Fatalf("Create existing branch: %v", err)
	}
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("worktree directory was not created")
	}

	cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = repo
	_ = cmd.Run()
}

func TestBranchExists(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "test-branch")

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	if !BranchExists("test-branch") {
		t.Error("expected test-branch to exist")
	}
	if BranchExists("nonexistent-branch") {
		t.Error("expected nonexistent-branch to not exist")
	}
}

func TestDetectMainRepo(t *testing.T) {
	repo := initTestRepo(t)
	result := DetectMainRepo(repo)
	if result != repo {
		t.Errorf("DetectMainRepo = %q, want %q", result, repo)
	}
}

func TestMergedBranches(t *testing.T) {
	repo := initTestRepo(t)

	// Create and merge a feature branch
	run(t, repo, "git", "checkout", "-b", "feature-done")
	run(t, repo, "git", "commit", "--allow-empty", "-m", "feature work")
	run(t, repo, "git", "checkout", "main")
	run(t, repo, "git", "merge", "--no-ff", "feature-done", "-m", "merge feature-done")

	// Create an unmerged branch
	run(t, repo, "git", "checkout", "-b", "feature-wip")
	run(t, repo, "git", "commit", "--allow-empty", "-m", "wip")
	run(t, repo, "git", "checkout", "main")

	branches, err := MergedBranches(repo)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, b := range branches {
		if b == "feature-done" {
			found = true
		}
		if b == "feature-wip" {
			t.Error("feature-wip should NOT be in merged branches")
		}
		if b == "main" {
			t.Error("main itself should be excluded from results")
		}
	}
	if !found {
		t.Errorf("expected feature-done in merged branches, got %v", branches)
	}
}

func TestMergedBranches_NoMerged(t *testing.T) {
	repo := initTestRepo(t)

	branches, err := MergedBranches(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 0 {
		t.Errorf("expected no merged branches, got %v", branches)
	}
}
