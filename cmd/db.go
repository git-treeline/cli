package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var dbResetFrom string
var dbNameJSON bool
var (
	dbDropForce     bool
	dbDropDryRun    bool
	dbResetForce    bool
	dbResetDryRun   bool
	dbRestoreForce  bool
	dbRestoreDryRun bool
)

func init() {
	dbResetCmd.Flags().StringVar(&dbResetFrom, "from", "", "Clone from this database instead of the configured template")
	dbNameCmd.Flags().BoolVar(&dbNameJSON, "json", false, "Output as JSON")
	dbDropCmd.Flags().BoolVarP(&dbDropForce, "force", "f", false, "Skip the confirmation prompt")
	dbDropCmd.Flags().BoolVar(&dbDropDryRun, "dry-run", false, "Print what would happen without making changes")
	dbResetCmd.Flags().BoolVarP(&dbResetForce, "force", "f", false, "Skip the confirmation prompt")
	dbResetCmd.Flags().BoolVar(&dbResetDryRun, "dry-run", false, "Print what would happen without making changes")
	dbRestoreCmd.Flags().BoolVarP(&dbRestoreForce, "force", "f", false, "Skip the confirmation prompt")
	dbRestoreCmd.Flags().BoolVar(&dbRestoreDryRun, "dry-run", false, "Print what would happen without making changes")
	dbPullCmd.Flags().BoolVarP(&dbPullForce, "force", "f", false, "Skip the confirmation prompt")
	dbPullCmd.Flags().BoolVar(&dbPullDryRun, "dry-run", false, "Resolve and print the plan without making changes")
	dbPullCmd.Flags().BoolVar(&dbPullDebug, "debug", false, "Echo each command (passwords redacted)")
	dbRefreshCmd.Flags().BoolVarP(&dbRefreshForce, "force", "f", false, "Skip the confirmation prompt")
	dbRefreshCmd.Flags().BoolVar(&dbRefreshDebug, "debug", false, "Echo each command (passwords redacted)")
	dbCmd.AddCommand(dbResetCmd)
	dbCmd.AddCommand(dbRestoreCmd)
	dbCmd.AddCommand(dbNameCmd)
	dbCmd.AddCommand(dbDropCmd)
	dbCmd.AddCommand(dbPullCmd)
	dbCmd.AddCommand(dbRefreshCmd)
	rootCmd.AddCommand(dbCmd)
}

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Manage the worktree's database",
}

