package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Path resolution (shared resolveWorktreePath) is covered in new_test.go.

// --- gtl claim integration tests ---
//
// These drive claimCmd.RunE directly against real git fixtures (no
// .treeline.yml, so the setup/allocator/registry stack is never touched —
// that machinery is exercised in internal/setup). They rely on os.Chdir to
// place the process in the "main repo" checkout, matching the
// os.Chdir-based pattern in internal/worktree's tests; for the same reason
// they don't run in parallel.

func skipIfNoGitClaim(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available, skipping test")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s", args, string(out))
	}
	return strings.TrimSpace(string(out))
}

// claimFixture builds a bare origin, a "seed" clone that stands in for an
// agent pushing commits, and a "myapp" clone (mainRepo) with no
// .treeline.yml — the zero-config path claim takes when a repo isn't
// treeline-configured. Returns mainRepo and seed paths.
func claimFixture(t *testing.T) (mainRepo, seed string) {
	t.Helper()
	skipIfNoGitClaim(t)

	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)

	seed = filepath.Join(tmp, "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "init", "--initial-branch=main")
	runGit(t, seed, "commit", "--allow-empty", "-m", "init")

	origin := filepath.Join(tmp, "origin.git")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "init", "--bare", "--initial-branch=main")

	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")

	mainRepo = filepath.Join(tmp, "myapp")
	runGit(t, tmp, "clone", origin, mainRepo)

	return mainRepo, seed
}

// captureStdIO redirects os.Stdout/os.Stderr for fn's duration. claim.go
// writes directly to those (not through cmd.OutOrStdout), so this is the
// only way to assert its stdout-path-only / stderr-progress contract.
func captureStdIO(t *testing.T, fn func() error) (stdout, stderr string, fnErr error) {
	t.Helper()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	fnErr = fn()

	os.Stdout, os.Stderr = origOut, origErr
	_ = outW.Close()
	_ = errW.Close()

	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)
	return string(outBytes), string(errBytes), fnErr
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestClaim_FreshRemoteOnlyBranch(t *testing.T) {
	mainRepo, seed := claimFixture(t)

	// Agent pushes a branch straight to origin; mainRepo has never seen it.
	runGit(t, seed, "checkout", "-b", "feature-x")
	runGit(t, seed, "commit", "--allow-empty", "-m", "agent commit")
	runGit(t, seed, "push", "origin", "feature-x")
	remoteHead := runGit(t, seed, "rev-parse", "feature-x")

	chdir(t, mainRepo)

	stdout, stderr, err := captureStdIO(t, func() error {
		return claimCmd.RunE(claimCmd, []string{"feature-x"})
	})
	if err != nil {
		t.Fatalf("claim failed: %v\nstderr:\n%s", err, stderr)
	}

	wantPath := filepath.Join(filepath.Dir(mainRepo), "myapp-feature-x")
	if stdout != wantPath+"\n" {
		t.Errorf("stdout = %q, want exactly %q (path + newline, nothing else)", stdout, wantPath+"\n")
	}

	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Fatalf("worktree not created at %s: %v", wantPath, statErr)
	}

	if localHead := runGit(t, wantPath, "rev-parse", "HEAD"); localHead != remoteHead {
		t.Errorf("claimed worktree HEAD = %s, want origin's %s", localHead, remoteHead)
	}
	if branch := runGit(t, wantPath, "rev-parse", "--abbrev-ref", "HEAD"); branch != "feature-x" {
		t.Errorf("checked out branch = %q, want %q", branch, "feature-x")
	}
}

// TestClaim_SlashedBranchName guards the exact pattern behind the 'gtl
// where' project/branch misparse (impl/717f7b0e, feature/foo, spec/foo):
// claim resolves the branch at the git level (worktree.FindWorktreeForBranch,
// worktree.BranchExists), never through the registry's project/branch split
// that command used, so it was never subject to that bug — this test locks
// that in as a regression guard now that the two are documented together.
func TestClaim_SlashedBranchName(t *testing.T) {
	mainRepo, seed := claimFixture(t)

	runGit(t, seed, "checkout", "-b", "impl/717f7b0e")
	runGit(t, seed, "commit", "--allow-empty", "-m", "agent commit")
	runGit(t, seed, "push", "origin", "impl/717f7b0e")
	remoteHead := runGit(t, seed, "rev-parse", "impl/717f7b0e")

	chdir(t, mainRepo)

	stdout, stderr, err := captureStdIO(t, func() error {
		return claimCmd.RunE(claimCmd, []string{"impl/717f7b0e"})
	})
	if err != nil {
		t.Fatalf("claim failed: %v\nstderr:\n%s", err, stderr)
	}

	wantPath := filepath.Join(filepath.Dir(mainRepo), "myapp-impl/717f7b0e")
	if stdout != wantPath+"\n" {
		t.Errorf("stdout = %q, want exactly %q", stdout, wantPath+"\n")
	}
	if localHead := runGit(t, wantPath, "rev-parse", "HEAD"); localHead != remoteHead {
		t.Errorf("claimed worktree HEAD = %s, want origin's %s", localHead, remoteHead)
	}
	if branch := runGit(t, wantPath, "rev-parse", "--abbrev-ref", "HEAD"); branch != "impl/717f7b0e" {
		t.Errorf("checked out branch = %q, want %q", branch, "impl/717f7b0e")
	}
}

