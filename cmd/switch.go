package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/github"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var switchRunSetup bool
var switchRestart bool

func init() {
	switchCmd.Flags().BoolVar(&switchRunSetup, "setup", false, "Re-run commands.setup after switching (for when deps changed)")
	switchCmd.Flags().BoolVar(&switchRestart, "restart", false, "Restart the supervised server after switching")
	switchCmd.ValidArgsFunction = completeBranchesAndPRs
	rootCmd.AddCommand(switchCmd)
}

var switchCmd = &cobra.Command{
	Use:   "switch <branch-or-PR#>",
	Short: "Switch this worktree to a different branch or PR",
	Long: `Switch the current worktree to a different branch. Accepts either a
branch name or a PR number (resolved via gh). Fetches from origin,
checks out the branch, and refreshes the environment.

Must be run from inside a worktree (not the main repo).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		absPath, _ := filepath.Abs(cwd)
		mainRepo := worktree.DetectMainRepo(absPath)

		if !isInWorktree(absPath, mainRepo) {
			return cliErr(cmd, errNotInWorktree())
		}

		branch := target
		if prNum, err := strconv.Atoi(target); err == nil {
			fmt.Println(style.Actionf("Looking up PR #%d...", prNum))
			pr, err := github.LookupPR(prNum)
			if err != nil {
				return err
			}
			branch = pr.HeadRefName
			fmt.Println(style.Actionf("PR #%d → branch '%s'", prNum, branch))
		}

		if err := switchWorktreeBranch(absPath, mainRepo, branch, switchRunSetup); err != nil {
			return err
		}

		if switchRestart {
			sockPath := supervisor.SocketPath(absPath)
			if resp, err := supervisor.Send(sockPath, "restart"); err == nil && resp == "ok" {
				fmt.Println("Server restarted.")
			} else {
				fmt.Fprintln(os.Stderr, style.Warnf("could not restart server (is it running?)"))
			}
		}

		return nil
	},
}

// switchWorktreeBranch fetches a branch, checks it out in the current worktree,
// updates the registry, and refreshes the environment. Shared by gtl switch
// and gtl review (when switching in place).
func switchWorktreeBranch(absPath, mainRepo, branch string, runSetup bool) error {
	fmt.Println(style.Actionf("Fetching origin/%s...", branch))
	if err := worktree.Fetch("origin", branch); err != nil {
		fmt.Fprintln(os.Stderr, style.Warnf("fetch failed (%s), trying local checkout", err))
	}

	fmt.Println(style.Actionf("Checking out '%s'...", branch))
	if err := worktree.Checkout(branch); err != nil {
		return err
	}

	reg := registry.New("")
	if err := reg.UpdateField(absPath, "branch", branch); err != nil {
		fmt.Fprintln(os.Stderr, style.Warnf("could not update branch in registry: %v", err))
	}

	uc := config.LoadUserConfig("")
	s := setup.New(absPath, mainRepo, uc)
	s.Options.RefreshOnly = !runSetup
	alloc, err := s.Run()
	if err != nil {
		return fmt.Errorf("refresh failed: %w", err)
	}

	fmt.Println()
	fmt.Printf("Switched to %s\n", branch)
	fmt.Printf("  Path: %s\n", absPath)
	if alloc != nil && alloc.Port > 0 {
		fmt.Printf("  URL:  http://localhost:%d\n", alloc.Port)
	}

	return nil
}

func completeBranchesAndPRs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	completions := worktree.ListBranches(toComplete)
	if prs, err := github.ListOpenPRs(); err == nil {
		for _, pr := range prs {
			completions = append(completions, fmt.Sprintf("%d\t%s", pr.Number, pr.Title))
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
