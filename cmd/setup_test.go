package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

func TestSetupCmd_DryRunDoesNotMigrateConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.ProjectConfigFile)
	original := "project: preview\ndefault_branch: main\nports_needed: 2\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GTL_HOME", t.TempDir())
	t.Setenv("GTL_HEADLESS", "1")
	originalDryRun, originalMainRepo := setupDryRun, setupMainRepo
	t.Cleanup(func() {
		setupDryRun, setupMainRepo = originalDryRun, originalMainRepo
	})
	setupDryRun = true
	setupMainRepo = dir

	if err := setupCmd.RunE(setupCmd, []string{dir}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("dry-run changed config:\n%s", after)
	}
}
