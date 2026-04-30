package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

func TestResolveNewWorktreePath_FlagOverride(t *testing.T) {
	oldPath := newPath
	defer func() { newPath = oldPath }()

	newPath = "/custom/path"
	uc := config.LoadUserConfig("/nonexistent/config.yml")
	got := resolveNewWorktreePath("/repo/main", "myapp", "feat", uc)
	if got != "/custom/path" {
		t.Errorf("expected /custom/path, got %s", got)
	}
}

func TestResolveNewWorktreePath_DefaultSiblingLayout(t *testing.T) {
	oldPath := newPath
	defer func() { newPath = oldPath }()

	newPath = ""
	uc := config.LoadUserConfig("/nonexistent/config.yml")
	got := resolveNewWorktreePath("/repos/main", "myapp", "feat", uc)
	want := filepath.Join("/repos", "myapp-feat")
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

func TestResolveNewWorktreePath_UserConfigTemplate(t *testing.T) {
	oldPath := newPath
	defer func() { newPath = oldPath }()

	newPath = ""

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"worktree":{"path":"/worktrees/{project}/{branch}"}}`)

	uc := config.LoadUserConfig(cfgPath)
	got := resolveNewWorktreePath("/repos/main", "myapp", "feat", uc)
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
