package cmd

import (
	"fmt"

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

// printRouterAndTunnel prints the Router URL and Tunnel hint after setup.
// Called from setup, new, and clone to avoid duplication.
func printRouterAndTunnel(uc *config.UserConfig, project, branch string) {
	routeKey := proxy.RouteKey(project, branch)

	if service.IsRunning() {
		if service.IsPortForwardConfigured() {
			fmt.Println(style.Actionf("Router: %s", style.Link("https://"+routeKey+".localhost")))
		} else {
			port := uc.RouterPort()
			fmt.Println(style.Actionf("Router: %s", style.Link(fmt.Sprintf("https://%s.localhost:%d", routeKey, port))))
		}
	}

	if domain := uc.TunnelDomain(""); domain != "" {
		fmt.Println(style.Actionf("Tunnel: run %s → %s", style.Cmd("gtl tunnel"), style.Link("https://"+routeKey+"."+domain)))
	}
}
