package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/database"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var dbResetFrom string
var dbNameJSON bool

func init() {
	dbResetCmd.Flags().StringVar(&dbResetFrom, "from", "", "Clone from this database instead of the configured template")
	dbNameCmd.Flags().BoolVar(&dbNameJSON, "json", false, "Output as JSON")
	dbCmd.AddCommand(dbResetCmd)
	dbCmd.AddCommand(dbRestoreCmd)
	dbCmd.AddCommand(dbNameCmd)
	dbCmd.AddCommand(dbDropCmd)
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
			return &CliError{
				Message: "No template database configured and no --from specified.",
				Hint:    "Set 'database.template' in .treeline.yml, or pass --from <db_name>.",
			}
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
			return &CliError{
				Message: fmt.Sprintf("Dump file not found: %s", dumpFile),
				Hint:    "Check the path — expected a pg_dump output file (custom or plain SQL).",
			}
		}

		info, err := resolveDB()
		if err != nil {
			return err
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
	target   string
	template string
	adapter  database.Adapter
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
	adapter, err := database.ForAdapter(adapterName)
	if err != nil {
		return nil, err
	}

	template := pc.DatabaseTemplate()
	target, tmpl := resolveDBPaths(adapterName, absPath, mainRepo, dbName, template)

	return &dbInfo{
		target:   target,
		template: tmpl,
		adapter:  adapter,
	}, nil
}
