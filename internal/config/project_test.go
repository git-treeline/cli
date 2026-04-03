package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	pc := LoadProjectConfig(dir)

	if pc.PortsNeeded() != 1 {
		t.Errorf("expected 1, got %d", pc.PortsNeeded())
	}
	if pc.DatabaseAdapter() != "postgresql" {
		t.Errorf("expected postgresql, got %s", pc.DatabaseAdapter())
	}
	if pc.EnvFileTarget() != ".env.local" {
		t.Errorf("expected .env.local, got %s", pc.EnvFileTarget())
	}
	if pc.Project() != filepath.Base(dir) {
		t.Errorf("expected %s, got %s", filepath.Base(dir), pc.Project())
	}
}

func TestProjectConfig_ParsesYAML(t *testing.T) {
	dir := t.TempDir()
	yml := `
project: salt
ports_needed: 2
database:
  adapter: postgresql
  template: salt_development
  pattern: "{template}_{worktree}"
copy_files:
  - config/master.key
env:
  PORT: "{port}"
  DATABASE_NAME: "{database}"
  ESBUILD_PORT: "{port_2}"
setup_commands:
  - bundle install --quiet
  - yarn install --silent
editor:
  vscode_title: '{project} (:{port})'
`
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := LoadProjectConfig(dir)

	if pc.Project() != "salt" {
		t.Errorf("expected salt, got %s", pc.Project())
	}
	if pc.PortsNeeded() != 2 {
		t.Errorf("expected 2, got %d", pc.PortsNeeded())
	}
	if pc.DatabaseTemplate() != "salt_development" {
		t.Errorf("expected salt_development, got %s", pc.DatabaseTemplate())
	}
	if len(pc.CopyFiles()) != 1 || pc.CopyFiles()[0] != "config/master.key" {
		t.Errorf("unexpected copy_files: %v", pc.CopyFiles())
	}
	env := pc.EnvTemplate()
	if env["ESBUILD_PORT"] != "{port_2}" {
		t.Errorf("expected {port_2}, got %s", env["ESBUILD_PORT"])
	}
	cmds := pc.SetupCommands()
	if len(cmds) != 2 {
		t.Errorf("expected 2 setup commands, got %d", len(cmds))
	}
	editor := pc.Editor()
	if editor["vscode_title"] != "{project} (:{port})" {
		t.Errorf("unexpected editor title: %s", editor["vscode_title"])
	}
}

func TestProjectConfig_MergeTarget(t *testing.T) {
	dir := t.TempDir()
	yml := `project: myapp
merge_target: develop
`
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)
	pc := LoadProjectConfig(dir)

	if pc.MergeTarget() != "develop" {
		t.Errorf("expected develop, got %s", pc.MergeTarget())
	}
}

func TestProjectConfig_MigrateDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\ndefault_branch: staging\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.MergeTarget() != "staging" {
		t.Errorf("expected staging after migration, got %s", pc.MergeTarget())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "default_branch") {
		t.Error("expected default_branch to be replaced in file")
	}
	if !strings.Contains(content, "merge_target: staging") {
		t.Errorf("expected merge_target: staging in file, got:\n%s", content)
	}
}

func TestProjectConfig_MigrateDefaultBranch_NoClobber(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\ndefault_branch: staging\nmerge_target: production\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)
	if pc.MergeTarget() != "production" {
		t.Errorf("expected existing merge_target to be preserved, got %s", pc.MergeTarget())
	}
}

func TestProjectConfig_MergeTarget_Empty(t *testing.T) {
	dir := t.TempDir()
	pc := LoadProjectConfig(dir)

	if pc.MergeTarget() != "" {
		t.Errorf("expected empty string, got %s", pc.MergeTarget())
	}
}
