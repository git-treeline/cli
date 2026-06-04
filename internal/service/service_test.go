package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeneratePlist(t *testing.T) {
	content, err := GeneratePlist("/usr/local/bin/gtl")
	if err != nil {
		t.Fatalf("GeneratePlist failed: %v", err)
	}

	checks := []string{
		"<string>dev.treeline.router</string>",
		"<string>/usr/local/bin/gtl</string>",
		"<string>serve</string>",
		"<string>run</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"router.log",
		"router.err",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("plist missing %q", check)
		}
	}
}

func TestResolveStablePath_NonCellar(t *testing.T) {
	got := resolveStablePath("/usr/local/bin/gtl")
	if got != "/usr/local/bin/gtl" {
		t.Errorf("expected unchanged path, got %q", got)
	}
}

func TestResolveStablePath_CellarWithSymlink(t *testing.T) {
	// Simulate: <tmp>/Cellar/git-treeline/0.38.0/bin/gtl → real binary
	//           <tmp>/bin/gtl → symlink to ../Cellar/git-treeline/0.38.0/bin/gtl
	root := t.TempDir()

	cellarBin := filepath.Join(root, "Cellar", "git-treeline", "0.38.0", "bin")
	_ = os.MkdirAll(cellarBin, 0o755)
	realBinary := filepath.Join(cellarBin, "gtl")
	_ = os.WriteFile(realBinary, []byte("binary"), 0o755)

	stableBin := filepath.Join(root, "bin")
	_ = os.MkdirAll(stableBin, 0o755)
	stableLink := filepath.Join(stableBin, "gtl")
	_ = os.Symlink(filepath.Join("..", "Cellar", "git-treeline", "0.38.0", "bin", "gtl"), stableLink)

	got := resolveStablePath(realBinary)
	if got != stableLink {
		t.Errorf("expected stable symlink %q, got %q", stableLink, got)
	}
}

func TestResolveStablePath_CellarNoSymlink(t *testing.T) {
	root := t.TempDir()

	cellarBin := filepath.Join(root, "Cellar", "git-treeline", "0.38.0", "bin")
	_ = os.MkdirAll(cellarBin, 0o755)
	realBinary := filepath.Join(cellarBin, "gtl")
	_ = os.WriteFile(realBinary, []byte("binary"), 0o755)

	// No symlink in <root>/bin — should return original
	got := resolveStablePath(realBinary)
	if got != realBinary {
		t.Errorf("expected original path %q, got %q", realBinary, got)
	}
}

func TestResolveStablePath_GoInstall(t *testing.T) {
	// go install puts binary in ~/go/bin/gtl — no Cellar, should be unchanged
	got := resolveStablePath("/Users/someone/go/bin/gtl")
	if got != "/Users/someone/go/bin/gtl" {
		t.Errorf("expected unchanged path, got %q", got)
	}
}

func TestGenerateUnit(t *testing.T) {
	content, err := GenerateUnit("/usr/local/bin/gtl")
	if err != nil {
		t.Fatalf("GenerateUnit failed: %v", err)
	}

	checks := []string{
		"ExecStart=/usr/local/bin/gtl serve run",
		"Restart=always",
		"WantedBy=default.target",
		"git-treeline subdomain router",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("unit missing %q", check)
		}
	}
}

// fakeExitErr runs "sh -c exit N" and returns the *exec.ExitError, which is
// what launchctlExitCode inspects.
func fakeExitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("expected non-zero exit for code %d", code)
	}
	return err
}

// setupReloadTest creates a temp plist file, redirects plistPathFn and
// sleepFn, and writes GTL_HOME so RouterVersionFile() lands in a temp dir.
// Returns a cleanup func that restores all overrides.
func setupReloadTest(t *testing.T) (versionFile string, cleanup func()) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("GTL_HOME", tmp)

	plist := filepath.Join(tmp, "router.plist")
	if err := os.WriteFile(plist, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	origPlistFn := plistPathFn
	origRunCmd := runCmd
	origSleep := sleepFn

	plistPathFn = func() string { return plist }
	sleepFn = func(time.Duration) {}

	versionFile = RouterVersionFile()

	return versionFile, func() {
		plistPathFn = origPlistFn
		runCmd = origRunCmd
		sleepFn = origSleep
	}
}

func TestReloadLaunchAgent_BootstrapRetry_SucceedsAfterExit5(t *testing.T) {
	versionFile, cleanup := setupReloadTest(t)
	defer cleanup()

	bootstrapCalls := 0
	runCmd = func(name string, args ...string) error {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			if bootstrapCalls < 3 {
				return fakeExitErr(t, 5)
			}
			// Simulate the router writing its version file on successful start.
			_ = os.WriteFile(versionFile, []byte("v1"), 0o644)
			return nil
		}
		return nil
	}

	if err := reloadLaunchAgent(2 * time.Second); err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if bootstrapCalls != 3 {
		t.Errorf("expected 3 bootstrap calls, got %d", bootstrapCalls)
	}
}

func TestReloadLaunchAgent_BootstrapRetry_StopsAtMaxRetries(t *testing.T) {
	_, cleanup := setupReloadTest(t)
	defer cleanup()

	bootstrapCalls := 0
	runCmd = func(name string, args ...string) error {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			return fakeExitErr(t, 5)
		}
		return nil
	}

	err := reloadLaunchAgent(100 * time.Millisecond)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if bootstrapCalls != 3 {
		t.Errorf("expected exactly 3 bootstrap attempts (maxRetries), got %d", bootstrapCalls)
	}
}

func TestReloadLaunchAgent_BootstrapRetry_NoRetryOnNonFiveError(t *testing.T) {
	_, cleanup := setupReloadTest(t)
	defer cleanup()

	bootstrapCalls := 0
	runCmd = func(name string, args ...string) error {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			return fakeExitErr(t, 1) // non-5 failure
		}
		return nil
	}

	err := reloadLaunchAgent(100 * time.Millisecond)
	if err == nil {
		t.Fatal("expected error")
	}
	if bootstrapCalls != 1 {
		t.Errorf("expected exactly 1 bootstrap attempt for non-5 error, got %d", bootstrapCalls)
	}
}
