package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/github"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var switchRunSetup bool

func init() {
	switchCmd.Flags().BoolVar(&switchRunSetup, "setup", false, "Re-run commands.setup after switching (for when deps changed)")
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

		resolvedAbs, _ := filepath.EvalSymlinks(absPath)
		resolvedMain, _ := filepath.EvalSymlinks(mainRepo)
		if resolvedAbs == resolvedMain {
			return fmt.Errorf("you're in the main repo, not a worktree.\n\n" +
				"  To create a new worktree:\n" +
				"    gtl new <branch>\n\n" +
				"  To review a PR in a new worktree:\n" +
				"    gtl review <PR#>")
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
		s.Options.RefreshOnly = !switchRunSetup
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
	},
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
