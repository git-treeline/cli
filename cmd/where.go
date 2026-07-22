package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

func init() {
	whereCmd.ValidArgsFunction = completeRegistryBranch
	rootCmd.AddCommand(whereCmd)
}

var whereCmd = &cobra.Command{
	Use:   "where <branch>",
	Short: "Print the path to a worktree by branch name",
	Long: `Looks up a worktree by branch name and prints its path.

Resolution order:
  1. The whole argument as a branch in the current project. This makes
     branch names that themselves contain a slash (e.g. 'feature/foo',
     'impl/717f7b0e') resolve correctly instead of being misread as
     project/branch.
  2. If that misses, the argument as project/branch — the documented
     disambiguation for a branch that exists in more than one project:
       gtl where salt/feature-auth
     Used only when that project actually has that branch.
  3. Otherwise, the whole argument as a branch name across all projects
     — erroring if it matches more than one.

Useful for scripting:
  cd $(gtl where feature-auth)
  code $(gtl where feature-auth)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		reg := registry.New("")
		allocs := reg.Allocations()

		if currentProject := currentProjectName(); currentProject != "" {
			if matches := whereMatches(allocs, currentProject, query); len(matches) == 1 {
				warnIfSplitAlsoMatches(query, currentProject, allocs)
				return printWhereMatch(cmd, matches[0], query)
			}
		}

		if idx := strings.Index(query, "/"); idx >= 0 {
			project, branch := query[:idx], query[idx+1:]
			if matches := whereMatches(allocs, project, branch); len(matches) > 0 {
				return resolveWhereMatches(cmd, matches, query, branch)
			}
		}

		return resolveWhereMatches(cmd, whereMatches(allocs, "", query), query, query)
	},
}

// currentProjectName best-effort resolves "the current project" the same way
// 'gtl new'/'gtl claim' do: detect the main repo from cwd, then read its
// project name (from .treeline.yml, or the sanitized directory name when
// there isn't one). Returns "" if cwd can't even be read.
func currentProjectName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	absPath, _ := filepath.Abs(cwd)
	mainRepo := worktree.DetectMainRepo(absPath)
	return config.LoadProjectConfig(mainRepo).Project()
}

// whereMatches returns allocations whose branch equals branch. An empty
// project matches any project; otherwise the allocation's project must
// equal it too.
func whereMatches(allocs []registry.Allocation, project, branch string) []registry.Allocation {
	var matches []registry.Allocation
	for _, a := range allocs {
		allocBranch, _ := a["branch"].(string)
		if allocBranch != branch {
			continue
		}
		if project != "" {
			allocProject, _ := a["project"].(string)
			if allocProject != project {
				continue
			}
		}
		matches = append(matches, a)
	}
	return matches
}

// whereProjectKnown reports whether any allocation belongs to project.
func whereProjectKnown(allocs []registry.Allocation, project string) bool {
	for _, a := range allocs {
		if p, _ := a["project"].(string); p == project {
			return true
		}
	}
	return false
}

// warnIfSplitAlsoMatches prints a stderr note when the current-project match
// 'where' is about to return is also shaped like 'project/branch' for a
// *different*, known project that has a matching branch. Without this, a
// user who meant the documented disambiguation syntax (project/branch) would
// silently get the current project's literally-named branch instead, with no
// indication the other reading existed.
func warnIfSplitAlsoMatches(query, currentProject string, allocs []registry.Allocation) {
	idx := strings.Index(query, "/")
	if idx < 0 {
		return
	}
	project, branch := query[:idx], query[idx+1:]
	if project == currentProject || !whereProjectKnown(allocs, project) {
		return
	}
	if len(whereMatches(allocs, project, branch)) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, style.Dimf(
		"Note: %q is a branch in the current project (%s); it also reads as project/branch %s/%s, which was not used.",
		query, currentProject, project, branch))
}

// resolveWhereMatches applies the not-found / ambiguous / found handling
// shared by all three resolution tiers.
func resolveWhereMatches(cmd *cobra.Command, matches []registry.Allocation, query, branch string) error {
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

	return printWhereMatch(cmd, matches[0], query)
}

func printWhereMatch(cmd *cobra.Command, alloc registry.Allocation, query string) error {
	worktreePath, _ := alloc["worktree"].(string)
	if worktreePath == "" {
		return cliErr(cmd, &CliError{
			Message: fmt.Sprintf("Allocation for branch %q has no worktree path", query),
			Hint:    "The registry may be corrupted. Run 'gtl prune --stale' to clean up.",
		})
	}
	fmt.Println(worktreePath)
	return nil
}
