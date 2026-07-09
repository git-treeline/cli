package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/style"
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

		// Snapshot before pruning so we can tear down runtime state (supervisor
		// + managed hosts entry) for whatever gets removed. Prune/PruneStale
		// return only a count, and the removed set differs by variant, so a
		// before/after diff keeps this independent of which path ran.
		before := reg.Allocations()

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
			removed := removedAllocations(before, reg.Allocations())
			teardownRuntimeState(removed)
			fmt.Printf("Pruned %d stale allocation(s).\n", count)
		}

		gcDanglingEdges(reg)
		surfaceOrphanedBranches(reg)
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
		if err := format.DropDatabases(formatAllocs); err != nil {
			return err
		}
	}

	paths := make([]string, 0, len(matches))
	for _, a := range matches {
		paths = append(paths, format.GetStr(format.Allocation(a), "worktree"))
	}

	count, err := reg.ReleaseMany(paths)
	if err != nil {
		return err
	}

	teardownRuntimeState(matches)

	if pruneRemoveWorktree {
		for _, p := range paths {
			removeWorktreeDir(p, pruneForce)
		}
	}

	fmt.Printf("Released %d allocation(s).\n", count)
	return nil
}

// gcDanglingEdges reclaims relationship edges that no longer point at any live
// worktree on either end. Endpoints resolve through the same (repo, branch)
// index that `gtl status`/`gtl related` use, so an edge is dropped only when
// neither side is currently checked out anywhere in the registry. Best-effort:
// failures are reported but never block the prune.
func gcDanglingEdges(reg *registry.Registry) {
	if len(reg.AllEdges()) == 0 {
		return
	}
	idx := buildWorktreeIndex(reg.Allocations())
	resolvable := func(ref registry.RepoRef) bool {
		_, ok := idx.pathByRef[ref]
		return ok
	}
	removed, err := reg.GCDanglingEdges(resolvable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not garbage-collect edges: %s\n", err)
		return
	}
	if len(removed) > 0 {
		fmt.Printf("Removed %d dangling relationship edge(s).\n", len(removed))
	}
}

// surfaceOrphanedBranches warns about allocations whose directory survives but
// whose branch was deleted out from under it — the route now points at a dead
// branch. Prune can't safely remove these (the worktree dir is still there and
// may hold work), so this is informational: it tells the user where to look.
func surfaceOrphanedBranches(reg *registry.Registry) {
	orphaned := reg.OrphanedBranchAllocations()
	if len(orphaned) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(style.Warnf("%d allocation(s) point at a deleted branch (worktree dir still present):", len(orphaned)))
	for _, a := range orphaned {
		fa := format.Allocation(a)
		fmt.Printf("  %s:%s  %s\n", format.GetStr(fa, "project"), format.DisplayName(fa), format.GetStr(fa, "worktree"))
	}
	fmt.Println(style.Dimf("  Recreate the branch, or release with: gtl release <path>"))
}

// removedAllocations returns the entries present in before but absent from
// after, matched by worktree path. Used to recover the set pruned by
// Prune/PruneStale (which report only a count) so their runtime state can be
// torn down.
func removedAllocations(before, after []registry.Allocation) []registry.Allocation {
	survived := make(map[string]bool, len(after))
	for _, a := range after {
		survived[format.GetStr(format.Allocation(a), "worktree")] = true
	}
	var removed []registry.Allocation
	for _, a := range before {
		if !survived[format.GetStr(format.Allocation(a), "worktree")] {
			removed = append(removed, a)
		}
	}
	return removed
}
