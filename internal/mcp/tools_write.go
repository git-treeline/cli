package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/supervisor"
	"github.com/git-treeline/git-treeline/internal/worktree"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func handleConfigSet(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	key, err := req.RequireString("key")
	if err != nil {
		return mcplib.NewToolResultError("key parameter is required"), nil
	}
	rawValue, err := req.RequireString("value")
	if err != nil {
		return mcplib.NewToolResultError("value parameter is required"), nil
	}

	// Parse value: numbers, booleans, then string (matches CLI behavior).
	var value any
	if n, parseErr := strconv.ParseFloat(rawValue, 64); parseErr == nil {
		value = n
	} else if rawValue == "true" {
		value = true
	} else if rawValue == "false" {
		value = false
	} else {
		value = rawValue
	}

	uc := config.LoadUserConfig("")
	uc.Set(key, value)
	if err := uc.Save(); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Failed to save config: %v", err)), nil
	}

	return jsonResult(map[string]any{
		"key":   key,
		"value": value,
		"path":  uc.Path,
	})
}

func handleLink(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	args := req.GetArguments()
	project, _ := args["project"].(string)
	branch, _ := args["branch"].(string)

	reg := newRegistry()

	// No project → list active links.
	if project == "" {
		links := reg.GetLinks(absPath)
		if links == nil {
			links = map[string]string{}
		}
		return jsonResult(links)
	}

	if branch == "" {
		return mcplib.NewToolResultError("branch parameter is required when project is provided"), nil
	}

	alloc := reg.Find(absPath)
	if alloc == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("No allocation found for %s. Run `gtl setup` first.", absPath)), nil
	}

	target := reg.FindProjectBranch(project, branch)
	if target == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("No allocation for project %q on branch %q. Run 'gtl setup' in that worktree first.", project, branch)), nil
	}

	if err := reg.SetLink(absPath, project, branch); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Failed to set link: %v", err)), nil
	}

	uc := config.LoadUserConfig("")
	envUpdated := true
	if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
		envUpdated = false
	}

	restarted := false
	sockPath := supervisor.SocketPath(absPath)
	if resp, err := supervisor.Send(sockPath, "restart"); err == nil && resp == "ok" {
		restarted = true
	}

	return jsonResult(map[string]any{
		"linked":      true,
		"project":     project,
		"branch":      branch,
		"env_updated": envUpdated,
		"restarted":   restarted,
	})
}

func handleUnlink(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	project, err := req.RequireString("project")
	if err != nil {
		return mcplib.NewToolResultError("project parameter is required"), nil
	}

	absPath := resolvePath(req)
	reg := newRegistry()

	links := reg.GetLinks(absPath)
	if _, ok := links[project]; !ok {
		return mcplib.NewToolResultError(fmt.Sprintf("No active link for %q", project)), nil
	}

	if err := reg.RemoveLink(absPath, project); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Failed to remove link: %v", err)), nil
	}

	uc := config.LoadUserConfig("")
	envUpdated := true
	if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
		envUpdated = false
	}

	restarted := false
	sockPath := supervisor.SocketPath(absPath)
	if resp, err := supervisor.Send(sockPath, "restart"); err == nil && resp == "ok" {
		restarted = true
	}

	return jsonResult(map[string]any{
		"unlinked":    true,
		"project":     project,
		"env_updated": envUpdated,
		"restarted":   restarted,
	})
}

func handleSetup(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	args := req.GetArguments()
	mainRepo, _ := args["main_repo"].(string)
	dryRun, _ := args["dry_run"].(bool)

	uc := config.LoadUserConfig("")
	s := setup.New(absPath, mainRepo, uc)
	s.Log = io.Discard
	s.Options.DryRun = dryRun

	alloc, err := s.Run()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Setup failed: %v", err)), nil
	}

	result := map[string]any{
		"worktree": alloc.Worktree,
		"project":  alloc.Project,
		"branch":   alloc.Branch,
		"dry_run":  dryRun,
	}
	if alloc.Port > 0 {
		result["ports"] = alloc.Ports
	}
	if alloc.Database != "" {
		result["database"] = alloc.Database
	}

	return jsonResult(result)
}

