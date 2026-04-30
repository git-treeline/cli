package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/detect"
	setupPkg "github.com/git-treeline/git-treeline/internal/setup"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = orig
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func loadTestUserConfig(t *testing.T) *config.UserConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"port":{"base":41000,"increment":10},"redis":{"strategy":"prefixed","url":"redis://localhost:6379"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.LoadUserConfig(path)
}

func TestInstallCmd_MissingProjectConfig(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	t.Chdir(dir)
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "1")
	oldRegistryPath := setupPkg.RegistryPath
	setupPkg.RegistryPath = filepath.Join(t.TempDir(), "registry.json")
	t.Cleanup(func() { setupPkg.RegistryPath = oldRegistryPath })

	err := installCmd.RunE(installCmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".treeline.yml")); err != nil {
		t.Fatalf("expected install to create .treeline.yml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "post-checkout")); err != nil {
		t.Fatalf("expected install to install post-checkout hook: %v", err)
	}
}

func TestInstallCmd_MissingProjectConfigFromSubdirInitializesRepoRoot(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	subdir := filepath.Join(dir, "apps", "web")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "1")
	oldRegistryPath := setupPkg.RegistryPath
	setupPkg.RegistryPath = filepath.Join(t.TempDir(), "registry.json")
	t.Cleanup(func() { setupPkg.RegistryPath = oldRegistryPath })

	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".treeline.yml")); err != nil {
		t.Fatalf("expected install to create .treeline.yml at repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(subdir, ".treeline.yml")); !os.IsNotExist(err) {
		t.Fatalf("expected no nested .treeline.yml in subdir, got err=%v", err)
	}
}

