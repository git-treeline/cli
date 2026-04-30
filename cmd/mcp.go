package cmd

import (
	gtlmcp "github.com/git-treeline/cli/internal/mcp"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(mcpCmd)
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server for AI agent integration (started by your editor)",
	Long: `JSON-RPC MCP server over stdio. Your editor starts this automatically —
you don't need to run it directly. Configure it once and agents get
structured access to allocations, ports, databases, and server controls.

Cursor (.cursor/mcp.json):
  { "mcpServers": { "gtl": { "command": "gtl", "args": ["mcp"] } } }

Claude Code:
  claude mcp add gtl -- gtl mcp`,
	Args:   cobra.NoArgs,
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return gtlmcp.Serve(Version)
	},
}
