package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/setup"
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
	Short: "Update the root template database from the remote merge target",
	Long: `Fetches the merge target branch and runs commands.migrate in a temporary,
detached checkout. Existing checkout branches and worktree databases are unchanged.

The env mapping must use {database}, {database.name}, or {database_1}; these
resolve to database.template for this operation. Configured copy_files and the
root repo's env file seed the checkout. Setup commands and hooks are not run;
commands.migrate must prepare any dependencies it needs in a fresh checkout.

Run 'gtl db reset' in a worktree to re-clone from the updated template.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		mainRepo := worktree.DetectMainRepo(cwd)
		pc := config.LoadProjectConfigReadOnly(worktree.DetectRepoRoot(cwd))

		migrateCmd := pc.MigrateCommand()
		if migrateCmd == "" {
			return cliErr(cmd, &CliError{
				Message: "No migrate command configured.",
				Hint:    "Set 'commands.migrate' in .treeline.yml (e.g. bin/rails db:migrate).",
			})
		}

		if err := setup.UpdateTemplateDatabase(cmd.Context(), mainRepo, pc, cmd.OutOrStdout()); err != nil {
			var configErr *setup.TemplateConfigError
			if errors.As(err, &configErr) {
				return cliErr(cmd, &CliError{Message: configErr.Message, Hint: configErr.Hint, Cause: err})
			}
			return cliErr(cmd, err)
		}

		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "==> Template database updated. Run 'gtl db reset' in any worktree to re-clone.")
		return nil
	},
}
