package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/worktree"
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
