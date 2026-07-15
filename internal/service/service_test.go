package service

import (
	"errors"
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

func TestRouterLogFiles(t *testing.T) {
	stdout, stderr := RouterLogFiles()
	if !strings.HasSuffix(stdout, "git-treeline/router.log") {
		t.Errorf("stdout log path = %q, want .../git-treeline/router.log", stdout)
	}
	if !strings.HasSuffix(stderr, "git-treeline/router.err") {
		t.Errorf("stderr log path = %q, want .../git-treeline/router.err", stderr)
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
	origRunCmdOutput := runCmdOutput
	origSleep := sleepFn
	origHealth := waitRouterRespondingFn

	plistPathFn = func() string { return plist }
	sleepFn = func(time.Duration) {}
	waitRouterRespondingFn = func(int, time.Duration) error { return nil }
	// Default: the label is already deregistered, so the bootout poll exits
	// on its first probe. Tests override runCmd to model a lingering label.
	runCmd = func(name string, args ...string) error {
		if name == "launchctl" && len(args) > 0 && args[0] == "list" {
			return errors.New("could not find service")
		}
		return nil
	}

	versionFile = RouterVersionFile()

	return versionFile, func() {
		plistPathFn = origPlistFn
		runCmd = origRunCmd
		runCmdOutput = origRunCmdOutput
		sleepFn = origSleep
		waitRouterRespondingFn = origHealth
	}
}

func TestReloadLaunchAgent_BootstrapRetry_SucceedsAfterExit5(t *testing.T) {
	versionFile, cleanup := setupReloadTest(t)
	defer cleanup()

	bootstrapCalls := 0
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			if bootstrapCalls < 3 {
				return nil, fakeExitErr(t, 5)
			}
			// Simulate the router writing its version file on successful start.
			_ = os.WriteFile(versionFile, []byte("v1"), 0o644)
			return nil, nil
		}
		return nil, nil
	}

	if err := reloadLaunchAgent(3001, 2*time.Second); err != nil {
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
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			return nil, fakeExitErr(t, 5)
		}
		return nil, nil
	}

	err := reloadLaunchAgent(3001, 100*time.Millisecond)
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
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			return nil, fakeExitErr(t, 1) // non-5 failure
		}
		return nil, nil
	}

	err := reloadLaunchAgent(3001, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error")
	}
	if bootstrapCalls != 1 {
		t.Errorf("expected exactly 1 bootstrap attempt for non-5 error, got %d", bootstrapCalls)
	}
}

func TestReloadLaunchAgent_WaitsForLingeringLabel(t *testing.T) {
	versionFile, cleanup := setupReloadTest(t)
	defer cleanup()

	// Model bootout's asynchronous deregistration: the label stays
	// registered for the first few polls, then disappears.
	listCalls := 0
	runCmd = func(name string, args ...string) error {
		if name == "launchctl" && len(args) > 0 && args[0] == "list" {
			listCalls++
			if listCalls < 4 {
				return nil // still registered
			}
			return errors.New("could not find service")
		}
		return nil
	}
	bootstrapCalls := 0
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			if listCalls < 4 {
				t.Error("bootstrap fired while the label was still registered")
			}
			bootstrapCalls++
			_ = os.WriteFile(versionFile, []byte("v1"), 0o644)
		}
		return nil, nil
	}

	if err := reloadLaunchAgent(3001, 2*time.Second); err != nil {
		t.Fatalf("reloadLaunchAgent: %v", err)
	}
	if listCalls != 4 {
		t.Errorf("expected 4 list polls, got %d", listCalls)
	}
	if bootstrapCalls != 1 {
		t.Errorf("expected 1 bootstrap call, got %d", bootstrapCalls)
	}
}

