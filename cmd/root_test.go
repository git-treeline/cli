package cmd

import "testing"

func TestShouldWarnStaleRouter_Truthy(t *testing.T) {
	if !shouldWarnStaleRouter("status", "0.39.4", "0.39.2", "") {
		t.Error("expected warning when version mismatches and command is not self-repairing")
	}
}

func TestShouldWarnStaleRouter_SuppressedByEnv(t *testing.T) {
	if shouldWarnStaleRouter("status", "0.39.4", "0.39.2", "1") {
		t.Error("GTL_NO_STALE_WARN should suppress")
	}
}

func TestShouldWarnStaleRouter_SuppressedDuringSelfRepair(t *testing.T) {
	for _, c := range []string{"install", "serve"} {
		if shouldWarnStaleRouter(c, "0.39.4", "0.39.2", "") {
			t.Errorf("%q is self-repairing — should not warn", c)
		}
	}
}

func TestShouldWarnStaleRouter_QuietWhenVersionsMatch(t *testing.T) {
	if shouldWarnStaleRouter("status", "0.39.4", "0.39.4", "") {
		t.Error("matching versions should not warn")
	}
}

func TestShouldWarnStaleRouter_QuietForDevBuild(t *testing.T) {
	if shouldWarnStaleRouter("status", "dev", "0.39.2", "") {
		t.Error("dev builds shouldn't nag (unstable Version string)")
	}
}

func TestShouldWarnStaleRouter_QuietWhenNoRunningRouter(t *testing.T) {
	if shouldWarnStaleRouter("status", "0.39.4", "", "") {
		t.Error("router never started — nothing to warn about")
	}
}
