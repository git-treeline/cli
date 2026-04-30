package cmd

import (
	"fmt"
	"strings"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(whereCmd)
}

var whereCmd = &cobra.Command{
	Use:   "where <branch>",
	Short: "Print the path to a worktree by branch name",
	Long: `Looks up a worktree by branch name and prints its path.

If the branch exists in multiple projects, specify project/branch:
  gtl where salt/feature-auth

Useful for scripting:
  cd $(gtl where feature-auth)
  code $(gtl where feature-auth)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		reg := registry.New("")
		allocs := reg.Allocations()

		var project, branch string
		if strings.Contains(query, "/") {
			parts := strings.SplitN(query, "/", 2)
			project = parts[0]
			branch = parts[1]
		} else {
			branch = query
		}

		var matches []registry.Allocation
		for _, a := range allocs {
			allocBranch, _ := a["branch"].(string)
			allocProject, _ := a["project"].(string)

			if project != "" {
				if allocProject == project && allocBranch == branch {
					matches = append(matches, a)
				}
			} else {
				if allocBranch == branch {
					matches = append(matches, a)
				}
			}
		}

		if len(matches) == 0 {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("No worktree found for branch %q", query),
				Hint:    "Run 'gtl status' to see all worktrees.",
			})
		}

		if len(matches) > 1 {
			var projects []string
			for _, m := range matches {
				if p, ok := m["project"].(string); ok {
					projects = append(projects, p)
				}
			}
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Branch %q exists in multiple projects: %s", branch, strings.Join(projects, ", ")),
				Hint:    fmt.Sprintf("Specify project: gtl where %s/%s", projects[0], branch),
			})
		}

		worktree, _ := matches[0]["worktree"].(string)
		if worktree == "" {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Allocation for branch %q has no worktree path", query),
				Hint:    "The registry may be corrupted. Run 'gtl prune --stale' to clean up.",
			})
		}
		fmt.Println(worktree)
		return nil
	},
}
