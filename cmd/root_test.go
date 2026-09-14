package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestPreRunDryRunDoesNotCreateState(t *testing.T) {
	for _, preview := range []bool{true, false} {
		name := "normal"
		if preview {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			t.Setenv("GTL_HOME", stateDir)
			t.Setenv("GTL_NO_STALE_WARN", "1")
			t.Setenv("GTL_NO_UPDATE_NOTIFY", "1")
			command := &cobra.Command{Use: "setup"}
			command.Flags().Bool("dry-run", preview, "")
			rootCmd.PersistentPreRun(command, nil)
			_, err := os.Stat(stateDir)
			if preview && !os.IsNotExist(err) {
				t.Fatalf("preview created state directory: %v", err)
			}
			if !preview && err != nil {
				t.Fatalf("normal command did not initialize state: %v", err)
			}
		})
	}
}

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
