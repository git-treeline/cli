package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveIptables_PrefersPath verifies that a PATH-resolvable iptables is
// used instead of a hardcoded /sbin path.
func TestResolveIptables_PrefersPath(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "iptables")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got, err := resolveIptables()
	if err != nil {
		t.Fatalf("resolveIptables: %v", err)
	}
	if got != fake {
		t.Errorf("expected PATH iptables %q, got %q", fake, got)
	}
}

// TestResolveIptables_NoneActionable verifies the error message is actionable
// when neither iptables nor nft is available.
func TestResolveIptables_NoneActionable(t *testing.T) {
	dir := t.TempDir() // empty: no iptables, no nft
	t.Setenv("PATH", dir)

	// Guard: the absolute-path fallbacks must not exist on the test host, or
	// this assertion is meaningless. Skip if a real system iptables is present.
	for _, c := range []string{"/usr/sbin/iptables", "/sbin/iptables", "/usr/bin/iptables"} {
		if _, err := os.Stat(c); err == nil {
			t.Skip("system iptables present at " + c)
		}
	}

	_, err := resolveIptables()
	if err == nil {
		t.Fatal("expected error when no iptables/nft available")
	}
	if !strings.Contains(err.Error(), "iptables") {
		t.Errorf("error should mention iptables, got: %v", err)
	}
}

func TestLinuxInstallScript_Idempotent(t *testing.T) {
	s := linuxInstallScript("/usr/sbin/iptables", 3001)
	// Must check before adding so re-runs don't stack duplicate rules.
	if !strings.Contains(s, "-C OUTPUT") {
		t.Errorf("install script must check (-C) before adding\nscript: %s", s)
	}
	if !strings.Contains(s, "|| /usr/sbin/iptables -t nat -A OUTPUT") {
		t.Errorf("install script must only add when check fails\nscript: %s", s)
	}
	if !strings.Contains(s, "--to-port 3001") {
		t.Errorf("install script must redirect to router port\nscript: %s", s)
	}
	if !strings.Contains(s, "--comment git-treeline") {
		t.Errorf("install script must tag the rule with our marker\nscript: %s", s)
	}
}

func TestLinuxUninstallScript_Bounded(t *testing.T) {
	s := linuxUninstallScript("/usr/sbin/iptables")
	if !strings.Contains(s, "[ $i -lt 20 ]") {
		t.Errorf("uninstall script must be bounded (no infinite loop)\nscript: %s", s)
	}
	// Honest exit codes: 0 clean, 1 delete denied, 2 gave up.
	for _, want := range []string{"exit 0", "|| exit 1", "exit 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("uninstall script missing honest exit code %q\nscript: %s", want, s)
		}
	}
	if !strings.Contains(s, "git-treeline") {
		t.Errorf("uninstall script must target our marked rules\nscript: %s", s)
	}
}
