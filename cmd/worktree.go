package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/registry"
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
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)

		reg := registry.New("")
		entry := reg.Find(absPath)
		if entry == nil {
			return cliErr(cmd, errNoAllocation(absPath))
		}

		wt, _ := entry["worktree"].(string)
		if wt == "" {
			wt = absPath
		}
		fmt.Println(wt)
		return nil
	},
}
