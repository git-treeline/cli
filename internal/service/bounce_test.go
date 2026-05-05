package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// withFakeRunCmd replaces runCmd / runCmdOutput with capturing fakes for the
// duration of a test. The returned slice records every (name, args) call in
// order.
func withFakeRunCmd(t *testing.T, runErr map[string]error) *[]string {
	t.Helper()
	var calls []string

	origRun := runCmd
	origOut := runCmdOutput
	t.Cleanup(func() {
		runCmd = origRun
		runCmdOutput = origOut
	})

	runCmd = func(name string, args ...string) error {
		joined := name + " " + strings.Join(args, " ")
		calls = append(calls, joined)
		for prefix, err := range runErr {
			if strings.HasPrefix(joined, prefix) {
				return err
			}
		}
		return nil
	}
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}
	return &calls
}

// withFakeClock replaces nowFn / sleepFn so wait loops complete without real
// time passing. Each sleep advances the simulated clock.
func withFakeClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	origNow := nowFn
	origSleep := sleepFn
	t.Cleanup(func() {
		nowFn = origNow
		sleepFn = origSleep
	})
	nowFn = func() time.Time { return now }
	sleepFn = func(d time.Duration) { now = now.Add(d) }
	return &now
}

// withTempVersionFile points RouterVersionFile() at a temp directory so tests
// can write the version file without touching the real ConfigDir.
func withTempVersionFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("GTL_HOME", dir)
	// On macOS, ConfigDir uses ~/Library/Application Support — override
	// HOME so it lands in the temp dir.
	t.Setenv("HOME", dir)
	path := RouterVersionFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return path
}

