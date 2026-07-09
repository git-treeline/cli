package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	pruneStale           bool
	pruneMerged          bool
	pruneDropDB          bool
	pruneForce           bool
	pruneRemoveWorktree  bool
)

func init() {
	pruneCmd.Flags().BoolVar(&pruneStale, "stale", false, "Also remove allocations for directories not listed in git worktree list")
	pruneCmd.Flags().BoolVar(&pruneMerged, "merged", false, "Remove allocations for worktrees on branches merged to main")
	pruneCmd.Flags().BoolVar(&pruneDropDB, "drop-db", false, "Also drop databases for pruned allocations")
	pruneCmd.Flags().BoolVar(&pruneRemoveWorktree, "remove-worktree", false, "Also remove the git worktree directories")
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

		candidates := prunableAllocations(reg)
		if len(candidates) == 0 && !pruneStale {
			fmt.Println("Nothing to prune.")
			return nil
		}

		if len(candidates) > 0 {
			fmt.Printf("This will prune %d allocation(s) whose worktree no longer exists:\n", len(candidates))
			for _, a := range candidates {
				fa := format.Allocation(a)
				name := format.DisplayName(fa)
				project := format.GetStr(fa, "project")
				db := format.GetStr(fa, "database")
				line := fmt.Sprintf("  %s:%s", project, name)
				if db != "" {
					line += fmt.Sprintf("  db:%s", db)
				}
				fmt.Println(line)
			}
		} else {
			fmt.Println("This will prune any stale allocations from the registry.")
		}

		if !confirm.Prompt("Prune these allocations?", pruneForce, nil) {
			fmt.Println("Aborted.")
			return nil
		}

		var count int
		var err error
		if pruneStale {
			count, err = reg.PruneStale()
		} else {
			count, err = reg.Prune()
		}

		if err != nil {
			return err
		}

		if count == 0 {
			fmt.Println("Nothing to prune.")
		} else {
			fmt.Printf("Pruned %d stale allocation(s).\n", count)
		}
		return nil
	},
}

// prunableAllocations returns the registered allocations whose worktree
// directory no longer exists on disk — the set that a default 'prune' removes.
// It is used to preview the destructive operation before confirming; for
// '--stale' the actual pruned set may also include git worktrees no longer
// registered with their parent repo.
func prunableAllocations(reg *registry.Registry) []registry.Allocation {
	var out []registry.Allocation
	for _, a := range reg.Allocations() {
		wt := registry.GetString(a, "worktree")
		if wt == "" {
			continue
		}
		if _, err := os.Stat(wt); err != nil {
			out = append(out, a)
		}
	}
	return out
}

func runPruneMerged() error {
	cwd, _ := os.Getwd()
	repoPath := worktree.DetectMainRepo(cwd)
	pc := config.LoadProjectConfig(repoPath)

	mergedBranches, err := worktree.MergedBranches(repoPath, pc.MergeTarget())
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
		fa := format.Allocation(a)
		port := format.PortDisplay(fa)
		name := format.DisplayName(fa)
		project := format.GetStr(fa, "project")
		db := format.GetStr(fa, "database")
		line := fmt.Sprintf("  %s:%s  %s", project, name, port)
		if db != "" {
			line += fmt.Sprintf("  db:%s", db)
		}
		fmt.Println(line)

		if wt := format.GetStr(fa, "worktree"); wt != "" {
			if _, err := os.Stat(wt); err == nil {
				if pruneRemoveWorktree {
					fmt.Printf("    (will remove worktree at %s)\n", wt)
				} else {
					fmt.Printf("    (worktree dir still exists at %s — remove with: git worktree remove %s)\n", wt, filepath.Base(wt))
				}
			}
		}
	}

	if !confirm.Prompt("Release these allocations?", pruneForce, nil) {
		fmt.Println("Aborted.")
		return nil
	}

	if pruneDropDB {
		formatAllocs := make([]format.Allocation, len(matches))
		for i, a := range matches {
			formatAllocs[i] = format.Allocation(a)
		}
		format.DropDatabases(formatAllocs)
	}

	paths := make([]string, 0, len(matches))
	for _, a := range matches {
		paths = append(paths, format.GetStr(format.Allocation(a), "worktree"))
	}

	count, err := reg.ReleaseMany(paths)
	if err != nil {
		return err
	}

	if pruneRemoveWorktree {
		for _, p := range paths {
			removeWorktreeDir(p, pruneForce)
		}
	}

	fmt.Printf("Released %d allocation(s).\n", count)
	return nil
}
