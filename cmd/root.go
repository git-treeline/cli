package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "git-treeline",
	Short:         "Worktree environment manager — ports, databases, and Redis across parallel development environments",
	SilenceErrors: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		formatCliError(err)
		os.Exit(1)
	}
}
