package cmd

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	gtlmcp "github.com/git-treeline/cli/internal/mcp"
	"github.com/spf13/cobra"
)

// mcpToolNames returns the sorted names of every tool the MCP server actually
// registers, derived from a live server instance so this list can never drift
// out of sync with internal/mcp.
func mcpToolNames() []string {
	srv := gtlmcp.NewServer(Version)
	tools := srv.ListTools()
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func init() {
	rootCmd.AddCommand(mcpConfigCmd)
}

var mcpConfigCmd = &cobra.Command{
	Use:   "mcp-config",
	Short: "Show MCP server configuration for AI agent integration",
	Long: `git-treeline includes an MCP server that gives AI agents structured access
to worktree allocations, ports, databases, and server controls.

Your editor starts the server automatically — you never run it directly.
Add the configuration below and agents can query and control your
development environments.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		gtlPath, err := exec.LookPath("gtl")
		if err != nil {
			gtlPath = "gtl"
		}

		fmt.Println("Cursor (.cursor/mcp.json):")
		fmt.Printf("  { \"mcpServers\": { \"gtl\": { \"command\": %q, \"args\": [\"mcp\"] } } }\n", gtlPath)
		fmt.Println()
		fmt.Println("Claude Code:")
		fmt.Printf("  claude mcp add gtl -- %s mcp\n", gtlPath)
		fmt.Println()
		fmt.Printf("Tools provided: %s\n", strings.Join(mcpToolNames(), ", "))
		fmt.Println("Resources:      gtl://allocations, gtl://config/user")

		return nil
	},
}
