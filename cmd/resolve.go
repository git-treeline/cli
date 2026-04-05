package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/resolve"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/spf13/cobra"
)

var resolveJSON bool

func init() {
	resolveCmd.Flags().BoolVar(&resolveJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(resolveCmd)
}

var resolveCmd = &cobra.Command{
	Use:   "resolve <project> [branch]",
	Short: "Resolve a project's URL from the registry",
	Long: `Print the URL for another project's worktree, resolved from the registry.
Uses the same-branch default (matching your current branch) unless an
explicit branch is provided.

Respects active links set via 'gtl link'.

Examples:
  gtl resolve api                    # same-branch lookup
  gtl resolve api feature-payments   # explicit branch
  curl $(gtl resolve api)/health     # scripting`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		project := args[0]
		var explicitBranch string
		if len(args) > 1 {
			explicitBranch = args[1]
		}

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)

		reg := registry.New("")
		branch := detectCurrentBranch(absPath)

		r := resolve.New(reg, absPath, branch)

		var url string
		if explicitBranch != "" {
			url, err = r.Resolve(project, explicitBranch)
		} else {
			url, err = r.Resolve(project)
		}
		if err != nil {
			return err
		}

		if service.IsRunning() {
			targetAlloc := findResolvedAlloc(reg, project, explicitBranch, branch, absPath)
			if targetAlloc != nil {
				targetBranch, _ := targetAlloc["branch"].(string)
				targetProject, _ := targetAlloc["project"].(string)
				if targetBranch != "" && targetProject != "" {
					routeKey := proxy.RouteKey(targetProject, targetBranch)
					uc := config.LoadUserConfig("")
					if service.IsPortForwardConfigured() {
						url = fmt.Sprintf("https://%s.localhost", routeKey)
					} else {
						url = fmt.Sprintf("https://%s.localhost:%d", routeKey, uc.RouterPort())
					}
				}
			}
		}

		if resolveJSON {
			data, _ := json.MarshalIndent(map[string]string{"url": url}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Println(url)
		return nil
	},
}

func findResolvedAlloc(reg *registry.Registry, project, explicitBranch, currentBranch, worktreePath string) registry.Allocation {
	branch := currentBranch
	if explicitBranch != "" {
		branch = explicitBranch
	} else {
		links := reg.GetLinks(worktreePath)
		if linked, ok := links[project]; ok {
			branch = linked
		}
	}
	return reg.FindProjectBranch(project, branch)
}
