package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooks(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(`
project: test
hooks:
  pre_setup:
    - echo pre
  post_setup:
    - echo post
  pre_release:
    - echo before-release
`), 0o644)
	pc := LoadProjectConfig(dir)
	hooks := pc.Hooks()
	if hooks == nil {
		t.Fatal("expected hooks, got nil")
	}
	if len(hooks["pre_setup"]) != 1 || hooks["pre_setup"][0] != "echo pre" {
		t.Errorf("pre_setup: got %v", hooks["pre_setup"])
	}
	if len(hooks["post_setup"]) != 1 || hooks["post_setup"][0] != "echo post" {
		t.Errorf("post_setup: got %v", hooks["post_setup"])
	}
	if len(hooks["pre_release"]) != 1 {
		t.Errorf("pre_release: got %v", hooks["pre_release"])
	}
	if _, ok := hooks["post_release"]; ok {
		t.Error("post_release should not exist when not configured")
	}
}

func TestHooksEmpty(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: test\n"), 0o644)
	pc := LoadProjectConfig(dir)
	hooks := pc.Hooks()
	if len(hooks) > 0 {
		t.Errorf("expected no hooks, got %v", hooks)
	}
}

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
port_count: 2
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
commands:
  setup:
    - bundle install --quiet
    - yarn install --silent
  start: bin/dev
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
	if pc.StartCommand() != "bin/dev" {
		t.Errorf("expected bin/dev, got %s", pc.StartCommand())
	}
	editor := pc.Editor()
	if editor["title"] != "{project} (:{port})" {
		t.Errorf("unexpected editor title: %s", editor["title"])
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

func TestProjectConfig_MigrateCommands(t *testing.T) {
	dir := t.TempDir()
	yml := `project: myapp
setup_commands:
  - bundle install
  - yarn install
start_command: bin/dev
`
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	cmds := pc.SetupCommands()
	if len(cmds) != 2 || cmds[0] != "bundle install" {
		t.Errorf("expected migrated setup commands, got %v", cmds)
	}
	if pc.StartCommand() != "bin/dev" {
		t.Errorf("expected bin/dev, got %s", pc.StartCommand())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "setup_commands") {
		t.Error("expected setup_commands to be removed from file")
	}
	if strings.Contains(content, "start_command") {
		t.Error("expected start_command to be removed from file")
	}
	if !strings.Contains(content, "commands:") {
		t.Error("expected commands: block in file")
	}
}

func TestProjectConfig_MergeTarget_Empty(t *testing.T) {
	dir := t.TempDir()
	pc := LoadProjectConfig(dir)

	if pc.MergeTarget() != "" {
		t.Errorf("expected empty string, got %s", pc.MergeTarget())
	}
}

// --- env_file format tests ---

func TestEnvFile_StringShorthand(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nenv_file: .env\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)
	if pc.EnvFileTarget() != ".env" {
		t.Errorf("expected .env, got %s", pc.EnvFileTarget())
	}
	if pc.EnvFileSource() != ".env" {
		t.Errorf("expected .env for source, got %s", pc.EnvFileSource())
	}
}

func TestEnvFile_NewMapFormat(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nenv_file:\n  path: .env\n  seed_from: .env.example\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)
	if pc.EnvFileTarget() != ".env" {
		t.Errorf("expected .env, got %s", pc.EnvFileTarget())
	}
	if pc.EnvFileSource() != ".env.example" {
		t.Errorf("expected .env.example, got %s", pc.EnvFileSource())
	}
}

func TestEnvFile_OldMapFormat_SameTargetSource(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nenv_file:\n  target: .env.local\n  source: .env.local\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.EnvFileTarget() != ".env.local" {
		t.Errorf("expected .env.local, got %s", pc.EnvFileTarget())
	}

	// Should have been migrated to string form
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "env_file: .env.local") {
		t.Errorf("expected migration to string form, got:\n%s", content)
	}
	if strings.Contains(content, "target:") {
		t.Error("expected target: key to be removed after migration")
	}
}

