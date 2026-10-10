package setup

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func templateCheckoutGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func templateCheckoutFixture(t *testing.T) (mainRepo, remote string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root := t.TempDir()
	remote = filepath.Join(root, "remote")
	mainRepo = filepath.Join(root, "main")
	templateCheckoutGit(t, root, "init", "--initial-branch=develop", remote)
	if err := os.WriteFile(filepath.Join(remote, "schema"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	templateCheckoutGit(t, remote, "add", "schema")
	templateCheckoutGit(t, remote, "commit", "-m", "initial")
	templateCheckoutGit(t, root, "clone", remote, mainRepo)
	return mainRepo, remote
}

func assertTemplateCheckoutCleaned(t *testing.T, repo, path, worktrees string) {
	t.Helper()
	if got := templateCheckoutGit(t, repo, "worktree", "list", "--porcelain"); got != worktrees {
		t.Fatalf("worktrees changed:\nbefore: %s\nafter: %s", worktrees, got)
	}
	if got := templateCheckoutGit(t, repo, "for-each-ref", "refs/gtl/template-updates/"); got != "" {
		t.Fatalf("temporary refs remain: %s", got)
	}
	if path != "" {
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("temporary directory remains: %s (%v)", filepath.Dir(path), err)
		}
	}
}

func TestTemplateCheckoutIsolatesLatestRemoteFromExistingWorktrees(t *testing.T) {
	mainRepo, remote := templateCheckoutFixture(t)
	templateCheckoutGit(t, mainRepo, "switch", "-c", "feature/trunk")
	other := filepath.Join(t.TempDir(), "develop")
	templateCheckoutGit(t, mainRepo, "worktree", "add", other, "develop")
	if err := os.WriteFile(filepath.Join(mainRepo, "schema"), []byte("staged local work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	templateCheckoutGit(t, mainRepo, "add", "schema")
	if err := os.WriteFile(filepath.Join(mainRepo, "schema"), []byte("unstaged local work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "schema"), []byte("new remote schema\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	templateCheckoutGit(t, remote, "commit", "-am", "new schema")
	wantCommit := templateCheckoutGit(t, remote, "rev-parse", "HEAD")
	refs := templateCheckoutGit(t, mainRepo, "show-ref")
	worktrees := templateCheckoutGit(t, mainRepo, "worktree", "list", "--porcelain")
	index := templateCheckoutGit(t, mainRepo, "diff", "--cached")
	fetchHead := filepath.Join(mainRepo, ".git", "FETCH_HEAD")
	if err := os.WriteFile(fetchHead, []byte("unchanged FETCH_HEAD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hookMarker := filepath.Join(mainRepo, "hook-ran")
	hooks := filepath.Join(t.TempDir(), "custom-hooks")
	if err := os.Mkdir(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"post-checkout", "reference-transaction"} {
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte("#!/bin/sh\ntouch '"+hookMarker+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	templateCheckoutGit(t, mainRepo, "config", "core.hooksPath", hooks)
	var checkout string
	err := withTemplateCheckout(context.Background(), mainRepo, "develop", io.Discard, func(path string) error {
		checkout = path
		if got := templateCheckoutGit(t, path, "rev-parse", "HEAD"); got != wantCommit {
			t.Errorf("checkout commit = %s, want %s", got, wantCommit)
		}
		if got := templateCheckoutGit(t, path, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
			t.Errorf("checkout is not detached: %s", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTemplateCheckoutCleaned(t, mainRepo, checkout, worktrees)
	if got := templateCheckoutGit(t, mainRepo, "show-ref"); got != refs {
		t.Errorf("existing refs changed:\n%s\n%s", refs, got)
	}
	if got := templateCheckoutGit(t, mainRepo, "diff", "--cached"); got != index {
		t.Error("main checkout index changed")
	}
	for path, want := range map[string]string{
		filepath.Join(mainRepo, "schema"): "unstaged local work\n",
		filepath.Join(other, "schema"):    "original\n",
		fetchHead:                         "unchanged FETCH_HEAD\n",
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("%s changed: %q (%v)", path, data, err)
		}
	}
	if _, err := os.Stat(hookMarker); !os.IsNotExist(err) {
		t.Fatalf("checkout or reference hook ran: %v", err)
	}
}

func TestTemplateCheckoutCleansAfterCallbackFailureAndCancellation(t *testing.T) {
	mainRepo, _ := templateCheckoutFixture(t)
	worktrees := templateCheckoutGit(t, mainRepo, "worktree", "list", "--porcelain")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("migration failed")
	var checkout string
	err := withTemplateCheckout(ctx, mainRepo, "", io.Discard, func(path string) error {
		checkout = path
		cancel()
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("got %v, want callback failure", err)
	}
	assertTemplateCheckoutCleaned(t, mainRepo, checkout, worktrees)
}

func TestTemplateCheckoutRejectsFetchFailureAndInvalidBranches(t *testing.T) {
	mainRepo, _ := templateCheckoutFixture(t)
	worktrees := templateCheckoutGit(t, mainRepo, "worktree", "list", "--porcelain")
	for _, branch := range []string{"missing", "--upload-pack=anything", "develop:other", "../develop", "@{-1}"} {
		t.Run(branch, func(t *testing.T) {
			err := withTemplateCheckout(context.Background(), mainRepo, branch, nil, func(string) error {
				t.Error("callback ran after invalid branch or failed fetch")
				return nil
			})
			if err == nil {
				t.Fatal("expected error")
			}
			assertTemplateCheckoutCleaned(t, mainRepo, "", worktrees)
		})
	}
}

func TestTemplateCheckoutRejectsConcurrentUpdate(t *testing.T) {
	mainRepo, _ := templateCheckoutFixture(t)
	err := withTemplateCheckout(context.Background(), mainRepo, "develop", nil, func(path string) error {
		returnErr := withTemplateCheckout(context.Background(), path, "develop", nil, func(string) error {
			t.Error("concurrent update ran")
			return nil
		})
		if returnErr == nil || !strings.Contains(returnErr.Error(), "already running") {
			t.Errorf("expected busy error, got %v", returnErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTemplateCheckoutFallsBackToMainWithoutOriginHEAD(t *testing.T) {
	mainRepo, remote := templateCheckoutFixture(t)
	templateCheckoutGit(t, remote, "branch", "main")
	templateCheckoutGit(t, mainRepo, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	wantCommit := templateCheckoutGit(t, remote, "rev-parse", "main")
	err := withTemplateCheckout(context.Background(), mainRepo, "", nil, func(path string) error {
		if got := templateCheckoutGit(t, path, "rev-parse", "HEAD"); got != wantCommit {
			t.Errorf("checkout commit = %s, want main at %s", got, wantCommit)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTemplateCheckoutRetainsCheckoutWhenCleanupFails(t *testing.T) {
	mainRepo, _ := templateCheckoutFixture(t)
	var checkout string
	t.Cleanup(func() {
		if checkout != "" {
			templateCheckoutGit(t, mainRepo, "worktree", "unlock", checkout)
			templateCheckoutGit(t, mainRepo, "worktree", "remove", "--force", checkout)
			_ = os.RemoveAll(filepath.Dir(checkout))
		}
	})
	err := withTemplateCheckout(context.Background(), mainRepo, "develop", nil, func(path string) error {
		checkout = path
		templateCheckoutGit(t, mainRepo, "worktree", "lock", path)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "retained at "+checkout) {
		t.Fatalf("expected cleanup error with retained path, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout, ".git")); err != nil {
		t.Fatalf("failed-cleanup checkout was removed: %v", err)
	}
	if got := templateCheckoutGit(t, mainRepo, "for-each-ref", "refs/gtl/template-updates/"); got != "" {
		t.Errorf("temporary ref remains: %s", got)
	}
}

func TestTemplateCheckoutIgnoresInheritedGitRepositoryEnvironment(t *testing.T) {
	mainRepo, _ := templateCheckoutFixture(t)
	worktrees := templateCheckoutGit(t, mainRepo, "worktree", "list", "--porcelain")
	var checkout string
	t.Run("inherited hook environment", func(t *testing.T) {
		for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY"} {
			t.Setenv(key, filepath.Join(t.TempDir(), "missing"))
		}
		err := withTemplateCheckout(context.Background(), mainRepo, "develop", nil, func(path string) error {
			checkout = path
			data, err := os.ReadFile(filepath.Join(path, "schema"))
			if err != nil || string(data) != "original\n" {
				t.Errorf("wrong checkout contents: %q (%v)", data, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertTemplateCheckoutCleaned(t, mainRepo, checkout, worktrees)
}

func templateCheckoutGitShim(t *testing.T, version, commonDir string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TEMPLATE_CHECKOUT_GIT_LOG"
if [ "$1" = "--version" ]; then
    printf '%s\n' "$TEMPLATE_CHECKOUT_GIT_VERSION"
    exit 0
fi
for arg in "$@"; do
    case "$arg" in
        --path-format*) echo 'unsupported option: --path-format' >&2; exit 1 ;;
    esac
done
if [ "$1" = "rev-parse" ] && [ "$2" = "--git-common-dir" ]; then
    printf '%s\n' "$TEMPLATE_CHECKOUT_GIT_COMMON_DIR"
    exit 0
fi
exec "$TEMPLATE_CHECKOUT_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEMPLATE_CHECKOUT_REAL_GIT", realGit)
	t.Setenv("TEMPLATE_CHECKOUT_GIT_VERSION", version)
	t.Setenv("TEMPLATE_CHECKOUT_GIT_COMMON_DIR", commonDir)
	t.Setenv("TEMPLATE_CHECKOUT_GIT_LOG", log)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestTemplateCheckoutSupportsGit230CommonDirectoryPaths(t *testing.T) {
	for _, linked := range []bool{false, true} {
		for _, relative := range []bool{false, true} {
			name := "main"
			if linked {
				name = "linked"
			}
			if relative {
				name += "/relative"
			} else {
				name += "/absolute"
			}
			t.Run(name, func(t *testing.T) {
				mainRepo, _ := templateCheckoutFixture(t)
				repo := mainRepo
				if linked {
					repo = filepath.Join(t.TempDir(), "linked")
					templateCheckoutGit(t, mainRepo, "worktree", "add", "--detach", repo)
				}
				commonDir := filepath.Join(mainRepo, ".git")
				if relative {
					var err error
					commonDir, err = filepath.Rel(repo, commonDir)
					if err != nil {
						t.Fatal(err)
					}
					cwd, err := os.Getwd()
					if err != nil {
						t.Fatal(err)
					}
					repo, err = filepath.Rel(cwd, repo)
					if err != nil {
						t.Fatal(err)
					}
				}
				worktrees := templateCheckoutGit(t, mainRepo, "worktree", "list", "--porcelain")
				templateCheckoutGitShim(t, "git version 2.30.0", commonDir)
				var checkout string
				err := withTemplateCheckout(context.Background(), repo, "develop", nil, func(path string) error {
					checkout = path
					data, err := os.ReadFile(filepath.Join(path, "schema"))
					if err != nil || string(data) != "original\n" {
						t.Errorf("wrong checkout contents: %q (%v)", data, err)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if checkout == "" {
					t.Fatal("callback did not run")
				}
				assertTemplateCheckoutCleaned(t, mainRepo, checkout, worktrees)
			})
		}
	}
}

func TestTemplateCheckoutRejectsOldGitBeforeMutation(t *testing.T) {
	mainRepo, _ := templateCheckoutFixture(t)
	log := templateCheckoutGitShim(t, "git version 2.28.0", ".git")
	err := withTemplateCheckout(context.Background(), mainRepo, "develop", nil, func(string) error {
		t.Error("callback ran with unsupported Git")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "require Git 2.29 or newer (found 2.28.0)") {
		t.Fatalf("expected actionable Git version error, got %v", err)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(commands) != "--version\n" {
		t.Errorf("Git commands ran after failed version check: %s", commands)
	}
	if _, err := os.Stat(filepath.Join(mainRepo, ".git", "gtl-template-update.lock")); !os.IsNotExist(err) {
		t.Errorf("lock file created before version check: %v", err)
	}
}

func TestCheckTemplateCheckoutGitVersion(t *testing.T) {
	for _, version := range []string{
		"git version 2.29.0", "git version 2.30.0", "git version 2.39.5 (Apple Git-154)",
		"git version 2.48.1.windows.1", "git version 3.0.0",
	} {
		t.Run(version, func(t *testing.T) {
			if err := checkTemplateCheckoutGitVersion(version); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, version := range []string{
		"git version 2.28.0", "git version 1.99.0", "git version 2.9.5",
		"", "git version", "git version unknown", "git version 2.bad.0", "git version -2.30.0",
	} {
		t.Run(version, func(t *testing.T) {
			if err := checkTemplateCheckoutGitVersion(version); err == nil {
				t.Fatal("expected version error")
			}
		})
	}
}
