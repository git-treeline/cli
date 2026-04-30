package cmd

import (
	"os"

	"github.com/git-treeline/cli/internal/platform"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "git-treeline",
	Short:         "Worktree environment manager — ports, databases, and Redis across parallel development environments",
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		_ = platform.EnsureConfigDir()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		formatCliError(err)
		os.Exit(1)
	}
}
