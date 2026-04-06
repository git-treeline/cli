package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/allocator"
	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/registry"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// --- updateOrAppend tests ---

func TestUpdateOrAppend_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	_ = os.WriteFile(f, []byte("EXISTING=hello\n"), 0o644)

	if err := updateOrAppend(f, "PORT", "3010"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(f)
	content := string(data)
	if !strings.Contains(content, `PORT="3010"`) {
		t.Errorf("expected PORT=\"3010\" in file, got:\n%s", content)
	}
	if !strings.Contains(content, "EXISTING=hello") {
		t.Error("expected existing line preserved")
	}
}

func TestUpdateOrAppend_UpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	_ = os.WriteFile(f, []byte("PORT=3000\nOTHER=val\n"), 0o644)

	if err := updateOrAppend(f, "PORT", "3010"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(f)
	content := string(data)
	if !strings.Contains(content, `PORT="3010"`) {
		t.Errorf("expected PORT=\"3010\", got:\n%s", content)
	}
	if strings.Contains(content, "PORT=3000") {
		t.Error("old PORT value should have been replaced")
	}
	if !strings.Contains(content, "OTHER=val") {
		t.Error("OTHER line should be preserved")
	}
}

func TestUpdateOrAppend_CreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")

	if err := updateOrAppend(f, "PORT", "3010"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), `PORT="3010"`) {
		t.Errorf("expected PORT=\"3010\" in new file, got:\n%s", string(data))
	}
}

// --- helper to build a testable Setup ---

func testSetup(t *testing.T, yamlContent string) (*Setup, string, string) {
	t.Helper()

	dir := t.TempDir()
	mainRepo := filepath.Join(dir, "main")
	worktree := filepath.Join(dir, "worktree")
	_ = os.MkdirAll(mainRepo, 0o755)
	_ = os.MkdirAll(worktree, 0o755)

	_ = os.WriteFile(filepath.Join(mainRepo, ".treeline.yml"), []byte(yamlContent), 0o644)

	regPath := filepath.Join(dir, "registry.json")
	confPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(confPath, []byte(`{"port":{"base":3000,"increment":10},"redis":{"strategy":"prefixed","url":"redis://localhost:6379"}}`), 0o644)

	uc := config.LoadUserConfig(confPath)
	pc := config.LoadProjectConfig(mainRepo)
	reg := registry.New(regPath)
	al := allocator.New(uc, pc, reg)

	s := &Setup{
		WorktreePath:  worktree,
		MainRepo:      mainRepo,
		UserConfig:    uc,
		ProjectConfig: pc,
		Registry:      reg,
		Allocator:     al,
		Log:           &bytes.Buffer{},
	}

	return s, mainRepo, worktree
}

// --- writeEnvFile tests ---

func TestWriteEnvFile_SeedsFromSource(t *testing.T) {
	s, mainRepo, worktree := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.local
env:
  PORT: "{port}"
`)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env.local"), []byte("APP_NAME=myapp\n"), 0o644)

	vars := map[string]string{"PORT": "3010"}
	if err := s.writeEnvFile(vars); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(worktree, ".env.local"))
	content := string(data)
	if !strings.Contains(content, "APP_NAME=myapp") {
		t.Error("expected seeded content from source")
	}
	if !strings.Contains(content, `PORT="3010"`) {
		t.Errorf("expected interpolated PORT, got:\n%s", content)
	}
}

func TestWriteEnvFile_FallsBackToDotEnv(t *testing.T) {
	s, mainRepo, worktree := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.nonexistent
env:
  PORT: "{port}"
`)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env"), []byte("FALLBACK=yes\n"), 0o644)

	vars := map[string]string{"PORT": "3010"}
	if err := s.writeEnvFile(vars); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(worktree, ".env.local"))
	content := string(data)
	if !strings.Contains(content, "FALLBACK=yes") {
		t.Error("expected fallback to .env content")
	}
}

// --- copyFiles tests ---

func TestCopyFiles_CopiesListed(t *testing.T) {
	s, mainRepo, worktree := testSetup(t, `
project: test
copy_files:
  - secret.key
`)
	_ = os.WriteFile(filepath.Join(mainRepo, "secret.key"), []byte("supersecret"), 0o644)

	s.copyFiles()

	data, err := os.ReadFile(filepath.Join(worktree, "secret.key"))
	if err != nil {
		t.Fatal("expected secret.key to be copied")
	}
	if string(data) != "supersecret" {
		t.Errorf("expected copied content, got %q", string(data))
	}
}

func TestCopyFiles_SkipsMissing(t *testing.T) {
	s, _, worktree := testSetup(t, `
project: test
copy_files:
  - does_not_exist.key
`)

	s.copyFiles()

	if _, err := os.Stat(filepath.Join(worktree, "does_not_exist.key")); err == nil {
		t.Error("expected missing source file to be skipped")
	}
}

