package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
)

func makeRenameEnv(t *testing.T) (mainRepo string, reg *registry.Registry) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GTL_HOME", filepath.Join(dir, "gtl-home"))
	mainRepo = filepath.Join(dir, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	// Use registry.New("") so it resolves via GTL_HOME — same path the command uses.
	reg = registry.New("")
	return mainRepo, reg
}

func writeRenameConfig(t *testing.T, dir, projectName string) {
	t.Helper()
	content := "project: " + projectName + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenameCmd_InvalidName(t *testing.T) {
	mainRepo, _ := makeRenameEnv(t)
	writeRenameConfig(t, mainRepo, "myapp")
	t.Chdir(mainRepo)
	renameYes = true
	t.Cleanup(func() { renameYes = false })

	err := renameCmd.RunE(renameCmd, []string{"my-app"})
	if err == nil {
		t.Fatal("expected error for invalid project name with dashes")
	}
	if !strings.Contains(err.Error(), "invalid project name") {
		t.Errorf("expected 'invalid project name' error, got: %v", err)
	}
}

func TestRenameCmd_SameNameIsNoop(t *testing.T) {
	mainRepo, _ := makeRenameEnv(t)
	writeRenameConfig(t, mainRepo, "myapp")
	t.Chdir(mainRepo)
	renameYes = true
	t.Cleanup(func() { renameYes = false })

	out := captureStdout(t, func() {
		if err := renameCmd.RunE(renameCmd, []string{"myapp"}); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "already named") {
		t.Errorf("expected 'already named' message, got: %q", out)
	}

	// .treeline.yml should be unchanged
	data, _ := os.ReadFile(filepath.Join(mainRepo, ".treeline.yml"))
	if !strings.Contains(string(data), "project: myapp") {
		t.Errorf("expected project name unchanged, got: %s", data)
	}
}

func TestRenameCmd_MissingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GTL_HOME", filepath.Join(dir, "gtl-home"))
	t.Chdir(dir)
	renameYes = true
	t.Cleanup(func() { renameYes = false })

	err := renameCmd.RunE(renameCmd, []string{"newname"})
	if err == nil {
		t.Fatal("expected error when .treeline.yml is missing")
	}
}

func TestRenameCmd_UpdatesProjectConfig(t *testing.T) {
	mainRepo, _ := makeRenameEnv(t)
	writeRenameConfig(t, mainRepo, "old_name")
	t.Chdir(mainRepo)
	renameYes = true
	t.Cleanup(func() { renameYes = false })

	if err := renameCmd.RunE(renameCmd, []string{"new_name"}); err != nil {
		t.Fatal(err)
	}

	pc := config.LoadProjectConfig(mainRepo)
	if pc.Project() != "new_name" {
		t.Errorf("expected project name 'new_name', got %q", pc.Project())
	}
}

func TestRenameCmd_ListsAffectedWorktrees(t *testing.T) {
	mainRepo, reg := makeRenameEnv(t)
	writeRenameConfig(t, mainRepo, "old_name")
	t.Chdir(mainRepo)
	renameYes = true
	t.Cleanup(func() { renameYes = false })

	// Register a fake worktree under the old project name
	_ = reg.Allocate(registry.Allocation{
		"project":  "old_name",
		"worktree": "/fake/worktree/path",
		"port":     3010,
	})

	out := captureStdout(t, func() {
		if err := renameCmd.RunE(renameCmd, []string{"new_name"}); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "/fake/worktree/path") {
		t.Errorf("expected affected worktree path in output, got: %q", out)
	}
	if !strings.Contains(out, "gtl setup") {
		t.Errorf("expected 'gtl setup' instructions in output, got: %q", out)
	}
}

func TestRenameCmd_DoesNotTouchRegistry(t *testing.T) {
	mainRepo, reg := makeRenameEnv(t)
	writeRenameConfig(t, mainRepo, "old_name")
	t.Chdir(mainRepo)
	renameYes = true
	t.Cleanup(func() { renameYes = false })

	_ = reg.Allocate(registry.Allocation{
		"project":  "old_name",
		"worktree": "/fake/worktree/path",
		"port":     3010,
		"database": "old_name_development_feat",
	})

	if err := renameCmd.RunE(renameCmd, []string{"new_name"}); err != nil {
		t.Fatal(err)
	}

	// Registry entry should still be there — rename doesn't touch it.
	// Each worktree cleans up its own stale entry on next gtl setup.
	entries := reg.FindByProject("old_name")
	if len(entries) != 1 {
		t.Errorf("expected registry entry to remain untouched, got %d entries", len(entries))
	}
}
