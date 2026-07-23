package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

// warnServeNotInstalled prints a non-blocking warning when the HTTPS router
// is not installed. Used by commands that benefit from but don't require it.
func warnServeNotInstalled() {
	if routerIsHealthy() || os.Getenv("GTL_HEADLESS") != "" {
		return
	}
	uc := config.LoadUserConfig("")
	if !uc.RouterWarningsEnabled() {
		return
	}
	fmt.Fprintln(os.Stderr, style.Warnf("HTTPS router not installed — local URLs will use http://localhost:{port}."))
	fmt.Fprintln(os.Stderr, style.Dimf("  Run 'gtl install' or 'gtl serve install' to enable HTTPS routing."))
	fmt.Fprintln(os.Stderr)
}

// printLocalAndRouter prints immediately usable URLs after start.
// Tunnels are intentionally omitted here; use gtl routes or gtl tunnel for
// public sharing URLs.
func printLocalAndRouter(uc *config.UserConfig, project, branch string, port int) {
	if port > 0 {
		fmt.Println(style.Actionf("Local:  %s", style.Link(fmt.Sprintf("http://localhost:%d", port))))
	}

	printRouterURL(uc, project, branch)
}

// printRouterURL prints the local HTTPS router URL when the router is running.
func printRouterURL(uc *config.UserConfig, project, branch string) {
	if uc.RouterMode() == config.RouterModeDisabled {
		return
	}
	domain := uc.RouterDomain()

	if service.IsRunning() {
		url := proxy.BuildRouterURL(0, project, branch, domain, uc.RouterPort(), true, service.IsPortForwardConfigured())
		fmt.Println(style.Actionf("Router: %s", style.Link(url)))
	}
}

// isInWorktree reports whether absPath differs from mainRepo after resolving
// symlinks. Falls back to filepath.Clean comparison when EvalSymlinks fails,
// avoiding false equality from two empty-string errors.
func isInWorktree(absPath, mainRepo string) bool {
	resolvedAbs, errAbs := filepath.EvalSymlinks(absPath)
	resolvedMain, errMain := filepath.EvalSymlinks(mainRepo)
	if errAbs != nil || errMain != nil {
		return filepath.Clean(absPath) != filepath.Clean(mainRepo)
	}
	return resolvedAbs != resolvedMain
}

// maybeOpenInBrowser opens the worktree's URL when its command was invoked
// with --open. Shared by new/review so the two commands can't drift on how
// the URL is built or when opening is skipped (no port allocated).
func maybeOpenInBrowser(open bool, uc *config.UserConfig, port int, project, branch string) {
	if !open || port <= 0 {
		return
	}
	url := buildOpenURL(port, project, branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())
	fmt.Printf("Opening %s\n", url)
	_ = openBrowser(url)
}

// maybeStartServer runs commands.start in dir when its command was invoked
// with --start. handled reports whether the flag was set at all — when true
// the caller should return err as its own result (the flag consumed the rest
// of the command), when false the caller continues its normal tail.
func maybeStartServer(start bool, pc *config.ProjectConfig, dir string) (handled bool, err error) {
	if !start {
		return false, nil
	}
	startCmd := pc.StartCommand()
	if startCmd == "" {
		fmt.Println(style.Warnf("--start passed but no commands.start configured in .treeline.yml"))
		return true, nil
	}
	fmt.Println(style.Actionf("Starting: %s", startCmd))
	return true, execInWorktree(dir, startCmd)
}

// ensureWorktreeAllocation returns the registry allocation for an existing
// worktree, running a full setup first when none exists yet — the "resume"
// path new/review/claim share when a branch is already checked out
// somewhere. progress is where setup's own output goes (os.Stdout for
// new/review, os.Stderr for claim so its stdout stays script-friendly).
func ensureWorktreeAllocation(wtPath, mainRepo string, uc *config.UserConfig, progress io.Writer) (registry.Allocation, error) {
	reg := registry.New("")
	if alloc := reg.Find(wtPath); alloc != nil {
		return alloc, nil
	}
	_, _ = fmt.Fprintln(progress, style.Actionf("No allocation found — running setup..."))
	s := setup.New(wtPath, mainRepo, uc)
	s.Log = progress
	if _, err := s.Run(); err != nil {
		return nil, errSetupFailed(err)
	}
	return reg.Find(wtPath), nil
}

// runSetupWithRollback runs 'gtl setup' in a freshly created worktree,
// writing progress to progress (os.Stdout for 'new', os.Stderr for 'claim'
// so its stdout stays script-friendly). On failure it removes the worktree
// so a failed 'new'/'claim' leaves no trace (no orphaned directory,
// invisible to prune). Shared by 'gtl new' (both the new-branch and
// existing-branch paths) and 'gtl claim' (its adopt path).
func runSetupWithRollback(cmd *cobra.Command, wtPath, mainRepo string, uc *config.UserConfig, progress io.Writer) (*allocator.Allocation, error) {
	_, _ = fmt.Fprintln(progress, style.Actionf("Worktree created at %s", wtPath))
	_, _ = fmt.Fprintln(progress, style.Actionf("Running setup..."))

	s := setup.New(wtPath, mainRepo, uc)
	s.Log = progress
	s.Options.DryRun = false
	alloc, err := s.Run()
	if err != nil {
		if rmErr := worktree.Remove(wtPath, true); rmErr != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("Could not remove worktree after failed setup: %s", rmErr))
			fmt.Fprintln(os.Stderr, style.Dimf("  Remove it manually: git worktree remove --force %s", wtPath))
		} else {
			_, _ = fmt.Fprintln(progress, style.Dimf("Rolled back worktree %s after setup failure.", wtPath))
		}
		return nil, cliErr(cmd, errSetupFailed(err))
	}
	return alloc, nil
}

// ensureGitignored delegates to worktree.EnsureGitignored and prints a
// message to progress if a pattern was added (os.Stdout for new/review,
// os.Stderr for claim so its stdout stays script-friendly).
func ensureGitignored(mainRepo, wtPath string, progress io.Writer) error {
	pattern, err := worktree.EnsureGitignored(mainRepo, wtPath)
	if err != nil {
		return err
	}
	if pattern != "" {
		_, _ = fmt.Fprintln(progress, style.Actionf("Added %s to .gitignore", pattern))
	}
	return nil
}

// resolveWorktreePath returns the target path for a worktree: the command's
// --path override, then the user config template, then the default sibling
// layout. Shared by 'gtl new' and 'gtl claim'.
func resolveWorktreePath(pathOverride, mainRepo, projectName, branch string, uc *config.UserConfig) string {
	if pathOverride != "" {
		return pathOverride
	}
	if p := uc.ResolveWorktreePath(mainRepo, projectName, branch); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(mainRepo), fmt.Sprintf("%s-%s", projectName, branch))
}
