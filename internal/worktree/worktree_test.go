package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TODO: These tests use os.Chdir because several worktree functions rely on
// process-wide cwd rather than accepting a dir parameter. This makes them
// incompatible with t.Parallel(). The long-term fix is to thread dir through
// the production API (Create, BranchExists, Checkout, ListBranches, etc.).

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

func TestCheckout(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "feature-checkout")

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	if err := Checkout("feature-checkout"); err != nil {
		t.Fatalf("Checkout failed: %v", err)
	}

	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get HEAD: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch != "feature-checkout" {
		t.Errorf("expected HEAD on feature-checkout, got %s", branch)
	}
}

func TestCheckout_NonexistentBranch(t *testing.T) {
	repo := initTestRepo(t)

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	if err := Checkout("nonexistent-branch-xyz"); err == nil {
		t.Error("expected error for nonexistent branch")
	}
}

func TestListBranches(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "alpha")
	run(t, repo, "git", "branch", "beta")
	run(t, repo, "git", "branch", "gamma")

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	all := ListBranches("")
	if len(all) < 3 {
		t.Errorf("expected at least 3 branches, got %d: %v", len(all), all)
	}

	filtered := ListBranches("al")
	if len(filtered) != 1 || filtered[0] != "alpha" {
		t.Errorf("expected [alpha], got %v", filtered)
	}

	none := ListBranches("zzz-no-match")
	if len(none) != 0 {
		t.Errorf("expected empty, got %v", none)
	}
}

func TestListBranches_DeduplicatesOrigin(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "feature-x")

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	branches := ListBranches("feature")
	count := 0
	for _, b := range branches {
		if b == "feature-x" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected feature-x exactly once, got %d in %v", count, branches)
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

func TestFindWorktreeForBranch(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "wt-find-test")

	wtDir := t.TempDir()
	wtDir, _ = filepath.EvalSymlinks(wtDir)
	wtPath := filepath.Join(wtDir, "wt-find-test")

	run(t, repo, "git", "worktree", "add", wtPath, "wt-find-test")
	defer func() {
		cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
		cmd.Dir = repo
		_ = cmd.Run()
	}()

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	found := FindWorktreeForBranch("wt-find-test")
	if found != wtPath {
		t.Errorf("expected %s, got %s", wtPath, found)
	}

	missing := FindWorktreeForBranch("nonexistent-branch")
	if missing != "" {
		t.Errorf("expected empty for missing branch, got %s", missing)
	}
}

func TestWorktreeBranches(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "branch-a")
	run(t, repo, "git", "branch", "branch-b")

	wtDirA := t.TempDir()
	wtDirA, _ = filepath.EvalSymlinks(wtDirA)
	wtPathA := filepath.Join(wtDirA, "branch-a")

	wtDirB := t.TempDir()
	wtDirB, _ = filepath.EvalSymlinks(wtDirB)
	wtPathB := filepath.Join(wtDirB, "branch-b")

	run(t, repo, "git", "worktree", "add", wtPathA, "branch-a")
	run(t, repo, "git", "worktree", "add", wtPathB, "branch-b")
	defer func() {
		cmd := exec.Command("git", "worktree", "remove", "--force", wtPathA)
		cmd.Dir = repo
		_ = cmd.Run()
		cmd = exec.Command("git", "worktree", "remove", "--force", wtPathB)
		cmd.Dir = repo
		_ = cmd.Run()
	}()

	branches := WorktreeBranches(repo)
	if branches == nil {
		t.Fatal("expected non-nil branches map")
	}

	if branches[wtPathA] != "branch-a" {
		t.Errorf("expected branch-a at %s, got %s", wtPathA, branches[wtPathA])
	}
	if branches[wtPathB] != "branch-b" {
		t.Errorf("expected branch-b at %s, got %s", wtPathB, branches[wtPathB])
	}
	if branches[repo] != "main" {
		t.Errorf("expected main at %s, got %s", repo, branches[repo])
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

func TestUnpushedCommitCount_NoRemote(t *testing.T) {
	repo := initTestRepo(t)

	// Local repo with no remote — all commits are "unpushed"
	count := UnpushedCommitCount(repo)
	if count != 1 {
		t.Errorf("expected 1 unpushed commit (initial), got %d", count)
	}

	// Add another commit
	file := filepath.Join(repo, "file2.txt")
	if err := os.WriteFile(file, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "second commit")

	count = UnpushedCommitCount(repo)
	if count != 2 {
		t.Errorf("expected 2 unpushed commits, got %d", count)
	}
}

func TestCurrentBranch_OnBranch(t *testing.T) {
	repo := initTestRepo(t)
	got := CurrentBranch(repo)
	if got != "main" {
		t.Errorf("expected main, got %q", got)
	}
}

func TestCurrentBranch_DetachedHEAD(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "checkout", "--detach", "HEAD")

	got := CurrentBranch(repo)
	if got != "" {
		t.Errorf("expected empty string for detached HEAD, got %q", got)
	}
}