func TestReloadLaunchAgent_DeregistrationTimeout_StillAttemptsBootstrap(t *testing.T) {
	_, cleanup := setupReloadTest(t)
	defer cleanup()
	withFakeClock(t)

	// Label never deregisters: the poll must give up at its deadline and
	// bootstrap must still be attempted (and surface launchd's failure).
	listCalls := 0
	runCmd = func(name string, args ...string) error {
		if name == "launchctl" && len(args) > 0 && args[0] == "list" {
			listCalls++
			return nil // registered forever
		}
		return nil
	}
	bootstrapCalls := 0
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
			return []byte("Bootstrap failed: 5: Input/output error"), fakeExitErr(t, 5)
		}
		return nil, nil
	}

	err := reloadLaunchAgent(3001, time.Second)
	if err == nil {
		t.Fatal("expected error when label never deregisters and bootstrap fails")
	}
	if listCalls < 2 {
		t.Errorf("expected repeated list polls before giving up, got %d", listCalls)
	}
	if bootstrapCalls != 3 {
		t.Errorf("expected 3 bootstrap attempts (retry backstop), got %d", bootstrapCalls)
	}
	// launchd's reason string must survive into the error — a bare
	// "exit status 5" is ambiguous.
	if !strings.Contains(err.Error(), "Input/output error") {
		t.Errorf("expected launchctl output in error, got: %v", err)
	}
}

func TestInstallLaunchAgent_UnchangedPlist_UsesKickstart(t *testing.T) {
	versionFile := withTempVersionFile(t)
	withFakeHealth(t)

	gtlPath := "/opt/homebrew/bin/gtl"
	content, err := GeneratePlist(gtlPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(PlistPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PlistPath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := withFakeRunCmd(t, nil)
	origRun := runCmd
	runCmd = func(name string, args ...string) error {
		err := origRun(name, args...)
		if err == nil && len(args) > 0 && args[0] == "kickstart" {
			_ = os.WriteFile(versionFile, []byte("v1"), 0o644)
		}
		return err
	}

	if _, err := installLaunchAgent(gtlPath, 3001); err != nil {
		t.Fatalf("installLaunchAgent: %v", err)
	}
	var sawKickstart bool
	for _, c := range *calls {
		if strings.Contains(c, "kickstart") {
			sawKickstart = true
		}
		if strings.Contains(c, "bootout") || strings.Contains(c, "bootstrap") {
			t.Errorf("unchanged plist must not bootout/bootstrap, got: %v", *calls)
		}
	}
	if !sawKickstart {
		t.Errorf("expected kickstart for unchanged plist, got: %v", *calls)
	}
}

func TestInstallLaunchAgent_ChangedPlist_WritesAndReloads(t *testing.T) {
	versionFile := withTempVersionFile(t)
	withFakeHealth(t)

	gtlPath := "/opt/homebrew/bin/gtl"
	if err := os.MkdirAll(filepath.Dir(PlistPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	// On-disk plist points at a stale Cellar path → content differs.
	if err := os.WriteFile(PlistPath(), []byte("<plist>stale</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := withFakeRunCmd(t, map[string]error{
		"launchctl list": errors.New("could not find service"),
	})
	origOut := runCmdOutput
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		out, err := origOut(name, args...)
		if err == nil && len(args) > 0 && args[0] == "bootstrap" {
			_ = os.WriteFile(versionFile, []byte("v1"), 0o644)
		}
		return out, err
	}

	if _, err := installLaunchAgent(gtlPath, 3001); err != nil {
		t.Fatalf("installLaunchAgent: %v", err)
	}
	want, _ := GeneratePlist(gtlPath)
	got, err := os.ReadFile(PlistPath())
	if err != nil || string(got) != want {
		t.Errorf("plist not rewritten with new content (err=%v)", err)
	}
	var sawBootstrap bool
	for _, c := range *calls {
		if strings.Contains(c, "bootstrap") {
			sawBootstrap = true
		}
		if strings.Contains(c, "kickstart") {
			t.Errorf("changed plist must reload, not kickstart, got: %v", *calls)
		}
	}
	if !sawBootstrap {
		t.Errorf("expected bootstrap for changed plist, got: %v", *calls)
	}
}
