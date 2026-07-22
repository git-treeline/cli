package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var claimPath string

func init() {
	claimCmd.Flags().StringVar(&claimPath, "path", "", "Custom worktree path (default: ../<project>-<branch>)")
	claimCmd.ValidArgsFunction = completeBranches
	rootCmd.AddCommand(claimCmd)
}

var claimCmd = &cobra.Command{
	Use:   "claim <branch>",
	Short: "Check out a branch into a worktree at its freshest remote state",
	Long: `Fetch <branch> from origin and ensure a worktree exists for it at the
latest commit origin has. If the branch is already checked out somewhere,
claim just fast-forwards it — safe to re-run (idempotent re-claim).
Otherwise it adopts the branch into a new worktree the same way 'gtl new'
does for an existing branch, then fast-forwards.

Unlike 'gtl new', 'claim' never creates a new branch — it only adopts one
that already exists locally or on origin, so --base does not apply.

If the branch has diverged from origin (can't fast-forward), claim warns
and leaves the worktree as-is rather than failing: the worktree existing
is the claim, resolving the divergence is on you.

Prints the worktree path to stdout on success and nothing else, so it's
safe to capture, e.g.: wt=$(gtl claim agent/some-branch)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branch := args[0]

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		absPath, _ := filepath.Abs(cwd)
		mainRepo := worktree.DetectMainRepo(absPath)
		pc := config.LoadProjectConfig(mainRepo)
		uc := config.LoadUserConfig("")

		fmt.Fprintln(os.Stderr, style.Actionf("Fetching origin/%s...", branch))
		if err := worktree.Fetch("origin", branch); err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("fetch failed (%s), trying local state", err))
		}

		// Already checked out somewhere: ensure it has an allocation and bring
		// it up to date. This is what makes re-claiming the same branch safe.
		if existingWT := worktree.FindWorktreeForBranch(branch); existingWT != "" {
			fmt.Fprintln(os.Stderr, style.Actionf("Branch '%s' already checked out at %s", branch, existingWT))
			if pc.Exists() {
				if _, err := ensureWorktreeAllocation(existingWT, mainRepo, uc, os.Stderr); err != nil {
					return cliErr(cmd, err)
				}
			}
			warnOnClaimPullFailure(branch, existingWT, worktree.Pull(existingWT, "origin", branch))
			fmt.Println(existingWT)
			return nil
		}

		if !worktree.BranchExists(branch) {
			return cliErr(cmd, errClaimBranchNotFound(branch))
		}

		projectName := pc.Project()
		wtPath := resolveWorktreePath(claimPath, mainRepo, projectName, branch, uc)

		if err := ensureGitignored(mainRepo, wtPath, os.Stderr); err != nil {
			return err
		}

		fmt.Fprintln(os.Stderr, style.Actionf("Checking out branch '%s'", branch))
		if err := worktree.Create(wtPath, branch, false, ""); err != nil {
			return cliErr(cmd, err)
		}

		if pc.Exists() {
			// Same adopt path 'gtl new' uses for an existing branch: run setup,
			// rolling the worktree back if it fails.
			if _, err := runSetupWithRollback(cmd, wtPath, mainRepo, uc, os.Stderr); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(os.Stderr, style.Actionf("Worktree created at %s", wtPath))
		}

		warnOnClaimPullFailure(branch, wtPath, worktree.Pull(wtPath, "origin", branch))

		fmt.Println(wtPath)
		return nil
	},
}

// warnOnClaimPullFailure prints a non-fatal warning for a failed post-claim
// fast-forward pull. A diverged branch gets pointed manual-resolution
// guidance; any other failure (no origin, offline, branch deleted upstream)
// gets a generic warning. Either way the claim has already succeeded — the
// worktree exists at wtPath — so this never fails the command.
func warnOnClaimPullFailure(branch, wtPath string, err error) {
	if err == nil {
		return
	}
	if worktree.IsDivergedPull(err) {
		fmt.Fprintln(os.Stderr, style.Warnf("Branch '%s' has diverged from origin/%s — can't fast-forward.", branch, branch))
		fmt.Fprintln(os.Stderr, style.Dimf("  Resolve manually in %s (e.g. 'git merge origin/%s' or 'git rebase'), then retry.", wtPath, branch))
		return
	}
	fmt.Fprintln(os.Stderr, style.Warnf("Could not pull latest for '%s': %s", branch, err))
}
