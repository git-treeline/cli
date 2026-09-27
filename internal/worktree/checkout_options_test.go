package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckoutOptionsDeferSetupKeepsOtherHookBehavior(t *testing.T) {
	removalEnvironment(t)
	t.Setenv("GTL_DEFER_SETUP", "")
	repo := initTestRepo(t)
	run(t, repo, "git", "branch", "feature")
	hook := "#!/bin/sh\nprintf '%s' \"${GTL_DEFER_SETUP:-unset}\" > observed-defer\ntouch unrelated-hook-ran\n"
	if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", "post-checkout"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	if err := CheckoutWithOptions("feature", CheckoutOptions{DeferSetup: true}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(repo, "observed-defer")); string(raw) != "1" {
		t.Fatalf("owned checkout didn't defer setup: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(repo, "unrelated-hook-ran")); err != nil {
		t.Fatal("owned checkout suppressed other hook commands")
	}
	if err := Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(repo, "observed-defer")); string(raw) != "unset" {
		t.Fatalf("raw checkout default changed: %q", raw)
	}
	other := initTestRepo(t)
	t.Chdir(other)
	if err := CheckoutInRepoWithOptions(repo, "feature", CheckoutOptions{DeferSetup: true}); err != nil {
		t.Fatal(err)
	}
	if CurrentBranch(repo) != "feature" || CurrentBranch(other) != "main" {
		t.Fatal("explicit admitted checkout followed ambient cwd")
	}
}