func TestClaim_IdempotentReClaim(t *testing.T) {
	mainRepo, seed := claimFixture(t)

	runGit(t, seed, "checkout", "-b", "feature-y")
	runGit(t, seed, "commit", "--allow-empty", "-m", "agent commit 1")
	runGit(t, seed, "push", "origin", "feature-y")

	chdir(t, mainRepo)

	stdout1, stderr1, err := captureStdIO(t, func() error {
		return claimCmd.RunE(claimCmd, []string{"feature-y"})
	})
	if err != nil {
		t.Fatalf("first claim failed: %v\nstderr:\n%s", err, stderr1)
	}
	wtPath := strings.TrimSpace(stdout1)

	// Agent pushes another commit after the first claim.
	runGit(t, seed, "commit", "--allow-empty", "-m", "agent commit 2")
	runGit(t, seed, "push", "origin", "feature-y")
	remoteHead := runGit(t, seed, "rev-parse", "feature-y")

	stdout2, stderr2, err := captureStdIO(t, func() error {
		return claimCmd.RunE(claimCmd, []string{"feature-y"})
	})
	if err != nil {
		t.Fatalf("re-claim failed: %v\nstderr:\n%s", err, stderr2)
	}
	if got := strings.TrimSpace(stdout2); got != wtPath {
		t.Errorf("re-claim path = %q, want %q (idempotent)", got, wtPath)
	}

	if localHead := runGit(t, wtPath, "rev-parse", "HEAD"); localHead != remoteHead {
		t.Errorf("re-claim did not pull latest: local HEAD %s, want %s", localHead, remoteHead)
	}
}

func TestClaim_DivergedPullWarnsButSucceeds(t *testing.T) {
	mainRepo, seed := claimFixture(t)

	runGit(t, seed, "checkout", "-b", "feature-z")
	runGit(t, seed, "commit", "--allow-empty", "-m", "agent commit 1")
	runGit(t, seed, "push", "origin", "feature-z")

	chdir(t, mainRepo)

	stdout1, stderr1, err := captureStdIO(t, func() error {
		return claimCmd.RunE(claimCmd, []string{"feature-z"})
	})
	if err != nil {
		t.Fatalf("first claim failed: %v\nstderr:\n%s", err, stderr1)
	}
	wtPath := strings.TrimSpace(stdout1)

	// Worktree gets a local-only commit...
	runGit(t, wtPath, "commit", "--allow-empty", "-m", "local-only commit")
	localOnlyHead := runGit(t, wtPath, "rev-parse", "HEAD")

	// ...while origin independently gets a different one: histories diverge.
	runGit(t, seed, "commit", "--allow-empty", "-m", "agent commit 2")
	runGit(t, seed, "push", "origin", "feature-z")

	stdout2, stderr2, err := captureStdIO(t, func() error {
		return claimCmd.RunE(claimCmd, []string{"feature-z"})
	})
	if err != nil {
		t.Fatalf("claim on a diverged branch must not fail: %v\nstderr:\n%s", err, stderr2)
	}
	if got := strings.TrimSpace(stdout2); got != wtPath {
		t.Errorf("stdout = %q, want %q even when diverged", got, wtPath)
	}
	if !strings.Contains(strings.ToLower(stderr2), "diverged") {
		t.Errorf("expected a 'diverged' warning on stderr, got:\n%s", stderr2)
	}

	// Not reset: the local-only commit must still be there.
	if head := runGit(t, wtPath, "rev-parse", "HEAD"); head != localOnlyHead {
		t.Errorf("worktree HEAD changed after a diverged pull: got %s, want unchanged %s", head, localOnlyHead)
	}
}

func TestClaim_BranchNotFoundAnywhere(t *testing.T) {
	mainRepo, _ := claimFixture(t)
	chdir(t, mainRepo)

	_, _, err := captureStdIO(t, func() error {
		return claimCmd.RunE(claimCmd, []string{"nonexistent-branch-xyz"})
	})
	if err == nil {
		t.Fatal("expected an error for a branch that doesn't exist locally or on origin")
	}
}
