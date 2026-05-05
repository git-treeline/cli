package cmd

import (
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/service"
)

func TestFormatPortForwardStatus_NotConfigured(t *testing.T) {
	got := formatPortForwardStatus(service.PortForwardStatus{}, 3001)
	if !strings.Contains(got, "not configured") {
		t.Errorf("got %q", got)
	}
}

func TestFormatPortForwardStatus_PfDisabled(t *testing.T) {
	got := formatPortForwardStatus(service.PortForwardStatus{
		ConfiguredOnDisk: true,
		PfStateKnown:     true,
		PfEnabled:        false,
	}, 3001)
	if !strings.Contains(got, "pf is disabled") {
		t.Errorf("expected pf-disabled message, got %q", got)
	}
	if !strings.Contains(got, "reload-pf") {
		t.Errorf("expected reload-pf hint, got %q", got)
	}
}

// The exact scenario observed on macOS Sequoia without sudo: neither
// pfctl -s info nor pfctl -s nat returns useful output. Status must NOT
// claim pf is disabled — that is just as misleading as the previous
// "Port forwarding: active" lie.
func TestFormatPortForwardStatus_PfStateUnknown(t *testing.T) {
	got := formatPortForwardStatus(service.PortForwardStatus{
		ConfiguredOnDisk: true,
		PfStateKnown:     false,
		KernelStateKnown: false,
	}, 3001)
	if strings.Contains(got, "disabled") {
		t.Errorf("must not claim pf disabled when state unknown, got %q", got)
	}
	if !strings.Contains(got, "sudo") {
		t.Errorf("expected sudo note, got %q", got)
	}
}

func TestFormatPortForwardStatus_KernelUnknown_NoFalseAlarm(t *testing.T) {
	// pfctl -s info worked (PfStateKnown) but the per-anchor query needs
	// sudo. Status should NOT scream that the rule is missing.
	got := formatPortForwardStatus(service.PortForwardStatus{
		ConfiguredOnDisk: true,
		PfStateKnown:     true,
		PfEnabled:        true,
		KernelStateKnown: false,
	}, 3001)
	if strings.Contains(got, "⚠") || strings.Contains(got, "missing") {
		t.Errorf("kernel-unknown should not warn-flag, got %q", got)
	}
	if !strings.Contains(got, "not readable without sudo") {
		t.Errorf("expected sudo note in detail, got %q", got)
	}
}

func TestFormatPortForwardStatus_RuleMissingFromKernel(t *testing.T) {
	got := formatPortForwardStatus(service.PortForwardStatus{
		ConfiguredOnDisk: true,
		PfStateKnown:     true,
		PfEnabled:        true,
		KernelStateKnown: true,
		LoadedInKernel:   false,
	}, 3001)
	if !strings.Contains(got, "⚠") {
		t.Errorf("expected warning marker, got %q", got)
	}
	if !strings.Contains(got, "reload-pf") {
		t.Errorf("expected reload-pf hint, got %q", got)
	}
}

func TestFormatPortForwardStatus_Active(t *testing.T) {
	got := formatPortForwardStatus(service.PortForwardStatus{
		ConfiguredOnDisk: true,
		PfStateKnown:     true,
		PfEnabled:        true,
		KernelStateKnown: true,
		LoadedInKernel:   true,
	}, 3001)
	if !strings.Contains(got, "active (443 → 3001)") {
		t.Errorf("expected active message, got %q", got)
	}
	if strings.Contains(got, "⚠") {
		t.Errorf("active should not warn, got %q", got)
	}
}