func TestEnvFile_OldMapFormat_DifferentTargetSource(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nenv_file:\n  target: .env\n  source: .env.example\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.EnvFileTarget() != ".env" {
		t.Errorf("expected .env, got %s", pc.EnvFileTarget())
	}
	if pc.EnvFileSource() != ".env.example" {
		t.Errorf("expected .env.example, got %s", pc.EnvFileSource())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "path: .env") {
		t.Errorf("expected path: .env after migration, got:\n%s", content)
	}
	if !strings.Contains(content, "seed_from: .env.example") {
		t.Errorf("expected seed_from: .env.example after migration, got:\n%s", content)
	}
	if strings.Contains(content, "target:") || strings.Contains(content, "source:") {
		t.Error("expected old keys removed after migration")
	}
}

func TestEnvFile_Migration_Idempotent(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nenv_file: .env.local\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	_ = LoadProjectConfig(dir)
	_ = LoadProjectConfig(dir) // second load

	data, _ := os.ReadFile(path)
	if string(data) != yml {
		t.Errorf("file should be unchanged, got:\n%s", string(data))
	}
}

func TestEnvFile_Migration_AlreadyNewMap(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nenv_file:\n  path: .env\n  seed_from: .env.example\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	_ = LoadProjectConfig(dir)

	data, _ := os.ReadFile(path)
	if string(data) != yml {
		t.Errorf("file should be unchanged for already-new format, got:\n%s", string(data))
	}
}

func TestProjectConfig_MigrateEditor(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\neditor:\n  vscode_title: '{project} (:{port})'\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.EditorTitle() != "{project} (:{port})" {
		t.Errorf("expected migrated title, got %s", pc.EditorTitle())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "vscode_title") {
		t.Error("expected vscode_title to be rewritten as title")
	}
	if !strings.Contains(content, "title:") {
		t.Errorf("expected title: key in file, got:\n%s", content)
	}
}

func TestProjectConfig_MigrateEditor_NoClobber(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\neditor:\n  vscode_title: 'old'\n  title: 'new'\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.EditorTitle() != "new" {
		t.Errorf("expected existing title preserved, got %s", pc.EditorTitle())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "vscode_title") {
		t.Error("vscode_title should NOT be removed when title already exists")
	}
}

func TestProjectConfig_MigrateEditor_Idempotent(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\neditor:\n  title: '{project} :{port}'\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	_ = LoadProjectConfig(dir)
	_ = LoadProjectConfig(dir)

	data, _ := os.ReadFile(path)
	if string(data) != yml {
		t.Errorf("file should be unchanged, got:\n%s", string(data))
	}
}

func TestProjectConfig_MigratePortCount(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nports_needed: 3\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.PortsNeeded() != 3 {
		t.Errorf("expected 3 after migration, got %d", pc.PortsNeeded())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "ports_needed") {
		t.Error("expected ports_needed to be replaced in file")
	}
	if !strings.Contains(content, "port_count: 3") {
		t.Errorf("expected port_count: 3 in file, got:\n%s", content)
	}
}

func TestProjectConfig_MigratePortCount_NoOverwrite(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\nport_count: 2\nports_needed: 5\n"
	path := filepath.Join(dir, ".treeline.yml")
	_ = os.WriteFile(path, []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.PortsNeeded() != 2 {
		t.Errorf("expected port_count to take precedence, got %d", pc.PortsNeeded())
	}
}

func TestProjectConfig_EditorAccessors(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\neditor:\n  title: '{project} :{port}'\n  color: auto\n  theme: Monokai\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)

	if pc.EditorTitle() != "{project} :{port}" {
		t.Errorf("unexpected title: %s", pc.EditorTitle())
	}
	if pc.EditorColor() != "auto" {
		t.Errorf("unexpected color: %s", pc.EditorColor())
	}
	if pc.EditorTheme() != "Monokai" {
		t.Errorf("unexpected theme: %s", pc.EditorTheme())
	}
}

func TestProjectConfig_EditorAccessors_Empty(t *testing.T) {
	dir := t.TempDir()
	pc := LoadProjectConfig(dir)

	if pc.EditorTitle() != "" {
		t.Errorf("expected empty title, got %s", pc.EditorTitle())
	}
	if pc.EditorColor() != "" {
		t.Errorf("expected empty color, got %s", pc.EditorColor())
	}
	if pc.EditorTheme() != "" {
		t.Errorf("expected empty theme, got %s", pc.EditorTheme())
	}
}