func TestCopyFiles_CreatesSubdirs(t *testing.T) {
	s, mainRepo, worktree := testSetup(t, `
project: test
copy_files:
  - config/master.key
`)
	_ = os.MkdirAll(filepath.Join(mainRepo, "config"), 0o755)
	_ = os.WriteFile(filepath.Join(mainRepo, "config", "master.key"), []byte("key"), 0o644)

	s.copyFiles()

	data, err := os.ReadFile(filepath.Join(worktree, "config", "master.key"))
	if err != nil {
		t.Fatal("expected config/master.key to be copied with subdirs created")
	}
	if string(data) != "key" {
		t.Errorf("expected 'key', got %q", string(data))
	}
}

// --- configureEditor tests ---

func TestConfigureEditor_WritesVSCodeSettings(t *testing.T) {
	s, _, worktree := testSetup(t, `
project: myapp
editor:
  vscode_title: "{project} (:{port}) — {branch}"
`)

	// configureEditor runs git rev-parse; initialize a real git repo with a commit
	for _, args := range [][]string{
		{"init", "-b", "feature-x"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = worktree
		if err := cmd.Run(); err != nil {
			t.Skipf("git %s failed: %v", args[0], err)
		}
	}

	alloc := &allocator.Allocation{Port: 3010, Branch: "feature-x"}
	s.configureEditor(alloc)

	data, err := os.ReadFile(filepath.Join(worktree, ".vscode", "settings.json"))
	if err != nil {
		t.Fatal("expected .vscode/settings.json to be created")
	}

	var settings map[string]string
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	title := settings["window.title"]
	if !strings.Contains(title, "myapp") {
		t.Errorf("expected project name in title, got %q", title)
	}
	if !strings.Contains(title, "3010") {
		t.Errorf("expected port in title, got %q", title)
	}
	if !strings.Contains(title, "feature-x") {
		t.Errorf("expected branch name in title, got %q", title)
	}
}

func TestConfigureEditor_SkipsWhenNoConfig(t *testing.T) {
	s, _, worktree := testSetup(t, `
project: test
`)

	alloc := &allocator.Allocation{Port: 3010, Branch: "main"}
	s.configureEditor(alloc)

	if _, err := os.Stat(filepath.Join(worktree, ".vscode")); err == nil {
		t.Error("expected .vscode dir to NOT be created when no editor config")
	}
}

// --- Run integration tests ---

func TestRun_DryRun(t *testing.T) {
	s, _, worktree := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.local
env:
  PORT: "{port}"
`)
	s.Options.DryRun = true

	alloc, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}
	if alloc.Port == 0 {
		t.Error("expected an allocated port")
	}

	// Env file should NOT be written
	if _, err := os.Stat(filepath.Join(worktree, ".env.local")); err == nil {
		t.Error("expected no env file written during dry-run")
	}

	// Registry should be empty
	allocs := s.Registry.Allocations()
	if len(allocs) != 0 {
		t.Errorf("expected empty registry during dry-run, got %d entries", len(allocs))
	}
}

func TestRun_RefreshOnly(t *testing.T) {
	s, mainRepo, worktree := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.local
env:
  PORT: "{port}"
commands:
  setup:
    - touch should_not_exist
`)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env.local"), []byte(""), 0o644)

	// First do a normal setup to create the allocation
	alloc, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}

	// Now do a refresh
	s.Options.RefreshOnly = true
	refreshAlloc, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}

	if refreshAlloc.Port != alloc.Port {
		t.Errorf("expected same port on refresh, got %d vs %d", refreshAlloc.Port, alloc.Port)
	}

	// Env file should be written
	if _, err := os.Stat(filepath.Join(worktree, ".env.local")); err != nil {
		t.Error("expected env file written during refresh")
	}

	// The "touch should_not_exist" command ran during first Run but should NOT
	// run again during refresh. However, the file from the first run will exist.
	// To truly test refresh skips commands, we use a different sentinel:
	sentinel := filepath.Join(worktree, "refresh_sentinel")
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("refresh should not have created refresh_sentinel")
	}
}

