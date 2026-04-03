package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/confirm"
	"github.com/git-treeline/git-treeline/internal/database"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/spf13/cobra"
)

var (
	releaseDropDB  bool
	releaseProject string
	releaseAll     bool
	releaseForce   bool
	releaseDryRun  bool
)

func init() {
	releaseCmd.Flags().BoolVar(&releaseDropDB, "drop-db", false, "Also drop the database")
	releaseCmd.Flags().StringVar(&releaseProject, "project", "", "Release all allocations for a project")
	releaseCmd.Flags().BoolVar(&releaseAll, "all", false, "Release all allocations across all projects")
	releaseCmd.Flags().BoolVarP(&releaseForce, "force", "f", false, "Skip confirmation prompt")
	releaseCmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "Show what would be released without doing it")
	rootCmd.AddCommand(releaseCmd)
}

var releaseCmd = &cobra.Command{
	Use:   "release [PATH]",
	Short: "Release allocated resources for a worktree",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		modes := 0
		if len(args) > 0 {
			modes++
		}
		if releaseProject != "" {
			modes++
		}
		if releaseAll {
			modes++
		}
		if modes > 1 {
			return fmt.Errorf("PATH, --project, and --all are mutually exclusive; use only one")
		}

		if releaseProject != "" {
			return runReleaseBatch(releaseProject, false)
		}
		if releaseAll {
			return runReleaseBatch("", true)
		}

		return runReleaseSingle(args)
	},
}

func runReleaseSingle(args []string) error {
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
		dropSingleDB(alloc, absPath)
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
}

func runReleaseBatch(project string, all bool) error {
	reg := registry.New("")

	var allocs []registry.Allocation
	if all {
		allocs = reg.Allocations()
	} else {
		allocs = reg.FindByProject(project)
	}

	if len(allocs) == 0 {
		if all {
			fmt.Println("No allocations found.")
		} else {
			fmt.Printf("No allocations for project %q.\n", project)
		}
		return nil
	}

	// Collect unique projects for summary
	projects := make(map[string]bool)
	for _, a := range allocs {
		if p, ok := a["project"].(string); ok {
			projects[p] = true
		}
	}

	if all {
		fmt.Printf("This will release ALL %d allocation(s) across %d project(s):\n", len(allocs), len(projects))
	} else {
		fmt.Printf("This will release %d allocation(s) for %s:\n", len(allocs), project)
	}

	for _, a := range allocs {
		ports := getPorts(a)
		name, _ := a["worktree_name"].(string)
		db, _ := a["database"].(string)
		proj, _ := a["project"].(string)

		line := fmt.Sprintf("  :%d  %s", ports[0], name)
		if all {
			line = fmt.Sprintf("  [%s] :%d  %s", proj, ports[0], name)
		}
		if db != "" {
			line += fmt.Sprintf("  db:%s", db)
		}
		fmt.Println(line)

		if wt, ok := a["worktree"].(string); ok && wt != "" {
			if _, err := os.Stat(wt); err == nil {
				fmt.Printf("    (worktree dir still exists at %s)\n", wt)
			}
		}
	}

	if releaseDryRun {
		fmt.Printf("\nWould release %d allocation(s). (dry-run)\n", len(allocs))
		return nil
	}

	if !confirm.Prompt("Release all?", releaseForce, nil) {
		fmt.Println("Aborted.")
		return nil
	}

	if releaseDropDB {
		dropDatabases(allocs)
	}

	paths := make([]string, 0, len(allocs))
	for _, a := range allocs {
		if wt, ok := a["worktree"].(string); ok {
			paths = append(paths, wt)
		}
	}

	count, err := reg.ReleaseMany(paths)
	if err != nil {
		return err
	}

	fmt.Printf("Released %d allocation(s).\n", count)
	return nil
}

func dropSingleDB(alloc registry.Allocation, worktreePath string) {
	db, ok := alloc["database"].(string)
	if !ok || db == "" {
		return
	}
	adapterName, _ := alloc["database_adapter"].(string)
	adapter, err := database.ForAdapter(adapterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s, skipping database drop\n", err)
		return
	}
	dropTarget := db
	if adapterName == "sqlite" {
		dropTarget = filepath.Join(worktreePath, db)
	}
	fmt.Printf("==> Dropping database %s\n", db)
	if err := adapter.Drop(dropTarget); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to drop database: %s\n", err)
	}
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
