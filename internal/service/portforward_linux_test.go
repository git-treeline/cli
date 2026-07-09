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
