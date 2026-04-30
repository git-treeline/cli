package tui

import (
	"sort"
	"strings"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/supervisor"
)

// WorktreeStatus represents the live state of a single worktree allocation.
type WorktreeStatus struct {
	Project      string
	Branch       string
	WorktreeName string
	WorktreePath string
	Ports        []int
	Database     string
	RedisPrefix  string
	RedisDB      int
	EnvFile      string
	Links        map[string]string
	Supervisor   string // "running", "stopped", or "not running"
	Listening    bool
	RouterURL    string
	TunnelURL    string
}

// Snapshot is the full dashboard state captured by a single poll.
type Snapshot struct {
	Worktrees    []WorktreeStatus
	Projects     []string
	ServeRunning bool
	TunnelDomain string
}

// Poll reads the registry and probes each worktree's supervisor and ports.
func Poll() Snapshot {
	reg := registry.New("")
	allocs := reg.Allocations()

	uc := config.LoadUserConfig("")
	domain := uc.RouterDomain()
	routerPort := uc.RouterPort()
	tunnelDomain := uc.TunnelDomain("")
	svcRunning := service.IsRunning()
	pfConfigured := service.IsPortForwardConfigured()

	worktrees := make([]WorktreeStatus, 0, len(allocs))
	projectSet := make(map[string]struct{})

	for _, a := range allocs {
		fa := format.Allocation(a)
		wt := format.GetStr(fa, "worktree")
		project := format.GetStr(fa, "project")
		branch := format.GetStr(fa, "branch")
		ports := format.GetPorts(fa)

		projectSet[project] = struct{}{}

		sv := "not running"
		if wt != "" {
			sockPath := supervisor.SocketPath(wt)
			if resp, err := supervisor.Send(sockPath, "status"); err == nil {
				sv = resp
			}
		}

		listening := false
		if len(ports) > 0 {
			listening = allocator.CheckPortsListening(ports)
		}

		links := extractLinks(a)

		envFile := ""
		if wt != "" {
			pc := config.LoadProjectConfig(wt)
			if pc.HasEnvFileConfig() {
				envFile = pc.EnvFileTarget()
			}
		}

		var redisDB int
		if v, ok := a["redis_db"].(float64); ok {
			redisDB = int(v)
		}

		// Only store actual router URLs (https://); localhost fallbacks are
		// reconstructed on demand in openInBrowser().
		var routerURL string
		if len(ports) > 0 {
			u := proxy.BuildRouterURL(ports[0], project, branch, domain, routerPort, svcRunning, pfConfigured)
			if strings.HasPrefix(u, "https://") {
				routerURL = u
			}
		}

		tunnelURL := proxy.BuildTunnelURL(project, branch, tunnelDomain)

		worktrees = append(worktrees, WorktreeStatus{
			Project:      project,
			Branch:       branch,
			WorktreeName: format.DisplayName(fa),
			WorktreePath: wt,
			Ports:        ports,
			Database:     format.GetStr(fa, "database"),
			RedisPrefix:  format.GetStr(fa, "redis_prefix"),
			RedisDB:      redisDB,
			EnvFile:      envFile,
			Links:        links,
			Supervisor:   sv,
			Listening:    listening,
			RouterURL:    routerURL,
			TunnelURL:    tunnelURL,
		})
	}

	projects := make([]string, 0, len(projectSet))
	for p := range projectSet {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	return Snapshot{
		Worktrees:    worktrees,
		Projects:     projects,
		ServeRunning: svcRunning,
		TunnelDomain: tunnelDomain,
	}
}

func extractLinks(a registry.Allocation) map[string]string {
	raw, ok := a["links"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
