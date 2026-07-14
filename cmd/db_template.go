package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

func init() {
	dbTemplateCmd.AddCommand(dbTemplateUpdateCmd)
	dbCmd.AddCommand(dbTemplateCmd)
}

var dbTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage the root template database",
}

var dbTemplateUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Pull the merge target branch and run migrations in the root repo",
	Long: `Advances the root template database to match the current state of the
merge target branch. Pulls latest in the root repo, then runs the
commands.migrate command configured in .treeline.yml.

The root repo must have a clean working tree. Existing worktree databases
are not affected — run 'gtl db reset' in a worktree to re-clone from the
updated template.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		mainRepo := worktree.DetectMainRepo(cwd)
		pc := config.LoadProjectConfig(cwd)

		migrateCmd := pc.MigrateCommand()
		if migrateCmd == "" {
			return cliErr(cmd, &CliError{
				Message: "No migrate command configured.",
				Hint:    "Set 'commands.migrate' in .treeline.yml (e.g. bin/rails db:migrate).",
			})
		}

		mergeTarget := pc.MergeTarget()
		if mergeTarget == "" {
			mergeTarget = resolveRemoteDefaultBranch(mainRepo)
		}

		if err := checkCleanWorktree(mainRepo); err != nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Root repo has uncommitted changes: %s", err),
				Hint:    "Commit or stash changes in the root repo before updating the template database.",
			})
		}

		fmt.Printf("==> Pulling %s in %s\n", mergeTarget, mainRepo)
		pull := exec.Command("git", "pull", "origin", mergeTarget)
		pull.Dir = mainRepo
		pull.Stdout = os.Stdout
		pull.Stderr = os.Stderr
		if err := pull.Run(); err != nil {
			return fmt.Errorf("git pull: %w", err)
		}

		fmt.Printf("==> Running: %s\n", migrateCmd)
		migrate := exec.Command("sh", "-c", migrateCmd)
		migrate.Dir = mainRepo
		migrate.Stdout = os.Stdout
		migrate.Stderr = os.Stderr
		if err := migrate.Run(); err != nil {
			return fmt.Errorf("migrate command failed: %w", err)
		}

		fmt.Println("==> Template database updated. Run 'gtl db reset' in any worktree to re-clone.")
		return nil
	},
}

// resolveRemoteDefaultBranch reads origin/HEAD to find the remote's default
// branch name. Falls back to "main" if the symbolic ref is absent or unset
// (e.g. remote added manually, shallow clone, or stale after a branch rename).
func resolveRemoteDefaultBranch(dir string) string {
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	// output is "refs/remotes/origin/<branch>\n"
	ref := strings.TrimSpace(string(out))
	const prefix = "refs/remotes/origin/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return "main"
}

// checkCleanWorktree returns an error if the git working tree at dir has
// uncommitted changes (staged or unstaged). Untracked files are ignored.
func checkCleanWorktree(dir string) error {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("checking working tree: %w", err)
	}
	if len(out) > 0 {
		return fmt.Errorf("working tree is not clean")
	}
	return nil
}
