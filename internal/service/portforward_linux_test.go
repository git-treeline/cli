package service

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	s := linuxInstallScript("/usr/sbin/iptables", 3001, "/tmp/unit.service")
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
	// Rule application gates success; persistence is best-effort (masked).
	if !strings.Contains(s, "} || exit 1;") {
		t.Errorf("rule application must gate overall success\nscript: %s", s)
	}
	if !strings.Contains(s, "systemctl enable") || !strings.Contains(s, "} || true") {
		t.Errorf("persistence unit install must be best-effort (masked)\nscript: %s", s)
	}
}

func TestLinuxPortForwardUnitBody(t *testing.T) {
	body := linuxPortForwardUnitBody("/usr/sbin/iptables", 3001)
	for _, want := range []string{
		"Type=oneshot",
		"RemainAfterExit=yes",
		"WantedBy=multi-user.target",
		"--to-port 3001",
		"--comment git-treeline",
		"-C OUTPUT", // idempotent check-or-add in ExecStart
	} {
		if !strings.Contains(body, want) {
			t.Errorf("unit body missing %q\nbody: %s", want, body)
		}
	}
}

func TestLinuxUninstallScript_RemovesPersistenceUnit(t *testing.T) {
	s := linuxUninstallScript("/usr/sbin/iptables")
	if !strings.Contains(s, "systemctl disable") {
		t.Errorf("uninstall must disable the persistence unit\nscript: %s", s)
	}
	if !strings.Contains(s, linuxPortForwardUnitPath()) {
		t.Errorf("uninstall must remove the unit file\nscript: %s", s)
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

func TestLinuxDialFallback_LivePortMarksConfigured(t *testing.T) {
	orig := pfDialTimeout
	pfDialTimeout = func(_, _ string, _ time.Duration) (net.Conn, error) {
		return fakePFConn{}, nil
	}
	t.Cleanup(func() { pfDialTimeout = orig })

	// Kernel state unknown (non-root) and marker absent, but :443 answers.
	st := linuxDialFallback(PortForwardStatus{ConfiguredOnDisk: false, KernelStateKnown: false, Detail: "not configured"})
	if !st.ConfiguredOnDisk {
		t.Error("a live :443 dial should mark the setup as configured for non-root")
	}
}

func TestLinuxDialFallback_DeadPortLeavesUnconfigured(t *testing.T) {
	orig := pfDialTimeout
	pfDialTimeout = func(_, _ string, _ time.Duration) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}
	t.Cleanup(func() { pfDialTimeout = orig })

	st := linuxDialFallback(PortForwardStatus{ConfiguredOnDisk: false, KernelStateKnown: false})
	if st.ConfiguredOnDisk {
		t.Error("a refused :443 dial must not mark the setup as configured")
	}
}

func TestLinuxDialFallback_NoopWhenKernelKnown(t *testing.T) {
	called := false
	orig := pfDialTimeout
	pfDialTimeout = func(_, _ string, _ time.Duration) (net.Conn, error) {
		called = true
		return fakePFConn{}, nil
	}
	t.Cleanup(func() { pfDialTimeout = orig })

	linuxDialFallback(PortForwardStatus{KernelStateKnown: true})
	if called {
		t.Error("dial fallback must not run when the kernel ruleset was read")
	}
}

type fakePFConn struct{ net.Conn }

func (fakePFConn) Close() error { return nil }

func TestParseToPort(t *testing.T) {
	body := linuxPortForwardUnitBody("/usr/sbin/iptables", 3001)
	got, ok := parseToPort(body)
	if !ok || got != 3001 {
		t.Errorf("parseToPort(unit body) = %d,%v; want 3001,true", got, ok)
	}
	if _, ok := parseToPort("no port here"); ok {
		t.Error("parseToPort should fail when --to-port is absent")
	}
	if got, ok := parseToPort("--to-port '8443'"); !ok || got != 8443 {
		t.Errorf("parseToPort quoted = %d,%v; want 8443,true", got, ok)
	}
}