func TestCurrentBranch_InWorktree(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "wt-branch")

	wtDir := t.TempDir()
	wtDir, _ = filepath.EvalSymlinks(wtDir)
	wtPath := filepath.Join(wtDir, "wt-branch")

	run(t, repo, "git", "worktree", "add", wtPath, "wt-branch")
	defer func() {
		cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
		cmd.Dir = repo
		_ = cmd.Run()
	}()

	got := CurrentBranch(wtPath)
	if got != "wt-branch" {
		t.Errorf("expected wt-branch, got %q", got)
	}

	gotMain := CurrentBranch(repo)
	if gotMain != "main" {
		t.Errorf("expected main repo still on main, got %q", gotMain)
	}
}

func TestHasUncommittedChanges_Clean(t *testing.T) {
	repo := initTestRepo(t)
	if HasUncommittedChanges(repo) {
		t.Error("expected no uncommitted changes on clean repo")
	}
}

func TestHasUncommittedChanges_UnstagedChanges(t *testing.T) {
	repo := initTestRepo(t)
	file := filepath.Join(repo, "dirty.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "dirty.txt")
	run(t, repo, "git", "commit", "-m", "add file")

	if err := os.WriteFile(file, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !HasUncommittedChanges(repo) {
		t.Error("expected uncommitted changes with unstaged modification")
	}
}

func TestHasUncommittedChanges_StagedChanges(t *testing.T) {
	repo := initTestRepo(t)
	file := filepath.Join(repo, "staged.txt")
	if err := os.WriteFile(file, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "staged.txt")

	if !HasUncommittedChanges(repo) {
		t.Error("expected uncommitted changes with staged file")
	}
}

func TestHasUncommittedChanges_AfterCommit(t *testing.T) {
	repo := initTestRepo(t)
	file := filepath.Join(repo, "committed.txt")
	if err := os.WriteFile(file, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "committed.txt")
	run(t, repo, "git", "commit", "-m", "commit file")

	if HasUncommittedChanges(repo) {
		t.Error("expected no uncommitted changes after commit")
	}
}

func TestRemove_Worktree(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "remove-test")

	wtDir := t.TempDir()
	wtDir, _ = filepath.EvalSymlinks(wtDir)
	wtPath := filepath.Join(wtDir, "remove-test")

	run(t, repo, "git", "worktree", "add", wtPath, "remove-test")

	err := Remove(wtPath, false)
	if err != nil {
		t.Fatalf("Remove worktree failed: %v", err)
	}

	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Error("expected worktree directory to be removed")
	}
}

func TestRemove_MainWorktree(t *testing.T) {
	repo := initTestRepo(t)

	err := Remove(repo, false)
	if err == nil {
		t.Fatal("expected error when removing main worktree")
	}
	if !strings.Contains(err.Error(), "cannot remove the main worktree") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemove_Force_DirtyWorktree(t *testing.T) {
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "dirty-wt")

	wtDir := t.TempDir()
	wtDir, _ = filepath.EvalSymlinks(wtDir)
	wtPath := filepath.Join(wtDir, "dirty-wt")

	run(t, repo, "git", "worktree", "add", wtPath, "dirty-wt")

	file := filepath.Join(wtPath, "uncommitted.txt")
	if err := os.WriteFile(file, []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wtPath, "git", "add", "uncommitted.txt")

	err := Remove(wtPath, false)
	if err == nil {
		t.Fatal("expected error removing dirty worktree without force")
	}

	err = Remove(wtPath, true)
	if err != nil {
		t.Fatalf("force remove failed: %v", err)
	}

	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Error("expected worktree directory to be removed after force")
	}
}

func TestFetch_Success(t *testing.T) {
	repo := initTestRepo(t)

	remoteDir := t.TempDir()
	remoteDir, _ = filepath.EvalSymlinks(remoteDir)
	run(t, remoteDir, "git", "init", "--bare")

	run(t, repo, "git", "remote", "add", "origin", remoteDir)
	run(t, repo, "git", "push", "origin", "main")

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	err := Fetch("origin", "main")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
}

func TestFetch_NonexistentRemote(t *testing.T) {
	repo := initTestRepo(t)

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	err := Fetch("nonexistent-remote", "main")
	if err == nil {
		t.Error("expected error for non-existent remote")
	}
}

func TestFetch_NonexistentBranch(t *testing.T) {
	repo := initTestRepo(t)

	remoteDir := t.TempDir()
	remoteDir, _ = filepath.EvalSymlinks(remoteDir)
	run(t, remoteDir, "git", "init", "--bare")

	run(t, repo, "git", "remote", "add", "origin", remoteDir)
	run(t, repo, "git", "push", "origin", "main")

	orig, _ := os.Getwd()
	_ = os.Chdir(repo)
	defer func() { _ = os.Chdir(orig) }()

	err := Fetch("origin", "nonexistent-branch-xyz")
	if err == nil {
		t.Error("expected error for non-existent branch")
	}
}