func handleNew(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	branch, err := req.RequireString("branch")
	if err != nil {
		return mcplib.NewToolResultError("branch parameter is required"), nil
	}

	args := req.GetArguments()
	mainRepoArg, _ := args["path"].(string)
	base, _ := args["base"].(string)
	customWTPath, _ := args["worktree_path"].(string)
	dryRun, _ := args["dry_run"].(bool)
	wantOpen, _ := args["open"].(bool)

	mainRepo := mainRepoArg
	if mainRepo == "" {
		cwd, _ := os.Getwd()
		mainRepo, _ = filepath.Abs(cwd)
	}
	mainRepo = worktree.DetectMainRepo(mainRepo)

	pc := config.LoadProjectConfig(mainRepo)
	uc := config.LoadUserConfig("")

	if !pc.Exists() {
		return mcplib.NewToolResultError(fmt.Sprintf(
			"No .treeline.yml found in %s. Run 'gtl init' first, or create the config manually.",
			mainRepo,
		)), nil
	}

	projectName := pc.Project()

	wtPath := customWTPath
	if wtPath == "" {
		wtPath = uc.ResolveWorktreePath(mainRepo, projectName, branch)
	}
	if wtPath == "" {
		wtPath = filepath.Join(filepath.Dir(mainRepo), fmt.Sprintf("%s-%s", projectName, branch))
	}

	// Check if branch is already in a worktree (resume case).
	if existingWT := worktree.FindWorktreeForBranch(branch); existingWT != "" {
		reg := newRegistry()
		alloc := reg.Find(existingWT)

		if alloc == nil && !dryRun {
			s := setup.New(existingWT, mainRepo, uc)
			s.Log = io.Discard
			if _, err := s.Run(); err != nil {
				return mcplib.NewToolResultError(fmt.Sprintf("Setup failed for existing worktree: %v", err)), nil
			}
			reg = newRegistry()
			alloc = reg.Find(existingWT)
		}

		result := map[string]any{
			"worktree": existingWT,
			"branch":   branch,
			"project":  projectName,
			"resumed":  true,
		}
		if alloc != nil {
			fa := format.Allocation(alloc)
			ports := format.GetPorts(fa)
			if len(ports) > 0 {
				result["ports"] = ports
				result["url"] = buildWorktreeURL(ports[0], projectName, branch, uc)
			}
		}
		return jsonResult(result)
	}

	if dryRun {
		existing := worktree.BranchExists(branch)
		result := map[string]any{
			"dry_run":       true,
			"worktree_path": wtPath,
			"branch":        branch,
			"new_branch":    !existing,
		}
		if !existing {
			b := base
			if b == "" {
				b = "(current branch)"
			}
			result["base"] = b
		}
		return jsonResult(result)
	}

	mcpEnsureGitignored(mainRepo, wtPath)

	existing := worktree.BranchExists(branch)
	if existing {
		_ = worktree.Fetch("origin", branch)
		if err := worktree.Create(wtPath, branch, false, ""); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("Failed to create worktree: %v", err)), nil
		}
	} else {
		if base == "" {
			base = worktree.CurrentBranch(".")
			if base == "" {
				base = "main"
			}
		}
		if err := worktree.Create(wtPath, branch, true, base); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("Failed to create worktree: %v", err)), nil
		}
	}

	s := setup.New(wtPath, mainRepo, uc)
	s.Log = io.Discard
	alloc, err := s.Run()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Setup failed: %v", err)), nil
	}

	result := map[string]any{
		"worktree": wtPath,
		"branch":   branch,
		"project":  projectName,
	}
	if alloc.Port > 0 {
		result["ports"] = alloc.Ports
		result["url"] = buildWorktreeURL(alloc.Port, projectName, alloc.Branch, uc)
	}
	if alloc.Database != "" {
		result["database"] = alloc.Database
	}
	if wantOpen {
		if url, ok := result["url"].(string); ok {
			result["open_url"] = url
		}
	}

	return jsonResult(result)
}

// buildWorktreeURL returns the best URL for a worktree — router URL if the
// HTTPS router is running, otherwise plain localhost.
func buildWorktreeURL(port int, project, branch string, uc *config.UserConfig) string {
	if service.IsRunning() {
		routeKey := proxy.RouteKey(project, branch)
		domain := uc.RouterDomain()
		if service.IsPortForwardConfigured() {
			return fmt.Sprintf("https://%s.%s", routeKey, domain)
		}
		return fmt.Sprintf("https://%s.%s:%d", routeKey, domain, uc.RouterPort())
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

// mcpEnsureGitignored adds the worktree directory to .gitignore if it lives
// inside the repo root and isn't already ignored. Mirrors cmd/output.go logic.
func mcpEnsureGitignored(mainRepo, wtPath string) {
	absRepo, _ := filepath.Abs(mainRepo)
	absWT, _ := filepath.Abs(wtPath)

	rel, err := filepath.Rel(absRepo, absWT)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}

	cmd := exec.Command("git", "check-ignore", "-q", absWT)
	cmd.Dir = mainRepo
	if cmd.Run() == nil {
		return
	}

	topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	pattern := "/" + topLevel + "/"

	gitignorePath := filepath.Join(absRepo, ".gitignore")
	existing, _ := os.ReadFile(gitignorePath)
	if strings.Contains(string(existing), pattern) {
		return
	}

	entry := pattern + "\n"
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		entry = "\n" + entry
	}
	_ = os.WriteFile(gitignorePath, append(existing, []byte(entry)...), 0o644)
}
