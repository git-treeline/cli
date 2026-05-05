package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/spf13/cobra"
)

var (
	reallocateFrom    string
	reallocateApply   bool
	reallocateAllReg  bool
)

func init() {
	reallocateCmd.Flags().StringVar(&reallocateFrom, "from", "", "Scan this directory for treeline projects (recursive, depth 3)")
	reallocateCmd.Flags().BoolVar(&reallocateApply, "apply", false, "Actually run setup on each candidate (default: dry-run)")
	reallocateCmd.Flags().BoolVar(&reallocateAllReg, "all-registry", false, "Re-run setup for every entry currently in the registry whose directory still exists")
	rootCmd.AddCommand(reallocateCmd)
}

var reallocateCmd = &cobra.Command{
	Use:   "reallocate [path...]",
	Short: "Re-allocate ports/databases for one or many worktrees",
	Long: `Re-runs the allocation pipeline (port + database + Redis assignment, env
file write) for one or many directories. Use this to recover from registry
loss or corruption — for example after 'gtl prune --stale' incorrectly
removed standalone Conductor clones, or after manually editing
registry.json went sideways.

Modes:
  gtl reallocate                        # the current directory only
  gtl reallocate <path1> <path2> ...    # explicit paths
  gtl reallocate --from ~/conductor/workspaces  # scan a parent
  gtl reallocate --all-registry         # every existing registry entry

By default this is dry-run — pass --apply to actually run setup. Each
target must be an existing directory containing a .treeline.yml.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := collectReallocateTargets(args, reallocateFrom, reallocateAllReg)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			fmt.Println("No targets found.")
			return nil
		}

		fmt.Printf("Found %d candidate worktree(s):\n", len(paths))
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}

		if !reallocateApply {
			fmt.Println()
			fmt.Println("Dry run — pass --apply to run setup on each.")
			return nil
		}

		uc := config.LoadUserConfig("")
		successes := 0
		var failures []string
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
		fmt.Printf("Reallocated: %d. Failed: %d.\n", successes, len(failures))
		if len(failures) > 0 {
			for _, p := range failures {
				fmt.Printf("  ✗ %s\n", p)
			}
			return fmt.Errorf("%d reallocation(s) failed", len(failures))
		}
		return nil
	},
}

// collectReallocateTargets resolves the input arguments to a deduplicated,
// sorted list of absolute paths. Each path must already exist and contain a
// .treeline.yml (otherwise it can't be reallocated meaningfully).
func collectReallocateTargets(args []string, from string, allRegistry bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if seen[abs] {
			return
		}
		if !hasProjectConfig(abs) {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}

	if allRegistry {
		reg := registry.New("")
		for _, a := range reg.Allocations() {
			wt := registry.GetString(a, "worktree")
			if wt == "" {
				continue
			}
			if info, err := os.Stat(wt); err == nil && info.IsDir() {
				add(wt)
			}
		}
	}

	if from != "" {
		matches, err := scanForTreelineProjects(from)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			add(m)
		}
	}

	if len(args) == 0 && from == "" && !allRegistry {
		// Default to current directory.
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		add(cwd)
	}
	for _, a := range args {
		add(a)
	}

	sort.Strings(out)
	return out, nil
}

// scanForTreelineProjects walks <root> up to 3 levels deep looking for
// directories that contain a .treeline.yml. Conductor's standard layout
// (~/conductor/workspaces/<project>/<workspace>) lives at depth 2.
func scanForTreelineProjects(root string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("scan root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	const maxDepth = 3
	var found []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if hasProjectConfig(dir) {
			found = append(found, dir)
			return // don't descend into a treeline project
		}
		if depth >= maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if e.Name() == ".git" || e.Name() == "node_modules" || e.Name()[0] == '.' {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1)
		}
	}
	walk(root, 0)
	return found, nil
}

func hasProjectConfig(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, config.ProjectConfigFile))
	return err == nil
}
