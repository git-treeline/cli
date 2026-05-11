package proxy

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClassifyBackend_PortListening(t *testing.T) {
	portUp := func(int, time.Duration) bool { return true }
	supCalled := false
	supFn := func(string) (string, error) {
		supCalled = true
		return "running", nil
	}

	got := classifyBackend("/tmp/wt", 3000, supFn, portUp)
	if got != BackendUnreachable {
		t.Errorf("port listening should yield BackendUnreachable, got %v", got)
	}
	if supCalled {
		t.Error("supervisor should not be probed when port is listening")
	}
}

func TestClassifyBackend_StartingUp(t *testing.T) {
	portDown := func(int, time.Duration) bool { return false }
	supRunning := func(string) (string, error) { return "running", nil }

	if got := classifyBackend("/tmp/wt", 3000, supRunning, portDown); got != BackendStarting {
		t.Errorf("expected BackendStarting, got %v", got)
	}
}

func TestClassifyBackend_NotStarted(t *testing.T) {
	portDown := func(int, time.Duration) bool { return false }
	supMissing := func(string) (string, error) { return "", fmt.Errorf("no socket") }

	if got := classifyBackend("/tmp/wt", 3000, supMissing, portDown); got != BackendNotStarted {
		t.Errorf("expected BackendNotStarted, got %v", got)
	}
}

func TestClassifyBackend_Stopped(t *testing.T) {
	portDown := func(int, time.Duration) bool { return false }
	supStopped := func(string) (string, error) { return "stopped", nil }

	if got := classifyBackend("/tmp/wt", 3000, supStopped, portDown); got != BackendStopped {
		t.Errorf("expected BackendStopped, got %v", got)
	}
}

func TestClassifyBackend_SupervisorWeirdResponse(t *testing.T) {
	portDown := func(int, time.Duration) bool { return false }
	supWeird := func(string) (string, error) { return "wat", nil }

	if got := classifyBackend("/tmp/wt", 3000, supWeird, portDown); got != BackendUnreachable {
		t.Errorf("expected BackendUnreachable for unknown supervisor response, got %v", got)
	}
}

func TestClassifyBackend_AliasRouteNoWorktree(t *testing.T) {
	portDown := func(int, time.Duration) bool { return false }
	supCalled := false
	supFn := func(string) (string, error) {
		supCalled = true
		return "running", nil
	}

	got := classifyBackend("", 3000, supFn, portDown)
	if got != BackendUnknown {
		t.Errorf("empty worktree path should yield BackendUnknown, got %v", got)
	}
	if supCalled {
		t.Error("supervisor should not be probed when worktree is unknown")
	}
}

func TestClassifyBackend_StatusResponseTrimmed(t *testing.T) {
	portDown := func(int, time.Duration) bool { return false }
	supTrailingWS := func(string) (string, error) { return "  running\n", nil }

	if got := classifyBackend("/tmp/wt", 3000, supTrailingWS, portDown); got != BackendStarting {
		t.Errorf("whitespace in supervisor response should not break classification, got %v", got)
	}
}

func TestDataForState_StartingHasRefreshAndElapsed(t *testing.T) {
	d := dataForState(BackendStarting, "salt-main", 3000)
	if d.Refresh != 2 {
		t.Errorf("starting state should refresh every 2s, got %d", d.Refresh)
	}
	if !d.ShowElapsed {
		t.Error("starting state should show elapsed counter")
	}
	if d.Tone != "amber" {
		t.Errorf("starting state should use amber tone, got %s", d.Tone)
	}
}

func TestDataForState_StoppedHasNoAutoRefresh(t *testing.T) {
	d := dataForState(BackendStopped, "salt-main", 3000)
	if d.Refresh != 0 {
		t.Errorf("stopped state should not auto-refresh (user must run gtl start), got refresh=%d", d.Refresh)
	}
	if d.Command == "" {
		t.Error("stopped state should suggest a recovery command")
	}
}

func TestDataForState_NotStartedSuggestsGtlStart(t *testing.T) {
	d := dataForState(BackendNotStarted, "salt-main", 3000)
	if d.Command != "gtl start" {
		t.Errorf("not-started state should suggest 'gtl start', got %q", d.Command)
	}
}

func TestToneClasses(t *testing.T) {
	cases := []struct {
		tone string
		want string // substring expected to appear
	}{
		{"amber", "amber-100"},
		{"rose", "rose-100"},
		{"slate", "slate-100"},
		{"unknown", "slate-100"}, // default fallback
	}
	for _, tc := range cases {
		got := toneClasses(tc.tone)
		if !strings.Contains(got, tc.want) {
			t.Errorf("toneClasses(%q) = %q, expected to contain %q", tc.tone, got, tc.want)
		}
	}
}
