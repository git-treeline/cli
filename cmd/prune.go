package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/git-treeline/internal/confirm"
	"github.com/git-treeline/git-treeline/internal/database"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	pruneStale  bool
	pruneMerged bool
	pruneDropDB bool
	pruneForce  bool
)

func init() {
	pruneCmd.Flags().BoolVar(&pruneStale, "stale", false, "Also remove allocations for directories not listed in git worktree list")
	pruneCmd.Flags().BoolVar(&pruneMerged, "merged", false, "Remove allocations for worktrees on branches merged to main")
	pruneCmd.Flags().BoolVar(&pruneDropDB, "drop-db", false, "Also drop databases for pruned allocations")
	pruneCmd.Flags().BoolVar(&pruneForce, "force", false, "Skip confirmation prompt")
	rootCmd.AddCommand(pruneCmd)
}

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove allocations for worktrees that no longer exist on disk",
	RunE: func(cmd *cobra.Command, args []string) error {
		if pruneMerged {
			return runPruneMerged()
		}

		reg := registry.New("")

		var count int
		var err error
		if pruneStale {
			count, err = reg.PruneStale()
		} else {
			count, err = reg.Prune()
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}

		if count == 0 {
			fmt.Println("Nothing to prune.")
		} else {
			fmt.Printf("Pruned %d stale allocation(s).\n", count)
		}
		return nil
	},
}

func runPruneMerged() error {
	cwd, _ := os.Getwd()
	repoPath := worktree.DetectMainRepo(cwd)

	mergedBranches, err := worktree.MergedBranches(repoPath)
	if err != nil {
		return fmt.Errorf("failed to detect merged branches: %w", err)
	}

	if len(mergedBranches) == 0 {
		fmt.Println("No merged branches found.")
		return nil
	}

	wtBranches := worktree.WorktreeBranches(repoPath)
	reg := registry.New("")
	matches := reg.FindMergedAllocations(mergedBranches, wtBranches)

	if len(matches) == 0 {
		fmt.Println("No allocations on merged branches.")
		return nil
	}

	fmt.Printf("Found %d allocation(s) on merged branches:\n", len(matches))
	for _, a := range matches {
		port := getPortDisplay(a)
		name := getStr(a, "worktree_name")
		project := getStr(a, "project")
		db := getStr(a, "database")
		line := fmt.Sprintf("  %s:%s  %s", project, name, port)
		if db != "" {
			line += fmt.Sprintf("  db:%s", db)
		}
		fmt.Println(line)

		// Note if worktree dir still exists
		if wt := getStr(a, "worktree"); wt != "" {
			if _, err := os.Stat(wt); err == nil {
				fmt.Printf("    (worktree dir still exists at %s — remove with: git worktree remove %s)\n", wt, filepath.Base(wt))
			}
		}
	}

	if !confirm.Prompt("Release these allocations?", pruneForce, nil) {
		fmt.Println("Aborted.")
		return nil
	}

	if pruneDropDB {
		dropDatabases(matches)
	}

	paths := make([]string, 0, len(matches))
	for _, a := range matches {
		paths = append(paths, getStr(a, "worktree"))
	}

	count, err := reg.ReleaseMany(paths)
	if err != nil {
		return err
	}

	fmt.Printf("Released %d allocation(s).\n", count)
	return nil
}

func dropDatabases(allocs []registry.Allocation) {
	for _, a := range allocs {
		db := getStr(a, "database")
		if db == "" {
			continue
		}
		adapterName := getStr(a, "database_adapter")
		adapter, err := database.ForAdapter(adapterName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s, skipping database drop for %s\n", err, db)
			continue
		}
		dropTarget := db
		if adapterName == "sqlite" {
			dropTarget = filepath.Join(getStr(a, "worktree"), db)
		}
		fmt.Printf("==> Dropping database %s\n", db)
		if err := adapter.Drop(dropTarget); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to drop database %s: %s\n", db, err)
		}
	}
}

func getStr(a registry.Allocation, key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}

func getPortDisplay(a registry.Allocation) string {
	ports := getPorts(a)
	if len(ports) > 0 {
		return fmt.Sprintf(":%d", ports[0])
	}
	return ""
}
