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

func TestSetupHydrateSeam_WiredByCmd(t *testing.T) {
	// The cmd package's init must hand setup the source-hydration path;
	// without it, provision.database.auto silently degrades to the empty
	// fallback in every context.
	if setup.HydrateTemplateFromSource == nil {
		t.Fatal("setup.HydrateTemplateFromSource is not wired")
	}
}
