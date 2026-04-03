package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/database"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/spf13/cobra"
)

var releaseDropDB bool

func init() {
	releaseCmd.Flags().BoolVar(&releaseDropDB, "drop-db", false, "Also drop the PostgreSQL database")
	rootCmd.AddCommand(releaseCmd)
}

var releaseCmd = &cobra.Command{
	Use:   "release [PATH]",
	Short: "Release allocated resources for a worktree",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		absPath, _ := filepath.Abs(path)

		reg := registry.New("")
		alloc := reg.Find(absPath)
		if alloc == nil {
			fmt.Fprintf(os.Stderr, "No allocation found for %s\n", absPath)
			os.Exit(1)
		}

		if releaseDropDB {
			if db, ok := alloc["database"].(string); ok && db != "" {
				adapterName, _ := alloc["database_adapter"].(string)
				adapter, err := database.ForAdapter(adapterName)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: %s, skipping database drop\n", err)
				} else {
					dropTarget := db
					// SQLite paths are relative to the worktree directory
					if adapterName == "sqlite" {
						dropTarget = filepath.Join(absPath, db)
					}
					fmt.Printf("==> Dropping database %s\n", db)
					if err := adapter.Drop(dropTarget); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to drop database: %s\n", err)
					}
				}
			}
		}

		_, _ = reg.Release(absPath)
		fmt.Printf("==> Released resources for %s\n", filepath.Base(absPath))

		ports := getPorts(alloc)
		if len(ports) > 1 {
			fmt.Printf("  Ports:    %s\n", joinInts(ports, ", "))
		} else if len(ports) == 1 {
			fmt.Printf("  Port:     %d\n", ports[0])
		}
		if db, ok := alloc["database"].(string); ok && db != "" {
			fmt.Printf("  Database: %s\n", db)
		}

		return nil
	},
}

func getPorts(a registry.Allocation) []int {
	if ps, ok := a["ports"].([]any); ok {
		result := make([]int, 0, len(ps))
		for _, p := range ps {
			if f, ok := p.(float64); ok {
				result = append(result, int(f))
			}
		}
		return result
	}
	if p, ok := a["port"].(float64); ok {
		return []int{int(p)}
	}
	return nil
}

func joinInts(ints []int, sep string) string {
	parts := make([]string, len(ints))
	for i, v := range ints {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return strings.Join(parts, sep)
}
