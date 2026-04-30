package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/templates"
	"github.com/git-treeline/cli/internal/worktree"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// registryPath is overridable in tests to avoid hitting the real registry.
var registryPath string

func newRegistry() *registry.Registry {
	return registry.New(registryPath)
}

func resolvePath(req mcplib.CallToolRequest) string {
	args := req.GetArguments()
	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}
	cwd, _ := os.Getwd()
	abs, _ := filepath.Abs(cwd)
	return abs
}

func handleStatus(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)

	reg := newRegistry()
	entry := reg.Find(absPath)
	if entry == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("No allocation found for %s. Run `gtl setup` first.", absPath)), nil
	}

	fa := format.Allocation(entry)
	ports := format.GetPorts(fa)

	result := map[string]any{
		"worktree": format.GetStr(fa, "worktree"),
		"project":  format.GetStr(fa, "project"),
		"branch":   format.GetStr(fa, "branch"),
		"ports":    ports,
		"database": format.GetStr(fa, "database"),
	}

	if links := reg.GetLinks(absPath); len(links) > 0 {
		result["links"] = links
	}

	if len(ports) > 0 {
		result["listening"] = allocator.CheckPortsListening(ports)
	}

	sockPath := supervisor.SocketPath(absPath)
	if resp, err := supervisor.Send(sockPath, "status"); err == nil {
		result["supervisor"] = resp
	} else {
		result["supervisor"] = "not running"
	}

	return jsonResult(result)
}

func handlePort(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)

	reg := newRegistry()
	entry := reg.Find(absPath)
	if entry == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("No allocation found for %s. Run `gtl setup` first.", absPath)), nil
	}

	ports := format.GetPorts(format.Allocation(entry))
	if len(ports) == 0 {
		return mcplib.NewToolResultError("Allocation exists but has no ports."), nil
	}

	return mcplib.NewToolResultText(fmt.Sprintf("%d", ports[0])), nil
}

func handleList(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	reg := newRegistry()
	args := req.GetArguments()

	var allocs []registry.Allocation
	if project, ok := args["project"].(string); ok && project != "" {
		allocs = reg.FindByProject(project)
	} else {
		allocs = reg.Allocations()
	}

	if len(allocs) == 0 {
		return mcplib.NewToolResultText("No active allocations."), nil
	}

	type entry struct {
		Project  string `json:"project"`
		Branch   string `json:"branch,omitempty"`
		Worktree string `json:"worktree"`
		Ports    []int  `json:"ports"`
		Database string `json:"database,omitempty"`
	}

	entries := make([]entry, 0, len(allocs))
	for _, a := range allocs {
		fa := format.Allocation(a)
		entries = append(entries, entry{
			Project:  format.GetStr(fa, "project"),
			Branch:   format.GetStr(fa, "branch"),
			Worktree: format.GetStr(fa, "worktree"),
			Ports:    format.GetPorts(fa),
			Database: format.GetStr(fa, "database"),
		})
	}

	return jsonResult(entries)
}

func handleDoctor(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	mainRepo := worktree.DetectMainRepo(absPath)
	det := detect.Detect(absPath)
	pc := config.LoadProjectConfig(mainRepo)

	result := map[string]any{}

	// Config section
	cfgInfo := map[string]any{}
	if pc.Exists() {
		cfgInfo["treeline_yml"] = "ok"
		cfgInfo["project"] = pc.Project()
		if fw := det.Framework; fw != "" && fw != "unknown" {
			cfgInfo["framework"] = fw
		}
		cfgInfo["env_file"] = pc.EnvFileTarget()
		cfgInfo["start_command"] = pc.StartCommand()
	} else {
		cfgInfo["treeline_yml"] = "missing"
	}
	result["config"] = cfgInfo

	// Allocation section
	reg := newRegistry()
	alloc := reg.Find(absPath)
	allocInfo := map[string]any{}
	if alloc != nil {
		fa := format.Allocation(alloc)
		allocInfo["ports"] = format.GetPorts(fa)
		allocInfo["database"] = format.GetStr(fa, "database")
		if links := reg.GetLinks(absPath); len(links) > 0 {
			allocInfo["links"] = links
		}
	} else {
		allocInfo["status"] = "none — run gtl setup"
	}
	result["allocation"] = allocInfo

	// Runtime section
	runtime := map[string]any{}
	if alloc != nil {
		fa := format.Allocation(alloc)
		ports := format.GetPorts(fa)
		if len(ports) > 0 {
			runtime["listening"] = allocator.CheckPortsListening(ports)
		}
	}
	sockPath := supervisor.SocketPath(absPath)
	if resp, err := supervisor.Send(sockPath, "status"); err == nil {
		runtime["supervisor"] = resp
	} else {
		runtime["supervisor"] = "not running"
	}
	result["runtime"] = runtime

	// Diagnostics
	diags := templates.Diagnose(det)
	if len(diags) > 0 {
		var messages []string
		for _, d := range diags {
			messages = append(messages, fmt.Sprintf("[%s] %s", d.Level, d.Message))
		}
		result["diagnostics"] = messages
	}

	return jsonResult(result)
}

