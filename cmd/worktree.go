package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(worktreeCmd)
}

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Print the worktree path for the current directory",
	Long: `Prints the worktree path recorded in the allocation registry for the
current directory. Useful for scripting and agent tooling.

Example:
  gtl worktree                    # /Users/me/conductor/workspaces/salt/feature-x
  open $(gtl worktree)/.env.local # open the env file in your editor`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		alloc, err := currentAllocation()
		if err != nil {
			return cliErr(cmd, err)
		}

		wt, _ := alloc.Entry["worktree"].(string)
		if wt == "" {
			wt = alloc.Path
		}
		fmt.Println(wt)
		return nil
	},
}
