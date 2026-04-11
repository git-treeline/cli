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

// errServeNotInstalled is the shared error returned when commands require
// the HTTPS router but it hasn't been installed yet.
var errServeNotInstalled error = &CliError{
	Message: "HTTPS router not installed.",
	Hint:    "Run 'gtl serve install' first (one-time setup).",
	DocsURL: "https://git-treeline.dev/docs/#getting-started",
}

// requireServeInstalled returns errServeNotInstalled when the HTTPS CA is
// absent and GTL_HEADLESS is not set. Call from commands that need the router.
func requireServeInstalled() error {
	if !proxy.IsCAInstalled() && os.Getenv("GTL_HEADLESS") == "" {
		return errServeNotInstalled
	}
	return nil
}

// printRouterAndTunnel prints the Router URL and Tunnel hint after setup.
// Called from setup, new, and clone to avoid duplication.
func printRouterAndTunnel(uc *config.UserConfig, project, branch string) {
	routeKey := proxy.RouteKey(project, branch)
	domain := uc.RouterDomain()

	if service.IsRunning() {
		url := proxy.BuildRouterURL(0, project, branch, domain, uc.RouterPort(), true, service.IsPortForwardConfigured())
		fmt.Println(style.Actionf("Router: %s", style.Link(url)))
	}

	if tunnelDomain := uc.TunnelDomain(""); tunnelDomain != "" {
		fmt.Println(style.Actionf("Tunnel: run %s → %s", style.Cmd("gtl tunnel"), style.Link("https://"+routeKey+"."+tunnelDomain)))
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