func TestBounce_LaunchAgent_Kickstart_Success(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	versionPath := withTempVersionFile(t)
	withFakeClock(t)

	// Pre-populate version file with a known mtime, set to "before" the
	// fake clock so any later write counts as fresh.
	if err := os.WriteFile(versionPath, []byte("0.39.2"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := nowFn().Add(-time.Hour)
	_ = os.Chtimes(versionPath, past, past)

	calls := withFakeRunCmd(t, nil)

	// Simulate the router writing a fresh version file mid-bounce: when
	// kickstart is called, advance the file's mtime to the current
	// simulated clock.
	origRun := runCmd
	runCmd = func(name string, args ...string) error {
		err := origRun(name, args...)
		if err == nil && len(args) > 0 && args[0] == "kickstart" {
			t := nowFn().Add(50 * time.Millisecond)
			_ = os.Chtimes(versionPath, t, t)
		}
		return err
	}

	if err := Bounce(2 * time.Second); err != nil {
		t.Fatalf("Bounce: %v", err)
	}

	// First call should be `launchctl print` (probe), then `kickstart -k`.
	if len(*calls) < 2 {
		t.Fatalf("expected at least 2 launchctl calls, got %d: %v", len(*calls), *calls)
	}
	if !strings.Contains((*calls)[0], "launchctl print") {
		t.Errorf("first call should be print, got %q", (*calls)[0])
	}
	found := false
	for _, c := range *calls {
		if strings.Contains(c, "launchctl kickstart -k") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a kickstart -k call, got: %v", *calls)
	}
}

func TestBounce_LaunchAgent_FallsBackToBootstrap_WhenNotLoaded(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	versionPath := withTempVersionFile(t)
	withFakeClock(t)

	// Plist file must exist for bootstrap to proceed.
	plistDir := filepath.Dir(PlistPath())
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PlistPath(), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// `launchctl print` returns error → not loaded; bootstrap path runs.
	calls := withFakeRunCmd(t, map[string]error{
		"launchctl print": errors.New("service not loaded"),
	})

	// Have bootstrap "succeed" and write the version file.
	origRun := runCmd
	runCmd = func(name string, args ...string) error {
		if err := origRun(name, args...); err != nil {
			return err
		}
		if len(args) > 0 && args[0] == "bootstrap" {
			t := nowFn().Add(50 * time.Millisecond)
			_ = os.WriteFile(versionPath, []byte("9.9.9"), 0o644)
			_ = os.Chtimes(versionPath, t, t)
		}
		return nil
	}

	if err := Bounce(2 * time.Second); err != nil {
		t.Fatalf("Bounce: %v", err)
	}

	wantSeq := []string{"launchctl print", "launchctl bootout", "launchctl bootstrap"}
	for i, want := range wantSeq {
		if i >= len(*calls) || !strings.Contains((*calls)[i], want) {
			t.Fatalf("call %d: want prefix %q, got %v", i, want, *calls)
		}
	}
	// Ensure no kickstart call happened.
	for _, c := range *calls {
		if strings.Contains(c, "kickstart") {
			t.Errorf("did not expect kickstart in fallback path, got: %v", *calls)
		}
	}
}

func TestBounce_LaunchAgent_Times_Out_When_Version_Not_Updated(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	withTempVersionFile(t)
	withFakeClock(t)
	withFakeRunCmd(t, nil) // print + kickstart both succeed; nothing writes version file

	err := Bounce(500 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "did not record a new version") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBounce_LaunchAgent_Surfaces_Kickstart_Failure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	withTempVersionFile(t)
	withFakeClock(t)
	withFakeRunCmd(t, map[string]error{
		"launchctl kickstart": errors.New("Could not find service"),
	})

	err := Bounce(2 * time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "kickstart") {
		t.Errorf("expected error to mention kickstart, got %v", err)
	}
}

func TestBounce_Systemd_RestartUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	versionPath := withTempVersionFile(t)
	withFakeClock(t)

	calls := withFakeRunCmd(t, nil)
	origRun := runCmd
	runCmd = func(name string, args ...string) error {
		err := origRun(name, args...)
		if err == nil && len(args) > 1 && args[1] == "restart" {
			t := nowFn().Add(50 * time.Millisecond)
			_ = os.WriteFile(versionPath, []byte("9.9.9"), 0o644)
			_ = os.Chtimes(versionPath, t, t)
		}
		return err
	}

	if err := Bounce(2 * time.Second); err != nil {
		t.Fatalf("Bounce: %v", err)
	}
	if len(*calls) == 0 || !strings.Contains((*calls)[0], "systemctl --user restart") {
		t.Errorf("expected systemctl restart, got %v", *calls)
	}
}

func TestReload_LaunchAgent_BootoutThenBootstrap(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	versionPath := withTempVersionFile(t)
	withFakeClock(t)

	// Plist must exist.
	if err := os.MkdirAll(filepath.Dir(PlistPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PlistPath(), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := withFakeRunCmd(t, nil)
	origRun := runCmd
	runCmd = func(name string, args ...string) error {
		err := origRun(name, args...)
		if err == nil && len(args) > 0 && args[0] == "bootstrap" {
			t := nowFn().Add(50 * time.Millisecond)
			_ = os.WriteFile(versionPath, []byte("9.9.9"), 0o644)
			_ = os.Chtimes(versionPath, t, t)
		}
		return err
	}

	if err := Reload(2 * time.Second); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(*calls) < 2 {
		t.Fatalf("expected at least 2 calls (bootout, bootstrap), got %d", len(*calls))
	}
	if !strings.Contains((*calls)[0], "bootout") {
		t.Errorf("first call should be bootout, got %q", (*calls)[0])
	}
	if !strings.Contains((*calls)[1], "bootstrap") {
		t.Errorf("second call should be bootstrap, got %q", (*calls)[1])
	}
}

func TestReload_LaunchAgent_FailsWhenPlistMissing(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	withTempVersionFile(t)
	withFakeClock(t)
	// Do NOT create the plist.
	withFakeRunCmd(t, nil)

	err := Reload(time.Second)
	if err == nil {
		t.Fatal("expected error when plist is missing")
	}
	if !strings.Contains(err.Error(), "plist not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunningPIDDarwin_ParsesLaunchctlOutput(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	origOut := runCmdOutput
	t.Cleanup(func() { runCmdOutput = origOut })
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		return []byte(`gui/501/dev.treeline.router = {
	active count = 1
	on demand = false
	pid = 81129
	state = running
}`), nil
	}

	if got := RunningPID(); got != 81129 {
		t.Errorf("RunningPID() = %d, want 81129", got)
	}
}

func TestRunningPIDDarwin_ZeroOnError(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	origOut := runCmdOutput
	t.Cleanup(func() { runCmdOutput = origOut })
	runCmdOutput = func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("not loaded")
	}
	if got := RunningPID(); got != 0 {
		t.Errorf("RunningPID() = %d, want 0", got)
	}
}
