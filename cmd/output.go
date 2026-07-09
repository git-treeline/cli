package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
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
// path new/review share when a branch is already checked out somewhere.
func ensureWorktreeAllocation(wtPath, mainRepo string, uc *config.UserConfig) (registry.Allocation, error) {
	reg := registry.New("")
	if alloc := reg.Find(wtPath); alloc != nil {
		return alloc, nil
	}
	fmt.Println(style.Actionf("No allocation found — running setup..."))
	s := setup.New(wtPath, mainRepo, uc)
	if _, err := s.Run(); err != nil {
		return nil, errSetupFailed(err)
	}
	return registry.New("").Find(wtPath), nil
}

// ensureGitignored delegates to worktree.EnsureGitignored and prints
// a message if a pattern was added.
func ensureGitignored(mainRepo, wtPath string) error {
	pattern, err := worktree.EnsureGitignored(mainRepo, wtPath)
	if err != nil {
		return err
	}
	if pattern != "" {
		fmt.Println(style.Actionf("Added %s to .gitignore", pattern))
	}
	return nil
}
