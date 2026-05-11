package proxy

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
)

// BackendState classifies why a proxied request couldn't reach the
// upstream dev server. The router uses this to pick a status page so
// users can tell "still starting" apart from "actually broken".
type BackendState int

const (
	BackendUnknown      BackendState = iota
	BackendStarting                  // supervisor + child both up, port not yet listening
	BackendNotStarted                // allocation exists, supervisor not running
	BackendStopped                   // supervisor running, no live child (stopped or crashed)
	BackendUnreachable               // supervisor responded with something unexpected
)

// SupervisorProbe asks the supervisor at socketPath for its status.
// Returns "running", "stopped", or an empty string if the supervisor
// can't be reached. Pulled out so tests can inject a fake.
type SupervisorProbe func(socketPath string) (string, error)

// PortProbe attempts a TCP connection to localhost:port within the
// given timeout and returns true if the port is accepting connections.
type PortProbe func(port int, timeout time.Duration) bool

func defaultSupervisorProbe(socketPath string) (string, error) {
	return supervisor.SendWithTimeout(socketPath, "status", 500*time.Millisecond)
}

func defaultPortProbe(port int, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// classifyBackend decides which status page to show when the proxy
// can't reach the upstream. worktreePath is the absolute path of the
// worktree this allocation lives in; port is the allocated dev server
// port. Either probe can be nil to use the default.
func classifyBackend(worktreePath string, port int, probeSup SupervisorProbe, probePort PortProbe) BackendState {
	if probePort == nil {
		probePort = defaultPortProbe
	}
	if probeSup == nil {
		probeSup = defaultSupervisorProbe
	}

	if probePort(port, 200*time.Millisecond) {
		// Port is listening — the dial failure that put us in the error
		// handler must have been a transient blip. Show the generic
		// "unreachable" page so the user knows it's not "still starting".
		return BackendUnreachable
	}

	if worktreePath == "" {
		return BackendUnknown
	}

	socketPath := supervisor.SocketPath(worktreePath)
	resp, err := probeSup(socketPath)
	if err != nil {
		return BackendNotStarted
	}
	switch strings.TrimSpace(resp) {
	case "running":
		return BackendStarting
	case "stopped":
		return BackendStopped
	default:
		return BackendUnreachable
	}
}

// findWorktreeForRoute returns the worktree path for the allocation
// whose project/branch produces the given subdomain key. Empty string
// means no matching allocation (the route is an alias, not a real
// worktree). Aliases don't have supervisors so we can't classify them.
func findWorktreeForRoute(reg *registry.Registry, subdomain string) string {
	for _, a := range reg.Allocations() {
		project := registry.GetString(a, "project")
		branch := registry.GetString(a, "branch")
		if project == "" {
			continue
		}
		if RouteKey(project, branch) == subdomain {
			return registry.GetString(a, "worktree")
		}
	}
	return ""
}
