package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/envparse"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/resolve"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/worktree"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func handleResolve(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	project, err := req.RequireString("project")
	if err != nil {
		return mcplib.NewToolResultError("project parameter is required"), nil
	}

	absPath := resolvePath(req)
	args := req.GetArguments()
	explicitBranch, _ := args["branch"].(string)

	reg := newRegistry()
	branch := worktree.CurrentBranch(absPath)
	r := resolve.New(reg, absPath, branch)

	var url string
	if explicitBranch != "" {
		url, err = r.Resolve(project, explicitBranch)
	} else {
		url, err = r.Resolve(project)
	}
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	// If the HTTPS router is running, return the router URL instead.
	if service.IsRunning() {
		targetBranch := explicitBranch
		if targetBranch == "" {
			if links := reg.GetLinks(absPath); links != nil {
				if linked, ok := links[project]; ok {
					targetBranch = linked
				}
			}
			if targetBranch == "" {
				targetBranch = branch
			}
		}
		targetAlloc := reg.FindProjectBranch(project, targetBranch)
		if targetAlloc != nil {
			allocProject, _ := targetAlloc["project"].(string)
			allocBranch, _ := targetAlloc["branch"].(string)
			if allocProject != "" && allocBranch != "" {
				routeKey := proxy.RouteKey(allocProject, allocBranch)
				uc := config.LoadUserConfig("")
				domain := uc.RouterDomain()
				if service.IsPortForwardConfigured() {
					url = fmt.Sprintf("https://%s.%s", routeKey, domain)
				} else {
					url = fmt.Sprintf("https://%s.%s:%d", routeKey, domain, uc.RouterPort())
				}
			}
		}
	}

	return jsonResult(map[string]any{"url": url})
}

func handleEnv(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	pc := config.LoadProjectConfig(absPath)

	args := req.GetArguments()
	template, _ := args["template"].(bool)

	if template {
		tmpl := pc.EnvTemplate()
		if tmpl == nil {
			return jsonResult(map[string]any{})
		}
		return jsonResult(tmpl)
	}

	reg := newRegistry()
	entry := reg.Find(absPath)
	if entry == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("No allocation found for %s. Run `gtl setup` first.", absPath)), nil
	}

	envPath := filepath.Join(absPath, pc.EnvFileTarget())
	entries, err := envparse.ParseFile(envPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Could not read env file %s: %v", envPath, err)), nil
	}

	tmpl := pc.EnvTemplate()
	managed := make(map[string]struct{})
	for k := range tmpl {
		managed[k] = struct{}{}
	}

	varsMap := make(map[string]string, len(entries))
	for _, e := range entries {
		varsMap[e.Key] = e.Val
	}

	managedKeys := make([]string, 0, len(managed))
	for k := range managed {
		managedKeys = append(managedKeys, k)
	}
	sort.Strings(managedKeys)

	return jsonResult(map[string]any{
		"file":             pc.EnvFileTarget(),
		"vars":             varsMap,
		"treeline_managed": managedKeys,
	})
}

func handleEnvSync(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	uc := config.LoadUserConfig("")

	if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Env sync failed: %v", err)), nil
	}

	pc := config.LoadProjectConfig(absPath)
	tmpl := pc.EnvTemplate()
	keys := make([]string, 0, len(tmpl))
	for k := range tmpl {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return jsonResult(map[string]any{
		"synced":       true,
		"file":         pc.EnvFileTarget(),
		"managed_keys": keys,
	})
}

func handleWhere(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	query, err := req.RequireString("branch")
	if err != nil {
		return mcplib.NewToolResultError("branch parameter is required"), nil
	}

	reg := newRegistry()
	allocs := reg.Allocations()

	var project, branch string
	if strings.Contains(query, "/") {
		parts := strings.SplitN(query, "/", 2)
		project = parts[0]
		branch = parts[1]
	} else {
		branch = query
	}

	type match struct {
		Worktree string `json:"worktree"`
		Project  string `json:"project"`
		Branch   string `json:"branch"`
	}

	var matches []match
	for _, a := range allocs {
		allocBranch, _ := a["branch"].(string)
		allocProject, _ := a["project"].(string)
		allocWT, _ := a["worktree"].(string)

		if project != "" {
			if allocProject == project && allocBranch == branch {
				matches = append(matches, match{Worktree: allocWT, Project: allocProject, Branch: allocBranch})
			}
		} else {
			if allocBranch == branch {
				matches = append(matches, match{Worktree: allocWT, Project: allocProject, Branch: allocBranch})
			}
		}
	}

	if len(matches) == 0 {
		return mcplib.NewToolResultError(fmt.Sprintf("No worktree found for branch %q. Run 'gtl status' to see all worktrees.", query)), nil
	}

	if len(matches) > 1 {
		var projects []string
		for _, m := range matches {
			projects = append(projects, m.Project)
		}
		return mcplib.NewToolResultError(fmt.Sprintf(
			"Branch %q exists in multiple projects: %s. Use 'project/branch' format to disambiguate.",
			branch, strings.Join(projects, ", "),
		)), nil
	}

	return jsonResult(matches[0])
}
