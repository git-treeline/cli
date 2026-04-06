package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/style"
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
		if service.IsPortForwardConfigured() {
			fmt.Println(style.Actionf("Router: %s", style.Link("https://"+routeKey+"."+domain)))
		} else {
			port := uc.RouterPort()
			fmt.Println(style.Actionf("Router: %s", style.Link(fmt.Sprintf("https://%s.%s:%d", routeKey, domain, port))))
		}
	}

	if tunnelDomain := uc.TunnelDomain(""); tunnelDomain != "" {
		fmt.Println(style.Actionf("Tunnel: run %s → %s", style.Cmd("gtl tunnel"), style.Link("https://"+routeKey+"."+tunnelDomain)))
	}
}

// ensureGitignored checks whether a worktree path that lives inside the repo
// root is gitignored. If not, it appends the directory to .gitignore.
// Paths outside the repo root (the default sibling layout) are a no-op.
func ensureGitignored(mainRepo, wtPath string) error {
	absRepo, _ := filepath.Abs(mainRepo)
	absWT, _ := filepath.Abs(wtPath)

	rel, err := filepath.Rel(absRepo, absWT)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}

	cmd := exec.Command("git", "check-ignore", "-q", absWT)
	cmd.Dir = mainRepo
	if cmd.Run() == nil {
		return nil
	}

	topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	pattern := "/" + topLevel + "/"

	gitignorePath := filepath.Join(absRepo, ".gitignore")
	existing, _ := os.ReadFile(gitignorePath)
	if strings.Contains(string(existing), pattern) {
		return nil
	}

	entry := pattern + "\n"
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		entry = "\n" + entry
	}
	if err := os.WriteFile(gitignorePath, append(existing, []byte(entry)...), 0o644); err != nil {
		return fmt.Errorf("updating .gitignore: %w", err)
	}
	fmt.Println(style.Actionf("Added %s to .gitignore", pattern))
	return nil
}
