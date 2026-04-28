package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/config"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestWarnServeNotInstalled_MentionsInstallCommands(t *testing.T) {
	gtlHome := filepath.Join(t.TempDir(), "gtl-home")
	t.Setenv("GTL_HOME", gtlHome)
	t.Setenv("GTL_HEADLESS", "")
	oldHealth := routerHealthChecker
	routerHealthChecker = func() []string { return []string{"CA trust"} }
	t.Cleanup(func() { routerHealthChecker = oldHealth })
	if err := os.MkdirAll(gtlHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gtlHome, "config.json"), []byte(`{"router":{"mode":"prompt"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, warnServeNotInstalled)
	if !strings.Contains(out, "gtl install") {
		t.Errorf("expected gtl install in warning, got:\n%s", out)
	}
	if !strings.Contains(out, "gtl serve install") {
		t.Errorf("expected gtl serve install in warning, got:\n%s", out)
	}
}

func TestWarnServeNotInstalled_HeadlessSilent(t *testing.T) {
	gtlHome := filepath.Join(t.TempDir(), "gtl-home")
	t.Setenv("GTL_HOME", gtlHome)
	t.Setenv("GTL_HEADLESS", "1")
	oldHealth := routerHealthChecker
	routerHealthChecker = func() []string { return []string{"CA trust"} }
	t.Cleanup(func() { routerHealthChecker = oldHealth })
	if err := os.MkdirAll(gtlHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gtlHome, "config.json"), []byte(`{"router":{"mode":"prompt"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, warnServeNotInstalled)
	if out != "" {
		t.Errorf("expected no warning in headless mode, got:\n%s", out)
	}
}

func TestWarnServeNotInstalled_DisabledByUserConfig(t *testing.T) {
	gtlHome := filepath.Join(t.TempDir(), "gtl-home")
	t.Setenv("GTL_HOME", gtlHome)
	t.Setenv("GTL_HEADLESS", "")
	oldHealth := routerHealthChecker
	routerHealthChecker = func() []string { return []string{"CA trust"} }
	t.Cleanup(func() { routerHealthChecker = oldHealth })
	if err := os.MkdirAll(gtlHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gtlHome, "config.json"), []byte(`{"warnings":{"router":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, warnServeNotInstalled)
	if out != "" {
		t.Errorf("expected warning suppressed by user config, got:\n%s", out)
	}
}
func TestPrintLocalAndRouter_PrintsLocalButNotTunnel(t *testing.T) {
	uc := loadTestUserConfig(t)
	uc.Set("tunnel.default", "personal")
	uc.Set("tunnel.tunnels.personal.domain", "example.com")

	out := captureStdout(t, func() {
		printLocalAndRouter(uc, "myapp", "feature-x", 3010)
	})
	out = ansiEscapeRE.ReplaceAllString(out, "")
	if !strings.Contains(out, "http://localhost:3010") {
		t.Errorf("expected localhost URL, got:\n%s", out)
	}
	if strings.Contains(out, "Tunnel:") || strings.Contains(out, "gtl tunnel") || strings.Contains(out, "example.com") {
		t.Errorf("expected no tunnel hint, got:\n%s", out)
	}
}

func TestPrintRouterURL_DisabledRouterModeSilent(t *testing.T) {
	uc := loadTestUserConfig(t)
	uc.SetRouterMode(config.RouterModeDisabled)

	out := captureStdout(t, func() {
		printRouterURL(uc, "myapp", "feature-x")
	})
	out = ansiEscapeRE.ReplaceAllString(out, "")
	if strings.Contains(out, "Router:") {
		t.Errorf("expected no router output when router mode is disabled, got:\n%s", out)
	}
}
func TestSortedRouteKeys(t *testing.T) {
	routes := map[string]int{
		"myapp-feature": 3010,
		"myapp-main":    3000,
		"other-dev":     3020,
		"api-staging":   4000,
	}
	keys := sortedRouteKeys(routes)
	if len(keys) != 4 {
		t.Fatalf("expected 4 keys, got %d", len(keys))
	}
	expected := []string{"api-staging", "myapp-feature", "myapp-main", "other-dev"}
	for i, want := range expected {
		if keys[i] != want {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], want)
		}
	}
}

func TestSortedRouteKeys_Empty(t *testing.T) {
	keys := sortedRouteKeys(map[string]int{})
	if len(keys) != 0 {
		t.Errorf("expected empty, got %v", keys)
	}
}

func TestSortedRouteKeys_Single(t *testing.T) {
	keys := sortedRouteKeys(map[string]int{"only": 3000})
	if len(keys) != 1 || keys[0] != "only" {
		t.Errorf("expected [only], got %v", keys)
	}
}

func TestIsInWorktree_SamePath(t *testing.T) {
	dir := t.TempDir()
	if isInWorktree(dir, dir) {
		t.Error("same path should not be detected as worktree")
	}
}

func TestIsInWorktree_DifferentPath(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	if !isInWorktree(a, b) {
		t.Error("different paths should be detected as worktree")
	}
}

func TestIsInWorktree_SymlinkResolution(t *testing.T) {
	real := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	if isInWorktree(link, real) {
		t.Error("symlinked path pointing to same dir should not be detected as worktree")
	}
}

func TestIsInWorktree_NonexistentPathFallback(t *testing.T) {
	a := "/nonexistent/path/a"
	b := "/nonexistent/path/b"
	if !isInWorktree(a, b) {
		t.Error("different nonexistent paths should fall back to clean comparison")
	}
	if isInWorktree(a, a) {
		t.Error("same nonexistent path should not be detected as worktree")
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
}

func TestEnsureGitignored_OutsideRepo_Noop(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	sibling := filepath.Join(filepath.Dir(repo), "sibling-wt")

	if err := ensureGitignored(repo, sibling); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); err == nil {
		t.Error(".gitignore should not be created for sibling paths")
	}
}

func TestEnsureGitignored_InsideRepo_AddsPattern(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wtPath := filepath.Join(repo, ".worktrees", "feat-x")

	if err := ensureGitignored(repo, wtPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/.worktrees/") {
		t.Errorf("expected /.worktrees/ in .gitignore, got: %s", data)
	}
}

func TestEnsureGitignored_AlreadyIgnored_NoDoubleAdd(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("/.worktrees/\n"), 0o644)
	wtPath := filepath.Join(repo, ".worktrees", "feat-y")

	if err := ensureGitignored(repo, wtPath); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if strings.Count(string(data), "/.worktrees/") != 1 {
		t.Errorf("expected exactly one /.worktrees/ entry, got: %s", data)
	}
}

func TestEnsureGitignored_AppendsToExistingGitignore(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o644)
	wtPath := filepath.Join(repo, ".worktrees", "feat-z")

	if err := ensureGitignored(repo, wtPath); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	content := string(data)
	if !strings.Contains(content, "node_modules/") {
		t.Error("existing entries should be preserved")
	}
	if !strings.Contains(content, "/.worktrees/") {
		t.Error("expected /.worktrees/ to be appended")
	}
}
