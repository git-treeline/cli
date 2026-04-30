package mcp

import (
	"context"
	"encoding/json"

	"github.com/git-treeline/cli/internal/config"
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
	// --- query tools ---

	s.AddTool(mcplib.NewTool("status",
		mcplib.WithDescription("Show the full allocation for a worktree including its project name, branch, allocated ports, database name, active resolve links, and whether the dev server is running. Use this to understand what resources a worktree has and its current state."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleStatus)

	s.AddTool(mcplib.NewTool("port",
		mcplib.WithDescription("Get the primary allocated port number for a worktree. Returns just the port number as text. Use this when you need the port to construct URLs or check if a service is reachable."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handlePort)

	s.AddTool(mcplib.NewTool("list",
		mcplib.WithDescription("List all active worktree allocations across all projects. Returns a JSON array of allocations with worktree path, project, branch, ports, and database for each. Optionally filter by project name."),
		mcplib.WithString("project",
			mcplib.Description("Filter by project name (optional)"),
		),
	), handleList)

	s.AddTool(mcplib.NewTool("doctor",
		mcplib.WithDescription("Run health diagnostics for a worktree. Checks configuration (.treeline.yml), port allocation and availability, database existence, dev server status, and framework-specific issues. Returns structured results with warnings and suggested fixes."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleDoctor)

	s.AddTool(mcplib.NewTool("db_name",
		mcplib.WithDescription("Get the allocated database name for a worktree. Returns the database name as text. Only applicable to worktrees with database configuration in .treeline.yml."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleDBName)

	s.AddTool(mcplib.NewTool("start",
		mcplib.WithDescription("Start or resume the supervised dev server for a worktree. If the supervisor is already running and the server was stopped, this resumes it. Environment variables including resolve links are evaluated fresh at start time. Requires commands.start to be configured in .treeline.yml."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleStart)

	s.AddTool(mcplib.NewTool("stop",
		mcplib.WithDescription("Stop the dev server process but keep the supervisor alive. The server can be quickly resumed with start without re-initializing. Use this to free up resources while keeping the worktree ready. Set kill=true to shut down the supervisor entirely (useful when the supervisor is stuck or needs a full restart)."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
		mcplib.WithBoolean("kill",
			mcplib.Description("If true, shut down the supervisor entirely instead of keeping it alive for resume. Default: false"),
		),
	), handleStop)

	s.AddTool(mcplib.NewTool("restart",
		mcplib.WithDescription("Restart the dev server process. Environment variables including resolve links are re-evaluated, so this picks up any link changes. Equivalent to stop followed by start."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd if omitted)"),
		),
	), handleRestart)

	s.AddTool(mcplib.NewTool("config_get",
		mcplib.WithDescription("Read a git-treeline configuration value by dotted key path (e.g. 'port.base', 'redis.strategy', 'database.adapter'). Can read from user-level config or project-level .treeline.yml. Returns the value as JSON, or null if the key does not exist."),
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

	s.AddTool(mcplib.NewTool("resolve",
		mcplib.WithDescription("Look up the local URL for another project's worktree. Uses same-branch matching by default: if your worktree is on branch 'feature-auth', it finds the target project's 'feature-auth' allocation. Respects active link overrides (set via the link tool). Returns a base URL like http://127.0.0.1:3010, or the HTTPS router URL if the local router is running."),
		mcplib.WithString("project",
			mcplib.Required(),
			mcplib.Description("Target project name as defined in its .treeline.yml"),
		),
		mcplib.WithString("branch",
			mcplib.Description("Explicit target branch. Bypasses same-branch matching and link overrides."),
		),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the calling worktree (defaults to cwd). Used to determine the current branch for same-branch matching."),
		),
	), handleResolve)

	s.AddTool(mcplib.NewTool("env",
		mcplib.WithDescription("Show the resolved environment variables for a worktree. Returns all variables from the env file with metadata indicating which keys are managed by Treeline (ports, database, resolve URLs). Use template=true to see the raw templates from .treeline.yml before interpolation."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd)"),
		),
		mcplib.WithBoolean("template",
			mcplib.Description("If true, return unresolved templates from .treeline.yml instead of the env file contents"),
		),
	), handleEnv)

	s.AddTool(mcplib.NewTool("env_sync",
		mcplib.WithDescription("Re-sync the env file from .treeline.yml. Re-reads the env: block, re-resolves all {resolve:...} tokens and port/database variables, and writes updated values to the env file. Use this after editing .treeline.yml or when resolve targets have changed."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd)"),
		),
	), handleEnvSync)

	s.AddTool(mcplib.NewTool("routes",
		mcplib.WithDescription("Show the routing URLs for a worktree — one per allocated port. When the HTTPS router is running, returns https:// URLs via prt.dev; otherwise falls back to http://localhost. Also includes the tunnel URL if configured. Use this to find the URL(s) for the current worktree."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree directory (defaults to cwd)"),
		),
	), handleRoutes)

	s.AddTool(mcplib.NewTool("where",
		mcplib.WithDescription("Find the filesystem path of a worktree by branch name. If the branch exists in multiple projects, use 'project/branch' format to disambiguate. Returns the absolute path to the worktree directory."),
		mcplib.WithString("branch",
			mcplib.Required(),
			mcplib.Description("Branch name to look up, or 'project/branch' if ambiguous across projects"),
		),
	), handleWhere)

	// --- write operations ---

	s.AddTool(mcplib.NewTool("config_set",
		mcplib.WithDescription("Set a user-level git-treeline configuration value. Writes to ~/.config/git-treeline/config.json. Values are auto-parsed: numbers become numeric, 'true'/'false' become booleans, everything else is stored as a string."),
		mcplib.WithString("key",
			mcplib.Required(),
			mcplib.Description("Dotted key path (e.g. 'port.base', 'redis.strategy', 'tunnel.default')"),
		),
		mcplib.WithString("value",
			mcplib.Required(),
			mcplib.Description("Value to set. Numbers and booleans are auto-detected from the string."),
		),
	), handleConfigSet)

	s.AddTool(mcplib.NewTool("link",
		mcplib.WithDescription("Override cross-service resolution for this worktree. Sets which branch of a target project the {resolve:project} env template points to, instead of the default same-branch match. Immediately regenerates the worktree's env file and restarts the supervised server if running. Call with no project/branch to list active links."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree to modify (defaults to cwd)"),
		),
		mcplib.WithString("project",
			mcplib.Description("Target project name to override resolution for. Omit to list current links."),
		),
		mcplib.WithString("branch",
			mcplib.Description("Branch to resolve the target project to. Required when project is provided."),
		),
	), handleLink)

	s.AddTool(mcplib.NewTool("unlink",
		mcplib.WithDescription("Remove a cross-service resolution override for this worktree, reverting the target project to default same-branch matching. Immediately regenerates the env file and restarts the supervised server if running."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree to modify (defaults to cwd)"),
		),
		mcplib.WithString("project",
			mcplib.Required(),
			mcplib.Description("Target project name to remove the link override for"),
		),
	), handleUnlink)

	s.AddTool(mcplib.NewTool("setup",
		mcplib.WithDescription("Allocate resources and configure a worktree environment. Assigns non-conflicting ports, optionally clones a template database, and writes resolved environment variables to the env file. Requires .treeline.yml in the project. Idempotent — safe to re-run on an already-setup worktree."),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the worktree to set up (defaults to cwd)"),
		),
		mcplib.WithString("main_repo",
			mcplib.Description("Path to the main repository. Auto-detected from the worktree if omitted."),
		),
		mcplib.WithBoolean("dry_run",
			mcplib.Description("If true, show what would be allocated without writing anything"),
		),
	), handleSetup)

	s.AddTool(mcplib.NewTool("new",
		mcplib.WithDescription("Create a git worktree and allocate resources in one step. If the branch exists, checks it out; otherwise creates it from the base branch. Then runs setup to allocate ports, database, and generate the env file. Must be called from the main repository, not from inside a worktree. For projects without .treeline.yml, returns an error — run 'gtl init' first."),
		mcplib.WithString("branch",
			mcplib.Required(),
			mcplib.Description("Branch name to create or check out"),
		),
		mcplib.WithString("path",
			mcplib.Description("Absolute path to the main repository (defaults to cwd)"),
		),
		mcplib.WithString("base",
			mcplib.Description("Base branch for new branch creation (defaults to current branch)"),
		),
		mcplib.WithString("worktree_path",
			mcplib.Description("Custom path for the new worktree directory. If omitted, uses the configured template or ../project-branch convention."),
		),
		mcplib.WithBoolean("dry_run",
			mcplib.Description("If true, show what would happen without creating anything"),
		),
		mcplib.WithBoolean("open",
			mcplib.Description("If true, include the worktree URL in the response for the client to open"),
		),
	), handleNew)
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
