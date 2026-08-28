package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/selfupdate"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "git-treeline",
	Short:         "Worktree environment manager — ports, databases, and Redis across parallel development environments",
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		_ = platform.EnsureConfigDir()
		maybeWarnStaleRouter(cmd)
		maybeCheckForUpdate(cmd)
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		printUpdateNotice()
	},
}

// commandsThatSelfRepair are commands the user runs to FIX a stale router —
// emitting a "router is stale" warning during these is just noise.
var commandsThatSelfRepair = map[string]bool{
	"install":      true,
	"update":       true,
	"serve":        true, // covers all serve subcommands
	"version":      true,
	"help":         true,
	"completion":   true,
	"__complete":   true, // shell completion handler
	"__completeNoDesc": true,
}

// maybeWarnStaleRouter prints a one-line warning when the running router's
// version disagrees with the CLI binary. Suppressed for commands that are
// themselves intended to fix the situation.
//
// Cost: a single os.ReadFile of a tiny version file. No network, no sudo.
// Suppress entirely with GTL_NO_STALE_WARN=1 (CI environments).
func maybeWarnStaleRouter(cmd *cobra.Command) {
	if !shouldWarnStaleRouter(rootCommandName(cmd), Version, service.RunningRouterVersion(),
		os.Getenv("GTL_NO_STALE_WARN")) {
		return
	}
	fmt.Fprintln(os.Stderr, style.Warnf("Router is running %s but CLI is %s.", service.RunningRouterVersion(), Version))
	fmt.Fprintln(os.Stderr, style.Dimf("  Run 'gtl serve restart' to update the router (or 'gtl serve install' for a full reset)."))
	fmt.Fprintln(os.Stderr, style.Dimf("  Suppress this warning: GTL_NO_STALE_WARN=1"))
}

// shouldWarnStaleRouter is the pure decision logic, exposed for testing.
func shouldWarnStaleRouter(rootCmd, cliVersion, runningVersion, suppressEnv string) bool {
	if suppressEnv != "" {
		return false
	}
	if cliVersion == "" || cliVersion == "dev" {
		return false
	}
	if commandsThatSelfRepair[rootCmd] {
		return false
	}
	if runningVersion == "" || runningVersion == cliVersion {
		return false
	}
	return true
}

// updateNoticeVersion, when non-empty, is the newer release the post-run
// notice should mention. Set during PersistentPreRun from the check cache
// only — never from a live network call.
var updateNoticeVersion string

// maybeCheckForUpdate drives the passive update notice. It only ever reads
// the on-disk check cache in-band; when the cache is stale it refreshes in a
// background goroutine that lossily writes the cache for a future invocation
// (short-lived commands may exit first — gh's tradeoff, accepted here too).
func maybeCheckForUpdate(cmd *cobra.Command) {
	if !shouldCheckForUpdate(rootCommandName(cmd), Version,
		os.Getenv("GTL_NO_UPDATE_NOTIFY"), stderrIsTTY()) {
		return
	}
	if state, ok := selfupdate.ReadState(); ok && state.Fresh(time.Now()) {
		if selfupdate.IsNewer(Version, state.Latest) {
			updateNoticeVersion = state.Latest
		}
		return
	}
	go func() {
		if latest, err := selfupdate.FetchLatestVersion(3 * time.Second); err == nil {
			_ = selfupdate.WriteState(latest, time.Now())
		}
	}()
}

// shouldCheckForUpdate is the pure decision logic, exposed for testing.
// Reuses commandsThatSelfRepair as the suppression set: those are exactly
// the commands where an extra stderr line is noise (help, completion,
// version) or where the user is already mid-repair (install, update, serve).
func shouldCheckForUpdate(rootCmd, cliVersion, suppressEnv string, isTTY bool) bool {
	if suppressEnv != "" || !isTTY {
		return false
	}
	if cliVersion == "" || cliVersion == "dev" {
		return false
	}
	return !commandsThatSelfRepair[rootCmd]
}

// printUpdateNotice runs from PersistentPostRun so the nudge lands after the
// command's own output, not interleaved with it. PostRun is skipped when
// RunE errors — a failing command shouldn't end on an upsell.
func printUpdateNotice() {
	if updateNoticeVersion == "" {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, style.Dimf("A new version of git-treeline is available (%s → %s). Run 'gtl update'.",
		Version, updateNoticeVersion))
}

func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// rootCommandName returns the top-level subcommand name used to invoke this
// run. For 'gtl serve restart' it returns "serve"; for 'gtl status' it
// returns "status".
func rootCommandName(cmd *cobra.Command) string {
	root := cmd.Root()
	for c := cmd; c != nil; c = c.Parent() {
		if c.Parent() == root {
			return strings.SplitN(c.Use, " ", 2)[0]
		}
		if c == root {
			return ""
		}
	}
	return ""
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		formatCliError(err)
		var sce *setup.SetupCommandError
		if errors.As(err, &sce) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
