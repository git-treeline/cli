package tui

import (
	"sort"

	"github.com/git-treeline/git-treeline/internal/allocator"
	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/supervisor"
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
}

// Snapshot is the full dashboard state captured by a single poll.
type Snapshot struct {
	Worktrees    []WorktreeStatus
	Projects     []string
	ServeRunning bool
}

// Poll reads the registry and probes each worktree's supervisor and ports.
func Poll() Snapshot {
	reg := registry.New("")
	allocs := reg.Allocations()

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
		ServeRunning: service.IsRunning(),
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