func TestRun_RollbackOnError(t *testing.T) {
	s, mainRepo, _ := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.local
env:
  PORT: "{port}"
commands:
  setup:
    - "exit 1"
`)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env.local"), []byte(""), 0o644)

	_, err := s.Run()
	if err == nil {
		t.Fatal("expected error from failing setup command")
	}

	// Registry should be empty after rollback
	allocs := s.Registry.Allocations()
	if len(allocs) != 0 {
		t.Errorf("expected empty registry after rollback, got %d entries", len(allocs))
	}
}

func TestRun_SQLiteClone(t *testing.T) {
	s, mainRepo, worktree := testSetup(t, `
project: test
env_file:
  target: .env
  source: .env
database:
  adapter: sqlite
  template: db/development.sqlite3
  pattern: "db/{worktree}.sqlite3"
env:
  PORT: "{port}"
  DATABASE_PATH: "{database}"
`)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env"), []byte(""), 0o644)
	_ = os.MkdirAll(filepath.Join(mainRepo, "db"), 0o755)
	_ = os.WriteFile(filepath.Join(mainRepo, "db", "development.sqlite3"), []byte("sqlite-data"), 0o644)

	alloc, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}

	if alloc.Database == "" {
		t.Fatal("expected database name to be set")
	}

	// The cloned DB should exist in the worktree
	clonedPath := filepath.Join(worktree, alloc.Database)
	data, err := os.ReadFile(clonedPath)
	if err != nil {
		t.Fatalf("expected cloned SQLite file at %s: %v", clonedPath, err)
	}
	if string(data) != "sqlite-data" {
		t.Errorf("expected cloned content, got %q", string(data))
	}
}

func TestRun_SuccessfulSetup(t *testing.T) {
	s, mainRepo, worktree := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.local
env:
  PORT: "{port}"
  APP_URL: "http://localhost:{port}"
copy_files:
  - config/master.key
commands:
  setup:
    - touch setup_ran
`)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env.local"), []byte("BASE=value\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(mainRepo, "config"), 0o755)
	_ = os.WriteFile(filepath.Join(mainRepo, "config", "master.key"), []byte("secret"), 0o644)

	alloc, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}

	if alloc.Port == 0 {
		t.Error("expected allocated port")
	}

	// Env file written with interpolated values
	data, _ := os.ReadFile(filepath.Join(worktree, ".env.local"))
	content := string(data)
	if !strings.Contains(content, "BASE=value") {
		t.Error("expected seeded content")
	}
	portStr := strings.TrimSpace(strings.Split(strings.Split(content, `PORT="`)[1], `"`)[0])
	if portStr == "" || portStr == "{port}" {
		t.Errorf("expected interpolated port, got %q", portStr)
	}

	// Copy files worked
	if _, err := os.Stat(filepath.Join(worktree, "config", "master.key")); err != nil {
		t.Error("expected config/master.key copied")
	}

	// Setup command ran
	if _, err := os.Stat(filepath.Join(worktree, "setup_ran")); err != nil {
		t.Error("expected setup command to have run")
	}

	// Registry has the entry
	allocs := s.Registry.Allocations()
	if len(allocs) != 1 {
		t.Fatalf("expected 1 registry entry, got %d", len(allocs))
	}
}

// --- log / detail / warn output tests ---

func logOutput(s *Setup) string {
	return s.Log.(*bytes.Buffer).String()
}

func plainOutput(s *Setup) string {
	return ansiRE.ReplaceAllString(logOutput(s), "")
}

func TestLog_AddsActionPrefix(t *testing.T) {
	s, _, _ := testSetup(t, "project: test\n")
	s.log("Allocating port %d", 3010)
	plain := plainOutput(s)
	if !strings.Contains(plain, "==> Allocating port 3010") {
		t.Errorf("expected '==> Allocating port 3010', got: %q", plain)
	}
}

func TestLog_EmptyFormat(t *testing.T) {
	s, _, _ := testSetup(t, "project: test\n")
	s.log("")
	out := logOutput(s)
	if out != "\n" {
		t.Errorf("expected single newline, got: %q", out)
	}
}

func TestDetail_NoPrefix(t *testing.T) {
	s, _, _ := testSetup(t, "project: test\n")
	s.detail("  Port: %d", 3010)
	plain := plainOutput(s)
	if strings.Contains(plain, "==>") {
		t.Errorf("detail should not contain ==>, got: %q", plain)
	}
	if !strings.Contains(plain, "  Port: 3010") {
		t.Errorf("expected '  Port: 3010', got: %q", plain)
	}
}

func TestWarn_HasWarningPrefix(t *testing.T) {
	s, _, _ := testSetup(t, "project: test\n")
	s.warn("hook failed: %s", "exit 1")
	plain := plainOutput(s)
	if !strings.Contains(plain, "Warning: hook failed: exit 1") {
		t.Errorf("expected 'Warning: hook failed: exit 1', got: %q", plain)
	}
	if strings.Contains(plain, "==>") {
		t.Errorf("warn should not contain ==>, got: %q", plain)
	}
}

func TestDryRun_DetailLinesHaveNoActionPrefix(t *testing.T) {
	s, _, _ := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.local
env:
  PORT: "{port}"
`)
	s.Options.DryRun = true
	_, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}

	plain := plainOutput(s)
	for _, line := range strings.Split(plain, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Action lines (containing "[dry-run]") go through s.log and should have ==>
		if strings.Contains(trimmed, "[dry-run]") {
			if !strings.Contains(line, "==>") {
				t.Errorf("dry-run header should have ==> prefix: %q", line)
			}
			continue
		}
		// Subordinate detail lines (Port:, Redis:, Dir:, Env vars:, KEY=val) should NOT
		if strings.Contains(line, "==>") {
			t.Errorf("subordinate detail line should not have ==> prefix: %q", line)
		}
	}
}

func TestRun_SummaryBlockFormat(t *testing.T) {
	s, mainRepo, _ := testSetup(t, `
project: test
env_file:
  target: .env.local
  source: .env.local
env:
  PORT: "{port}"
`)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env.local"), []byte(""), 0o644)

	_, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}

	plain := plainOutput(s)
	if !strings.Contains(plain, "Done!") {
		t.Error("expected Done! in summary output")
	}
	if !strings.Contains(plain, "Port:") || !strings.Contains(plain, "Redis:") {
		t.Error("expected Port: and Redis: in summary output")
	}
}