var dbNameCmd = &cobra.Command{
	Use:   "name",
	Short: "Print the worktree's database name",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := resolveDB()
		if err != nil {
			return err
		}
		if dbNameJSON {
			data, err := json.MarshalIndent(map[string]string{
				"database": info.target,
			}, "", "  ")
			if err != nil {
				return fmt.Errorf("encoding database name: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}
		fmt.Println(info.target)
		return nil
	},
}

var dbDropCmd = &cobra.Command{
	Use:   "drop",
	Short: "Drop the worktree's database",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := resolveDB()
		if err != nil {
			return err
		}
		exists, err := info.adapter.Exists(info.target)
		if err != nil {
			return err
		}
		if !exists {
			fmt.Printf("Database %s does not exist\n", info.target)
			return nil
		}
		if dbDropDryRun {
			fmt.Printf("Would drop database %s. (dry-run)\n", info.target)
			return nil
		}
		prompt := fmt.Sprintf("This will DROP worktree db '%s'.", info.target)
		if !confirm.Prompt(prompt, dbDropForce, nil) {
			fmt.Println("Aborted.")
			return nil
		}
		if err := info.adapter.Drop(info.target); err != nil {
			return fmt.Errorf("dropping database: %w", err)
		}
		fmt.Printf("Dropped %s\n", info.target)
		return nil
	},
}

var dbResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Drop and re-clone the worktree's database from the template",
	Long: `Drop the worktree database and re-clone it from the template configured
in .treeline.yml. Use --from to clone from a different database instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := resolveDB()
		if err != nil {
			return err
		}

		source := info.template
		if dbResetFrom != "" {
			source = dbResetFrom
		}
		if source == "" {
			return cliErr(cmd, &CliError{
				Message: "No template database configured and no --from specified.",
				Hint:    "Set 'database.template' in .treeline.yml, or pass --from <db_name>.",
			})
		}

		if dbResetDryRun {
			fmt.Printf("Would drop %s and re-clone from %s. (dry-run)\n", info.target, source)
			return nil
		}
		prompt := fmt.Sprintf("This will DROP and re-clone worktree db '%s' from %s.", info.target, source)
		if !confirm.Prompt(prompt, dbResetForce, nil) {
			fmt.Println("Aborted.")
			return nil
		}

		fmt.Printf("==> Dropping %s\n", info.target)
		if err := info.adapter.Drop(info.target); err != nil {
			return fmt.Errorf("dropping database: %w", err)
		}

		fmt.Printf("==> Cloning %s → %s\n", source, info.target)
		if err := info.adapter.Clone(source, info.target); err != nil {
			return err
		}

		fmt.Printf("==> Done. Database %s ready.\n", info.target)
		return nil
	},
}

var dbRestoreCmd = &cobra.Command{
	Use:   "restore <dumpfile>",
	Short: "Drop and restore the worktree's database from a dump file",
	Long: `Drop the worktree database, create a fresh one, and restore from a
pg_dump file. Supports both custom format and plain SQL dumps.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dumpFile := args[0]
		if _, err := os.Stat(dumpFile); err != nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Dump file not found: %s", dumpFile),
				Hint:    "Check the path — expected a pg_dump output file (custom or plain SQL).",
			})
		}

		info, err := resolveDB()
		if err != nil {
			return err
		}

		if dbRestoreDryRun {
			fmt.Printf("Would drop %s and restore from %s. (dry-run)\n", info.target, dumpFile)
			return nil
		}
		prompt := fmt.Sprintf("This will DROP and restore worktree db '%s' from %s.", info.target, dumpFile)
		if !confirm.Prompt(prompt, dbRestoreForce, nil) {
			fmt.Println("Aborted.")
			return nil
		}

		fmt.Printf("==> Dropping %s\n", info.target)
		if err := info.adapter.Drop(info.target); err != nil {
			return fmt.Errorf("dropping database: %w", err)
		}

		fmt.Printf("==> Restoring %s from %s\n", info.target, dumpFile)
		if err := info.adapter.Restore(info.target, dumpFile); err != nil {
			return err
		}

		fmt.Printf("==> Done. Database %s restored.\n", info.target)
		return nil
	},
}

type dbInfo struct {
	target      string
	template    string
	adapter     database.Adapter
	adapterName string
	worktreeDir string
}

// resolveDBPaths returns the resolved target and template paths for a database
// adapter. SQLite paths are made absolute; PostgreSQL names pass through as-is.
func resolveDBPaths(adapterName, absPath, mainRepo, dbName, template string) (target, tmpl string) {
	target = dbName
	if adapterName == "sqlite" {
		target = filepath.Join(absPath, dbName)
	}
	tmpl = template
	if adapterName == "sqlite" && template != "" {
		tmpl = filepath.Join(mainRepo, template)
	}
	return target, tmpl
}

func resolveDB() (*dbInfo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	absPath, _ := filepath.Abs(cwd)
	mainRepo := worktree.DetectMainRepo(absPath)
	pc := config.LoadProjectConfig(absPath)

	reg := registry.New("")
	alloc := reg.Find(absPath)
	if alloc == nil {
		return nil, errNoAllocation(absPath)
	}

	dbName, _ := alloc["database"].(string)
	if dbName == "" {
		return nil, errNoDatabaseConfigured()
	}

	adapterName := pc.DatabaseAdapter()
	adapter, err := database.ForAdapter(adapterName, pc.DatabaseConnArgs())
	if err != nil {
		return nil, err
	}

	template := pc.DatabaseTemplate()
	target, tmpl := resolveDBPaths(adapterName, absPath, mainRepo, dbName, template)

	return &dbInfo{
		target:      target,
		template:    tmpl,
		adapter:     adapter,
		adapterName: adapterName,
		worktreeDir: absPath,
	}, nil
}
