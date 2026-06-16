package cmd

import (
	"fmt"
	"os"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var renameYes bool

func init() {
	renameCmd.Flags().BoolVarP(&renameYes, "yes", "y", false, "Skip confirmation prompt")
	rootCmd.AddCommand(renameCmd)
}

var renameCmd = &cobra.Command{
	Use:   "rename <new-name>",
	Short: "Rename the project, migrating registry, port reservations, and databases",
	Long: `Renames the project across .treeline.yml, the global registry, and
user-config keys (port reservations, editor overrides). Worktree databases
under the old name are dropped, then re-cloned from the template under the
new name. Existing worktrees keep their port reservations where possible.

Project names must match [a-zA-Z_][a-zA-Z0-9_]* — same rule as Postgres
identifiers, since the project name flows into databases, redis prefixes,
and router keys.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		newName := args[0]
		if !config.IsValidIdentifier(newName) {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("invalid project name %q", newName),
				Hint: fmt.Sprintf("Project names must match [a-zA-Z_][a-zA-Z0-9_]* (no dashes, dots, spaces).\n"+
					"  Try: gtl rename %s", config.SanitizeIdentifier(newName)),
			})
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		mainRepo := worktree.DetectMainRepo(cwd)
		pc := config.LoadProjectConfig(mainRepo)
		if !pc.Exists() {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("no %s found at %s", config.ProjectConfigFile, mainRepo),
				Hint:    "Run gtl rename inside a treeline project (the main repo or any worktree).",
			})
		}

		oldName := pc.Project()
		if oldName == newName {
			fmt.Printf("Project is already named %q. Nothing to do.\n", newName)
			return nil
		}

		uc := config.LoadUserConfig("")
		reg := registry.New("")
		entries := reg.FindByProject(oldName)

		fmt.Printf("Renaming project: %s → %s\n", oldName, newName)
		if len(entries) > 0 {
			fmt.Println()
			fmt.Printf("This drops and re-clones databases for %d worktree(s):\n", len(entries))
			for _, e := range entries {
				wt := registry.GetString(e, "worktree")
				db := registry.GetString(e, "database")
				if db != "" {
					fmt.Printf("  - %s  (drops %s)\n", wt, db)
				} else {
					fmt.Printf("  - %s\n", wt)
				}
			}
		}
		fmt.Println()

		if !renameYes {
			if !confirm.Prompt(fmt.Sprintf("Proceed with rename to %q?", newName), false, nil) {
				fmt.Println("Aborted.")
				return nil
			}
		}

		if err := pc.SetProject(newName); err != nil {
			return fmt.Errorf("rewriting %s: %w", config.ProjectConfigFile, err)
		}
		fmt.Println(style.Actionf("Updated %s", config.ProjectConfigFile))

		if migrated := uc.MigrateProjectKeys(oldName, newName); migrated > 0 {
			if err := uc.Save(); err != nil {
				return fmt.Errorf("saving user config: %w", err)
			}
			fmt.Println(style.Actionf("Migrated %d user-config key(s)", migrated))
		}

		if len(entries) > 0 {
			fmt.Println()
			fmt.Println("Commit this change and rebase each worktree, then run `gtl setup` to re-provision:")
			for _, e := range entries {
				wt := registry.GetString(e, "worktree")
				if wt != "" {
					fmt.Printf("  %s\n", wt)
				}
			}
		}

		return nil
	},
}
