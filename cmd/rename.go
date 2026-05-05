package cmd

import (
	"fmt"
	"os"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
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

		dropDatabases(pc.DatabaseAdapter(), entries)

		var paths []string
		for _, e := range entries {
			wt := registry.GetString(e, "worktree")
			if wt == "" {
				continue
			}
			if _, err := reg.Release(wt); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: failed to release %s: %v\n", wt, err)
			}
			if info, statErr := os.Stat(wt); statErr == nil && info.IsDir() {
				paths = append(paths, wt)
			}
		}

		successes := 0
		var failures []string
		if len(paths) > 0 {
			fmt.Println()
			fmt.Println("Reallocating worktrees under new project name...")
		}
		for _, p := range paths {
			fmt.Printf("\n→ %s\n", p)
			s := setup.New(p, "", uc)
			if _, err := s.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ %v\n", err)
				failures = append(failures, p)
				continue
			}
			successes++
		}

		fmt.Println()
		fmt.Printf("Rename complete: %d worktree(s) reallocated", successes)
		if len(failures) > 0 {
			fmt.Printf(", %d failed", len(failures))
		}
		fmt.Println(".")
		if len(failures) > 0 {
			return fmt.Errorf("%d reallocation(s) failed", len(failures))
		}
		return nil
	},
}

// dropDatabases drops every database name listed in the registry entries,
// using the configured adapter. Errors are reported but don't abort the
// rename — the worktree re-allocation step will surface any real problem.
func dropDatabases(adapterName string, entries []registry.Allocation) {
	if len(entries) == 0 {
		return
	}
	adapter, err := database.ForAdapter(adapterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		return
	}
	for _, e := range entries {
		dbName := registry.GetString(e, "database")
		if dbName == "" {
			continue
		}
		exists, err := adapter.Exists(dbName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: checking %s: %v\n", dbName, err)
			continue
		}
		if !exists {
			continue
		}
		fmt.Printf("==> Dropping %s\n", dbName)
		if err := adapter.Drop(dbName); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: dropping %s: %v\n", dbName, err)
		}
	}
}
