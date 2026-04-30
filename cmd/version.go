package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set via ldflags at build time: -ldflags "-X github.com/git-treeline/cli/cmd.Version=v0.3.0"
var Version = "dev"

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("git-treeline %s\n", Version)
	},
}