func TestRunInitForNew_RailsUsesDetectedTemplateDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "honolulu-v1")
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("gem 'rails'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "application.rb"), []byte("module HonoluluV1\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "database.yml"), []byte(`default: &default
  adapter: postgresql

development:
  <<: *default
  database: honolulu_v1_development
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInitForNew(dir, detect.Detect(dir)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".treeline.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "project: honolulu_v1") {
		t.Errorf("expected project from Rails app name, got:\n%s", content)
	}
	if !strings.Contains(content, "template: honolulu_v1_development") {
		t.Errorf("expected Rails template DB from database.yml, got:\n%s", content)
	}
	if strings.Contains(content, "template: honolulu-v1_development") {
		t.Errorf("expected no hyphenated PostgreSQL template DB, got:\n%s", content)
	}
}

func TestDefaultTemplateDB_SanitizesProjectForPostgreSQL(t *testing.T) {
	if got := defaultTemplateDB("honolulu-v1", &detect.Result{Framework: "rails"}); got != "honolulu_v1_development" {
		t.Errorf("expected sanitized Rails template DB, got %q", got)
	}
	if got := defaultTemplateDB("123-api", &detect.Result{Framework: "phoenix"}); got != "db_123_api_dev" {
		t.Errorf("expected sanitized Phoenix template DB, got %q", got)
	}
}

func TestInstallCmd_HappyPathInstallsHookAndAllocates(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	t.Chdir(dir)
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "1")

	oldRegistryPath := setupPkg.RegistryPath
	setupPkg.RegistryPath = filepath.Join(t.TempDir(), "registry.json")
	t.Cleanup(func() { setupPkg.RegistryPath = oldRegistryPath })

	yml := "project: installtest\nport_count: 1\nenv_file: .env.test\nenv:\n  PORT: \"{port}\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "post-checkout")); err != nil {
		t.Fatalf("expected post-checkout hook installed: %v", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolvedDir = dir
	}
	envData, err := os.ReadFile(filepath.Join(resolvedDir, ".env.test"))
	if err != nil {
		t.Fatalf("expected env file written: %v", err)
	}
	if !strings.Contains(string(envData), "PORT=") {
		t.Errorf("expected PORT in env file, got:\n%s", string(envData))
	}

	uc := config.LoadUserConfig("")
	if got := uc.RouterDomain(); got != "prt.dev" {
		t.Errorf("expected install-created config to default router domain to prt.dev, got %q", got)
	}
	if !uc.HasExplicitRouterDomain() {
		t.Error("expected install-created config to persist router.domain explicitly")
	}
}

func TestMaybeOfferServeInstall_HeadlessSkipsPrompt(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "1")

	uc := loadTestUserConfig(t)
	out := captureStdout(t, func() {
		if err := maybeOfferServeInstall(uc); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "HTTPS router setup:") {
		t.Errorf("expected no HTTPS prompt in headless mode, got:\n%s", out)
	}
}

func TestMaybeOfferServeInstall_HeadlessEnabledModeWarnsAndContinues(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "1")

	uc := loadTestUserConfig(t)
	uc.SetRouterMode(config.RouterModeEnabled)
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	oldHealth := routerHealthChecker
	routerHealthChecker = func() []string { return []string{"router service"} }
	t.Cleanup(func() { routerHealthChecker = oldHealth })

	out := captureStderr(t, func() {
		if err := maybeOfferServeInstall(uc); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "skipping router setup in headless mode") {
		t.Errorf("expected headless warning, got:\n%s", out)
	}
}

func TestMaybeOfferServeInstall_DisabledModeSkipsPrompt(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "")

	uc := loadTestUserConfig(t)
	uc.SetRouterMode(config.RouterModeDisabled)
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	oldSelect := installSelect
	installSelect = func(message string, options []string, defaultIdx int, reader io.Reader) int {
		t.Fatalf("unexpected prompt: %s", message)
		return 0
	}
	t.Cleanup(func() { installSelect = oldSelect })

	if err := maybeOfferServeInstall(uc); err != nil {
		t.Fatal(err)
	}
}

func TestMaybeOfferServeInstall_DeclineDisablesFutureOffers(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "")

	uc := loadTestUserConfig(t)

	oldSelect := installSelect
	oldHealth := routerHealthChecker
	installSelect = func(message string, options []string, defaultIdx int, reader io.Reader) int {
		return 2
	}
	routerHealthChecker = func() []string { return []string{"CA trust"} }
	t.Cleanup(func() {
		installSelect = oldSelect
		routerHealthChecker = oldHealth
	})

	if err := maybeOfferServeInstall(uc); err != nil {
		t.Fatal(err)
	}

	reloaded := config.LoadUserConfig(uc.Path)
	if got := reloaded.RouterMode(); got != config.RouterModeDisabled {
		t.Fatalf("expected router mode disabled, got %q", got)
	}
}

func TestMaybeOfferServeInstall_DeclineKeepsPromptMode(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "")

	uc := loadTestUserConfig(t)

	oldSelect := installSelect
	oldHealth := routerHealthChecker
	installSelect = func(message string, options []string, defaultIdx int, reader io.Reader) int {
		return 1
	}
	routerHealthChecker = func() []string { return []string{"CA trust"} }
	t.Cleanup(func() {
		installSelect = oldSelect
		routerHealthChecker = oldHealth
	})

	if err := maybeOfferServeInstall(uc); err != nil {
		t.Fatal(err)
	}

	reloaded := config.LoadUserConfig(uc.Path)
	if got := reloaded.RouterMode(); got != config.RouterModePrompt {
		t.Fatalf("expected router mode prompt after simple decline, got %q", got)
	}
}

func TestMaybeOfferServeInstall_HealthyAndStaleTriggersRefresh(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "")

	uc := loadTestUserConfig(t)
	uc.SetRouterMode(config.RouterModeEnabled)
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	oldHealth := routerHealthChecker
	oldStale := routerStaleChecker
	oldRefresh := routerRefresher
	routerHealthChecker = func() []string { return nil }
	routerStaleChecker = func() string { return "running 0.39.0 but CLI is 0.39.1" }
	refreshed := false
	routerRefresher = func() error { refreshed = true; return nil }
	t.Cleanup(func() {
		routerHealthChecker = oldHealth
		routerStaleChecker = oldStale
		routerRefresher = oldRefresh
	})

	out := captureStdout(t, func() {
		if err := maybeOfferServeInstall(uc); err != nil {
			t.Fatal(err)
		}
	})
	if !refreshed {
		t.Fatal("expected router refresh to run, but it did not")
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("expected stale-router message in output, got:\n%s", out)
	}
	if strings.Contains(out, "already running") {
		t.Errorf("did not expect 'already running' when stale, got:\n%s", out)
	}
}

func TestMaybeOfferServeInstall_HealthyAndFreshSkipsRefresh(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "")

	uc := loadTestUserConfig(t)
	uc.SetRouterMode(config.RouterModeEnabled)
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	oldHealth := routerHealthChecker
	oldStale := routerStaleChecker
	oldRefresh := routerRefresher
	routerHealthChecker = func() []string { return nil }
	routerStaleChecker = func() string { return "" }
	routerRefresher = func() error { t.Fatal("unexpected refresh call"); return nil }
	t.Cleanup(func() {
		routerHealthChecker = oldHealth
		routerStaleChecker = oldStale
		routerRefresher = oldRefresh
	})

	out := captureStdout(t, func() {
		if err := maybeOfferServeInstall(uc); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "already running") {
		t.Errorf("expected 'already running' message, got:\n%s", out)
	}
}

func TestMaybeOfferServeInstall_DisabledSkipsStaleCheck(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	t.Setenv("GTL_HEADLESS", "")

	uc := loadTestUserConfig(t)
	uc.SetRouterMode(config.RouterModeDisabled)
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	oldHealth := routerHealthChecker
	oldStale := routerStaleChecker
	oldRefresh := routerRefresher
	routerHealthChecker = func() []string { return nil }
	routerStaleChecker = func() string { t.Fatal("stale check should be skipped when disabled"); return "" }
	routerRefresher = func() error { t.Fatal("refresh should not run when disabled"); return nil }
	t.Cleanup(func() {
		routerHealthChecker = oldHealth
		routerStaleChecker = oldStale
		routerRefresher = oldRefresh
	})

	if err := maybeOfferServeInstall(uc); err != nil {
		t.Fatal(err)
	}
}

func TestRouterInstallIssuesWith_PortForwardingIsOptional(t *testing.T) {
	tests := []struct {
		name           string
		caInstalled    bool
		serviceRunning bool
		want           []string
	}{
		{
			name:           "healthy",
			caInstalled:    true,
			serviceRunning: true,
			want:           nil,
		},
		{
			name:           "missing ca trust",
			caInstalled:    false,
			serviceRunning: true,
			want:           []string{"CA trust"},
		},
		{
			name:           "missing router service",
			caInstalled:    true,
			serviceRunning: false,
			want:           []string{"router service"},
		},
		{
			name:           "missing both required pieces",
			caInstalled:    false,
			serviceRunning: false,
			want:           []string{"CA trust", "router service"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := routerInstallIssuesWith(tt.caInstalled, tt.serviceRunning)
			if strings.Join(issues, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("routerInstallIssuesWith(%t, %t) = %v, want %v", tt.caInstalled, tt.serviceRunning, issues, tt.want)
			}
		})
	}
}
