package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/style"
)

// errServeNotInstalled is the shared error returned when commands require
// the HTTPS router but it hasn't been installed yet.
var errServeNotInstalled = fmt.Errorf(
	"HTTPS router not installed.\n\n  Run 'gtl serve install' first (one-time setup).\n  Docs: https://gittreeline.com/docs/#getting-started",
)

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

	if runtime.GOOS == "darwin" {
		hostname := routeKey + "." + domain
		if service.NeedsHostsSync([]string{hostname}) {
			fmt.Fprintln(os.Stderr, style.Dimf("  Safari: run %s to resolve this route", style.Cmd("gtl serve hosts sync")))
		}
	}
}
