package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

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
		fmt.Println("Tools provided: status, port, list, doctor, db_name, start, stop, restart, config_get")
		fmt.Println("Resources:      gtl://allocations, gtl://config/user")

		return nil
	},
}