func TestProjectConfig_DatabasePattern_Default(t *testing.T) {
	dir := t.TempDir()
	pc := LoadProjectConfig(dir)
	if pc.DatabasePattern() != "{template}_{worktree}" {
		t.Errorf("expected default pattern, got %s", pc.DatabasePattern())
	}
}

func TestProjectConfig_DatabasePattern_Custom(t *testing.T) {
	dir := t.TempDir()
	yml := "project: myapp\ndatabase:\n  adapter: postgresql\n  pattern: \"{template}--{worktree}\"\n"
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644)

	pc := LoadProjectConfig(dir)
	if pc.DatabasePattern() != "{template}--{worktree}" {
		t.Errorf("expected custom pattern, got %s", pc.DatabasePattern())
	}
}

func TestProjectConfig_HasEnvFileConfig_Present(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\nenv_file: .env\n"), 0o644)

	pc := LoadProjectConfig(dir)
	if !pc.HasEnvFileConfig() {
		t.Error("expected HasEnvFileConfig true when env_file key present")
	}
}

func TestProjectConfig_HasEnvFileConfig_DefaultPresent(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)

	pc := LoadProjectConfig(dir)
	if !pc.HasEnvFileConfig() {
		t.Error("expected HasEnvFileConfig true (default env_file is merged)")
	}
}

func TestProjectConfig_Exists_True(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644)

	pc := LoadProjectConfig(dir)
	if !pc.Exists() {
		t.Error("expected Exists true when .treeline.yml present")
	}
}

func TestProjectConfig_Exists_False(t *testing.T) {
	dir := t.TempDir()
	pc := LoadProjectConfig(dir)
	if pc.Exists() {
		t.Error("expected Exists false when .treeline.yml absent")
	}
}

func TestRewriteEnvFileBlock_Simple(t *testing.T) {
	input := "project: myapp\nenv_file:\n  target: .env.local\n  source: .env.local\nenv:\n  PORT: \"{port}\"\n"
	got := rewriteEnvFileToSimple(input, ".env.local")

	if !strings.Contains(got, "env_file: .env.local") {
		t.Errorf("expected simple form, got:\n%s", got)
	}
	if strings.Contains(got, "target:") || strings.Contains(got, "source:") {
		t.Errorf("expected old keys removed, got:\n%s", got)
	}
	if !strings.Contains(got, "env:") {
		t.Errorf("expected subsequent blocks preserved, got:\n%s", got)
	}
}

func TestRewriteEnvFileBlock_Extended(t *testing.T) {
	input := "project: myapp\nenv_file:\n  target: .env\n  source: .env.example\nenv:\n  PORT: \"{port}\"\n"
	got := rewriteEnvFileToExtended(input, ".env", ".env.example")

	if !strings.Contains(got, "path: .env") {
		t.Errorf("expected path key, got:\n%s", got)
	}
	if !strings.Contains(got, "seed_from: .env.example") {
		t.Errorf("expected seed_from key, got:\n%s", got)
	}
	if strings.Contains(got, "target:") || strings.Contains(got, "source:") {
		t.Errorf("expected old keys removed, got:\n%s", got)
	}
}

func TestProjectAliases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\naliases:\n  redis-ui: 8081\n  mailhog: 8025\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pc := LoadProjectConfig(dir)
	aliases := pc.Aliases()
	if aliases["redis-ui"] != 8081 {
		t.Errorf("expected redis-ui=8081, got %d", aliases["redis-ui"])
	}
	if aliases["mailhog"] != 8025 {
		t.Errorf("expected mailhog=8025, got %d", aliases["mailhog"])
	}
}

func TestProjectAliases_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: myapp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pc := LoadProjectConfig(dir)
	if aliases := pc.Aliases(); len(aliases) != 0 {
		t.Errorf("expected no aliases, got %v", aliases)
	}
}

func TestRewriteEnvFileBlock_PreservesRestOfFile(t *testing.T) {
	input := "project: myapp\n\nenv_file:\n  target: .env.local\n  source: .env.local\n\ndatabase:\n  adapter: postgresql\n"
	got := rewriteEnvFileToSimple(input, ".env.local")

	if !strings.Contains(got, "database:") {
		t.Errorf("expected database block preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "project: myapp") {
		t.Errorf("expected project preserved, got:\n%s", got)
	}
}
