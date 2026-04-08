package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/confirm"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	releaseDropDB        bool
	releaseProject       string
	releaseAll           bool
	releaseForce         bool
	releaseDryRun        bool
	releaseRemoveWorktree bool
)

func init() {
	releaseCmd.Flags().BoolVar(&releaseDropDB, "drop-db", false, "Also drop the database")
	releaseCmd.Flags().BoolVar(&releaseRemoveWorktree, "remove-worktree", false, "Also remove the git worktree directory")
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
			return errMutuallyExclusive("PATH, --project, and --all")
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
		return errNoAllocation(absPath)
	}

	fa := format.Allocation(alloc)
	ports := format.GetPorts(fa)
	name := format.DisplayName(fa)
	db := format.GetStr(fa, "database")

	line := fmt.Sprintf("Release: %s", name)
	if len(ports) > 0 {
		line += fmt.Sprintf("  (port %s)", format.JoinInts(ports, ", "))
	}
	if db != "" {
		line += fmt.Sprintf("  db:%s", db)
	}
	fmt.Println(line)

	unpushed := worktree.UnpushedCommitCount(absPath)
	if unpushed > 0 {
		branch := worktree.CurrentBranch(absPath)
		fmt.Println()
		fmt.Println(style.Warnf("Branch %q has %d unpushed commit(s).", branch, unpushed))
		fmt.Println(style.Dimf("  These commits may be lost if you remove the worktree."))
		fmt.Println()
	}

	if releaseDryRun {
		fmt.Println("Would release. (dry-run)")
		return nil
	}

	if !confirm.Prompt("Release?", releaseForce, nil) {
		fmt.Println("Aborted.")
		return nil
	}

	pc := config.LoadProjectConfig(absPath)
	hooks := pc.Hooks()
	if cmds, ok := hooks["pre_release"]; ok && len(cmds) > 0 {
		if err := setup.RunHookCommands("pre_release", cmds, absPath, func(f string, a ...any) {
			fmt.Printf("==> "+f+"\n", a...)
		}); err != nil {
			return fmt.Errorf("%w — release aborted", err)
		}
	}

	if releaseDropDB {
		format.DropSingleDB(fa, absPath)
	}

	if _, err := reg.Release(absPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to release allocation: %s\n", err)
	}
	fmt.Printf("==> Released resources for %s\n", filepath.Base(absPath))

	if len(ports) > 1 {
		fmt.Printf("  Ports:    %s\n", format.JoinInts(ports, ", "))
	} else if len(ports) == 1 {
		fmt.Printf("  Port:     %d\n", ports[0])
	}
	if db != "" {
		fmt.Printf("  Database: %s\n", db)
	}

	if releaseRemoveWorktree {
		removeWorktreeDir(absPath, releaseForce)
	}

	if cmds, ok := hooks["post_release"]; ok && len(cmds) > 0 {
		if err := setup.RunHookCommands("post_release", cmds, absPath, func(f string, a ...any) {
			fmt.Printf("==> "+f+"\n", a...)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", err)
		}
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
		fa := format.Allocation(a)
		ports := format.GetPorts(fa)
		name := format.DisplayName(fa)
		db := format.GetStr(fa, "database")
		proj := format.GetStr(fa, "project")

		var line string
		if len(ports) == 0 {
			line = fmt.Sprintf("  (no port)  %s", name)
			if all {
				line = fmt.Sprintf("  [%s] (no port)  %s", proj, name)
			}
		} else {
			line = fmt.Sprintf("  :%d  %s", ports[0], name)
			if all {
				line = fmt.Sprintf("  [%s] :%d  %s", proj, ports[0], name)
			}
		}
		if db != "" {
			line += fmt.Sprintf("  db:%s", db)
		}
		fmt.Println(line)

		wt := format.GetStr(fa, "worktree")
		if wt != "" {
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
		formatAllocs := make([]format.Allocation, len(allocs))
		for i, a := range allocs {
			formatAllocs[i] = format.Allocation(a)
		}
		format.DropDatabases(formatAllocs)
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

	if releaseRemoveWorktree {
		for _, p := range paths {
			removeWorktreeDir(p, releaseForce)
		}
	}

	fmt.Printf("Released %d allocation(s).\n", count)
	return nil
}

// isInsideDir reports whether cwd is equal to or a child of dir.
func isInsideDir(cwd, dir string) bool {
	return cwd == dir || strings.HasPrefix(cwd+string(os.PathSeparator), dir+string(os.PathSeparator))
}

func removeWorktreeDir(absPath string, force bool) {
	if _, err := os.Stat(absPath); err != nil {
		return
	}

	cwd, _ := os.Getwd()
	cwdAbs, _ := filepath.Abs(cwd)
	insideWorktree := isInsideDir(cwdAbs, absPath)

	if insideWorktree {
		mainRepo := worktree.DetectMainRepo(absPath)
		fmt.Println()
		fmt.Println(style.Warnf("You're inside the worktree being removed."))
		fmt.Println(style.Dimf("  After removal, this directory will no longer exist."))
		fmt.Println(style.Dimf("  Your terminal will need: cd %s", mainRepo))
		fmt.Println(style.Dimf("  If in an IDE, close this window or switch workspaces."))
		fmt.Println()
		if !force && !confirm.Prompt("Continue with removal?", false, nil) {
			fmt.Println("Skipped worktree removal.")
			return
		}
	}

	if !force && worktree.HasUncommittedChanges(absPath) {
		fmt.Fprintf(os.Stderr, "Warning: %s has uncommitted changes, skipping removal (use --force to override)\n", filepath.Base(absPath))
		return
	}
	if err := worktree.Remove(absPath, force); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove worktree: %s\n", err)
		return
	}
	fmt.Printf("  Removed worktree %s\n", filepath.Base(absPath))

	if insideWorktree {
		mainRepo := worktree.DetectMainRepo(absPath)
		fmt.Println()
		fmt.Printf("  Run: cd %s\n", mainRepo)
	}
}
