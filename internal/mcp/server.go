package mcp

import (
	"context"
	"encoding/json"

	"github.com/git-treeline/git-treeline/internal/config"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func NewServer(version string) *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer(
		"git-treeline",
		version,
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithResourceCapabilities(true, false),
	)

	registerTools(s)
	registerResources(s)

	return s
}

func Serve(version string) error {
	s := NewServer(version)
	return mcpserver.ServeStdio(s)
}

func registerTools(s *mcpserver.MCPServer) {
	s.AddTool(mcplib.NewTool("status",
		mcplib.WithDescription("Show allocation for a worktree: port, database, branch, project, and supervisor state"),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleStatus)

	s.AddTool(mcplib.NewTool("port",
		mcplib.WithDescription("Get the primary allocated port for a worktree"),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handlePort)

	s.AddTool(mcplib.NewTool("list",
		mcplib.WithDescription("List all active allocations across all projects"),
		mcplib.WithString("project",
			mcplib.Description("Filter by project name (optional)"),
		),
	), handleList)

	s.AddTool(mcplib.NewTool("doctor",
		mcplib.WithDescription("Run project health diagnostics: config, allocation, runtime, and framework checks"),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleDoctor)

	s.AddTool(mcplib.NewTool("db_name",
		mcplib.WithDescription("Get the database name for a worktree"),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleDBName)

	s.AddTool(mcplib.NewTool("start",
		mcplib.WithDescription("Start or resume the supervised dev server for a worktree"),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleStart)

	s.AddTool(mcplib.NewTool("stop",
		mcplib.WithDescription("Stop the supervised dev server (supervisor stays alive for resume)"),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleStop)

	s.AddTool(mcplib.NewTool("restart",
		mcplib.WithDescription("Restart the supervised dev server"),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleRestart)

	s.AddTool(mcplib.NewTool("config_get",
		mcplib.WithDescription("Read a configuration value by dotted key path"),
		mcplib.WithString("key",
			mcplib.Required(),
			mcplib.Description("Dotted key path (e.g. 'port.base', 'database.adapter')"),
		),
		mcplib.WithString("scope",
			mcplib.Description("Config scope: 'user' or 'project' (defaults to 'project')"),
		),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the project root (required for scope=project)"),
		),
	), handleConfigGet)
}

func registerResources(s *mcpserver.MCPServer) {
	s.AddResource(
		mcplib.NewResource("gtl://allocations",
			"All Allocations",
			mcplib.WithResourceDescription("Full allocation registry across all projects"),
			mcplib.WithMIMEType("application/json"),
		),
		handleAllocationsResource,
	)

	s.AddResource(
		mcplib.NewResource("gtl://config/user",
			"User Config",
			mcplib.WithResourceDescription("User-level git-treeline configuration"),
			mcplib.WithMIMEType("application/json"),
		),
		handleUserConfigResource,
	)
}

func handleAllocationsResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	reg := newRegistry()
	allocs := reg.Allocations()
	data, err := json.MarshalIndent(allocs, "", "  ")
	if err != nil {
		return nil, err
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

func handleUserConfigResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	uc := config.LoadUserConfig("")
	data, err := json.MarshalIndent(uc.Data, "", "  ")
	if err != nil {
		return nil, err
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}