func handleDBName(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)

	reg := newRegistry()
	entry := reg.Find(absPath)
	if entry == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("No allocation found for %s. Run `gtl setup` first.", absPath)), nil
	}

	dbName, _ := entry["database"].(string)
	if dbName == "" {
		return mcplib.NewToolResultError("No database configured for this worktree."), nil
	}

	return mcplib.NewToolResultText(dbName), nil
}

func handleStart(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	sockPath := supervisor.SocketPath(absPath)

	resp, err := supervisor.Send(sockPath, "status")
	if err != nil {
		return mcplib.NewToolResultError("Supervisor not running. Start it with `gtl start` in a terminal first."), nil
	}

	if resp == "running" {
		return mcplib.NewToolResultText("Server is already running."), nil
	}

	resp, err = supervisor.Send(sockPath, "start")
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Failed to start server: %v", err)), nil
	}
	if strings.HasPrefix(resp, "error") {
		return mcplib.NewToolResultError(resp), nil
	}

	return mcplib.NewToolResultText("Server started."), nil
}

func handleStop(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	sockPath := supervisor.SocketPath(absPath)

	args := req.GetArguments()
	kill, _ := args["kill"].(bool)

	command := "stop"
	if kill {
		command = "shutdown"
	}

	resp, err := supervisor.Send(sockPath, command)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Supervisor not running: %v", err)), nil
	}
	if strings.HasPrefix(resp, "error") {
		return mcplib.NewToolResultError(resp), nil
	}

	if kill {
		return mcplib.NewToolResultText("Supervisor shut down."), nil
	}
	return mcplib.NewToolResultText("Server stopped. Supervisor still running — use `start` to resume."), nil
}

func handleRestart(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	absPath := resolvePath(req)
	sockPath := supervisor.SocketPath(absPath)

	resp, err := supervisor.Send(sockPath, "restart")
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Supervisor not running: %v", err)), nil
	}
	if strings.HasPrefix(resp, "error") {
		return mcplib.NewToolResultError(resp), nil
	}

	return mcplib.NewToolResultText("Server restarted."), nil
}

func handleConfigGet(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	key, err := req.RequireString("key")
	if err != nil {
		return mcplib.NewToolResultError("key parameter is required"), nil
	}

	args := req.GetArguments()
	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "project"
	}

	switch scope {
	case "user":
		uc := config.LoadUserConfig("")
		val := uc.Get(key)
		if val == nil {
			return mcplib.NewToolResultText("null"), nil
		}
		return jsonResult(val)

	case "project":
		absPath := resolvePath(req)
		mainRepo := worktree.DetectMainRepo(absPath)
		pc := config.LoadProjectConfig(mainRepo)
		keys := strings.Split(key, ".")
		val := config.Dig(pc.Data, keys...)
		if val == nil {
			return mcplib.NewToolResultText("null"), nil
		}
		return jsonResult(val)

	default:
		return mcplib.NewToolResultError(fmt.Sprintf("Unknown scope %q. Use 'user' or 'project'.", scope)), nil
	}
}

func jsonResult(v any) (*mcplib.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcplib.NewToolResultText(string(data)), nil
}
