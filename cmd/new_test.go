package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/setup"
)

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
