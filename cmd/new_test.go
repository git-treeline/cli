package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/setup"
)

func TestNewNoSetupDefersLegacyPostCheckoutHook(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating test source")
	}
	repoRoot := filepath.Dir(filepath.Dir(sourceFile))
	binDir := t.TempDir()
	gtl := filepath.Join(binDir, "git-treeline")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", gtl, "./cmd/git-treeline")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gtl fixture: %v\n%s", err, out)
	}

	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	home := filepath.Join(root, "home")
	gtlHome := filepath.Join(root, "gtl-home")
	for _, dir := range []string{home, gtlHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	baseEnv := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GTL_HOME="+gtlHome,
	)
	runIsolatedGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(baseEnv,
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shellPath := func(path string) string {
		return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
	}
	mainRepo := filepath.Join(root, "repo")
	runIsolatedGit(root, "init", "--initial-branch=main", mainRepo)
	setupMarker := filepath.Join(root, "setup-ran")
	configBody := "project: hooktest\nport_count: 1\nenv_file: .env.local\nenv:\n  PORT: \"{port}\"\ncommands:\n  setup:\n    - touch " + shellPath(setupMarker) + "\n"
	writeFile(t, filepath.Join(mainRepo, config.ProjectConfigFile), configBody)
	runIsolatedGit(mainRepo, "add", config.ProjectConfigFile)
	runIsolatedGit(mainRepo, "commit", "-m", "config")
	runIsolatedGit(mainRepo, "branch", "existing")

	hookMarker := filepath.Join(root, "unrelated-hook-ran")
	oldGTLMarker := filepath.Join(root, "old-gtl-ran")
	hook := "#!/bin/sh\ntouch " + shellPath(hookMarker) + "\ngtl port >/dev/null 2>&1 && gtl editor refresh || gtl setup .\n"
	hookPath := filepath.Join(mainRepo, ".git", "hooks", "post-checkout")
	writeFile(t, hookPath, hook)
	if err := os.Chmod(hookPath, 0o755); err != nil {
		t.Fatal(err)
	}

	oldBin := filepath.Join(root, "old-bin")
	if err := os.MkdirAll(oldBin, 0o755); err != nil {
		t.Fatal(err)
	}
	oldGTL := filepath.Join(oldBin, "gtl")
	writeFile(t, oldGTL, "#!/bin/sh\ntouch "+shellPath(oldGTLMarker)+"\nexit 1\n")
	if err := os.Chmod(oldGTL, 0o755); err != nil {
		t.Fatal(err)
	}

	baseEnv = append(baseEnv, "PATH="+oldBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runCLI := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(gtl, args...)
		cmd.Dir = mainRepo
		cmd.Env = baseEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gtl %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	assertAbsent := func(path, label string) {
		t.Helper()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s unexpectedly exists: %v", label, err)
		}
	}

	newWT := filepath.Join(root, "new-wt")
	runCLI("new", "new-branch", "--no-setup", "--path", newWT)
	if _, err := os.Stat(hookMarker); err != nil {
		t.Fatalf("unrelated hook command did not run: %v", err)
	}
	assertAbsent(oldGTLMarker, "older PATH gtl marker")
	assertAbsent(setupMarker, "setup command marker")
	assertAbsent(filepath.Join(newWT, ".env.local"), "generated env file")
	assertAbsent(filepath.Join(gtlHome, "registry.json"), "allocation registry")

	if err := os.Remove(hookMarker); err != nil {
		t.Fatal(err)
	}
	existingWT := filepath.Join(root, "existing-wt")
	runCLI("new", "existing", "--no-setup", "--path", existingWT)
	if _, err := os.Stat(hookMarker); err != nil {
		t.Fatalf("unrelated hook command did not run for existing branch: %v", err)
	}
	assertAbsent(oldGTLMarker, "older PATH gtl marker")
	assertAbsent(setupMarker, "setup command marker")

	// Resuming an already checked-out branch with --no-setup must remain inert.
	runCLI("new", "existing", "--no-setup")
	assertAbsent(setupMarker, "setup command marker after resume")
	assertAbsent(filepath.Join(gtlHome, "registry.json"), "allocation registry after resume")

	setup := exec.Command(gtl, "setup")
	setup.Dir = newWT
	setup.Env = baseEnv
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("explicit gtl setup: %v\n%s", err, out)
	}
	if _, err := os.Stat(setupMarker); err != nil {
		t.Fatalf("explicit setup did not run configured command: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newWT, ".env.local")); err != nil {
		t.Fatalf("explicit setup did not generate env file: %v", err)
	}

	// A raw git worktree add has no deferral env and still runs hook setup.
	if err := os.Remove(setupMarker); err != nil {
		t.Fatal(err)
	}
	currentBin := filepath.Join(root, "current-bin")
	if err := os.MkdirAll(currentBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gtl, filepath.Join(currentBin, "gtl")); err != nil {
		t.Fatal(err)
	}
	runGitCmd := exec.Command("git", "worktree", "add", filepath.Join(root, "raw-wt"), "-b", "raw-branch", "main")
	runGitCmd.Dir = mainRepo
	runGitCmd.Env = append(baseEnv, "PATH="+currentBin+string(os.PathListSeparator)+oldBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runGitCmd.Env = append(runGitCmd.Env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := runGitCmd.CombinedOutput(); err != nil {
		t.Fatalf("raw git worktree add: %v\n%s", err, out)
	}
	if _, err := os.Stat(setupMarker); err != nil {
		t.Fatalf("ordinary git worktree add did not trigger setup: %v", err)
	}
}

func TestResolveWorktreePath_FlagOverride(t *testing.T) {
	uc := config.LoadUserConfig("/nonexistent/config.yml")
	got := resolveWorktreePath("/custom/path", "/repo/main", "myapp", "feat", uc)
	if got != "/custom/path" {
		t.Errorf("expected /custom/path, got %s", got)
	}
}

func TestResolveWorktreePath_DefaultSiblingLayout(t *testing.T) {
	uc := config.LoadUserConfig("/nonexistent/config.yml")
	got := resolveWorktreePath("", "/repos/main", "myapp", "feat", uc)
	want := filepath.Join("/repos", "myapp-feat")
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

func TestResolveWorktreePath_UserConfigTemplate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"worktree":{"path":"/worktrees/{project}/{branch}"}}`)

	uc := config.LoadUserConfig(cfgPath)
	got := resolveWorktreePath("", "/repos/main", "myapp", "feat", uc)
	if got != "/worktrees/myapp/feat" {
		t.Errorf("expected /worktrees/myapp/feat, got %s", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestNewCmd_StrictFlagRegistered(t *testing.T) {
	f := newCmd.Flags().Lookup("strict")
	if f == nil {
		t.Fatal("expected --strict flag on gtl new")
	}
	if f.DefValue != "false" {
		t.Errorf("expected --strict to default to false, got %s", f.DefValue)
	}
}

func TestNewCmd_DryRunDoesNotMigrateConfigOrCreateGitignore(t *testing.T) {
	mainRepo := t.TempDir()
	runGit(t, mainRepo, "init", "--initial-branch=main")
	marker := filepath.Join(mainRepo, "started")
	configPath := filepath.Join(mainRepo, config.ProjectConfigFile)
	original := "project: preview\ndefault_branch: main\nports_needed: 2\ncommands:\n  start: touch " + marker + "\n"
	writeFile(t, configPath, original)
	runGit(t, mainRepo, "add", config.ProjectConfigFile)
	runGit(t, mainRepo, "commit", "-m", "config")

	t.Setenv("GTL_HOME", t.TempDir())
	chdir(t, mainRepo)
	originalBase, originalPath := newBase, newPath
	originalStart, originalOpen := newStart, newOpen
	originalDryRun, originalForce := newDryRun, newForce
	originalNoSetup, originalStrict := newNoSetup, newStrict
	t.Cleanup(func() {
		newBase, newPath = originalBase, originalPath
		newStart, newOpen = originalStart, originalOpen
		newDryRun, newForce = originalDryRun, originalForce
		newNoSetup, newStrict = originalNoSetup, originalStrict
	})
	newPath = filepath.Join(filepath.Dir(mainRepo), "preview-feature")
	newStart = true
	newDryRun = true

	for _, branch := range []string{"feature", "main"} {
		if err := newCmd.RunE(newCmd, []string{branch}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("dry-run changed config:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(mainRepo, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("dry-run created .gitignore: %v", err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("dry-run created worktree: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("dry-run started the application: %v", err)
	}
}

func TestSetupHydrateSeam_WiredByCmd(t *testing.T) {
	// The cmd package's init must hand setup the source-hydration path;
	// without it, provision.database.auto silently degrades to the empty
	// fallback in every context.
	if setup.HydrateTemplateFromSource == nil {
		t.Fatal("setup.HydrateTemplateFromSource is not wired")
	}
}
