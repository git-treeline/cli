package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/service"
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
	},
}

// commandsThatSelfRepair are commands the user runs to FIX a stale router —
// emitting a "router is stale" warning during these is just noise.
var commandsThatSelfRepair = map[string]bool{
	"install":      true,
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
		os.Exit(1)
	}
}
