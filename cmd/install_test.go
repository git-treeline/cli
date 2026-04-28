package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/config"
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
