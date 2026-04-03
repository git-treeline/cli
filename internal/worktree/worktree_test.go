package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available, skipping test")
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	skipIfNoGit(t)
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

	branches, err := MergedBranches(repo, "")
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

func TestParseHeadBranch(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "main branch",
			output: `* remote origin
  Fetch URL: git@github.com:example/repo.git
  Push  URL: git@github.com:example/repo.git
  HEAD branch: main
  Remote branches:
    main tracked`,
			want: "main",
		},
		{
			name: "staging branch",
			output: `* remote origin
  HEAD branch: staging
  Remote branches:
    staging tracked`,
			want: "staging",
		},
		{
			name: "master branch",
			output: `* remote origin
  HEAD branch: master`,
			want: "master",
		},
		{
			name: "unknown HEAD",
			output: `* remote origin
  HEAD branch: (unknown)`,
			want: "",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
		{
			name:   "no HEAD line",
			output: "* remote origin\n  Fetch URL: git@github.com:example/repo.git\n",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHeadBranch(tt.output)
			if got != tt.want {
				t.Errorf("parseHeadBranch() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectBranchFromLocalCandidates_Main(t *testing.T) {
	repo := initTestRepo(t)
	got := detectBranchFromLocalCandidates(repo)
	if got != "main" {
		t.Errorf("expected main, got %q", got)
	}
}

func TestDetectBranchFromLocalCandidates_Master(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	run(t, dir, "git", "init", "--initial-branch=master")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")

	got := detectBranchFromLocalCandidates(dir)
	if got != "master" {
		t.Errorf("expected master, got %q", got)
	}
}

func TestDetectBranchFromLocalCandidates_Develop(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	run(t, dir, "git", "init", "--initial-branch=develop")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")

	got := detectBranchFromLocalCandidates(dir)
	if got != "develop" {
		t.Errorf("expected develop, got %q", got)
	}
}

func TestDetectDefaultBranch_FallsBackToLocalMain(t *testing.T) {
	repo := initTestRepo(t)
	got := DetectDefaultBranch(repo)
	if got != "main" {
		t.Errorf("expected main, got %q", got)
	}
}

func TestDetectDefaultBranch_FallsBackToLocalDevelop(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	run(t, dir, "git", "init", "--initial-branch=develop")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")

	got := DetectDefaultBranch(dir)
	if got != "develop" {
		t.Errorf("expected develop, got %q", got)
	}
}

func TestDetectBranchFromLocalCandidates_Staging_NotInList(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	run(t, dir, "git", "init", "--initial-branch=staging")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")

	got := detectBranchFromLocalCandidates(dir)
	if got != "" {
		t.Errorf("expected empty (staging not in candidate list), got %q", got)
	}
}

func TestMergedBranches_WithOverride(t *testing.T) {
	repo := initTestRepo(t)

	run(t, repo, "git", "checkout", "-b", "develop")
	run(t, repo, "git", "commit", "--allow-empty", "-m", "develop base")

	run(t, repo, "git", "checkout", "-b", "feature-x")
	run(t, repo, "git", "commit", "--allow-empty", "-m", "feature work")
	run(t, repo, "git", "checkout", "develop")
	run(t, repo, "git", "merge", "--no-ff", "feature-x", "-m", "merge feature-x")

	branches, err := MergedBranches(repo, "develop")
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, b := range branches {
		if b == "feature-x" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected feature-x in merged branches with develop override, got %v", branches)
	}
}

func TestMergedBranches_NoMerged(t *testing.T) {
	repo := initTestRepo(t)

	branches, err := MergedBranches(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 0 {
		t.Errorf("expected no merged branches, got %v", branches)
	}
}
