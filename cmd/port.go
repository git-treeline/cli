package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/spf13/cobra"
)

var portJSON bool

func init() {
	portCmd.Flags().BoolVar(&portJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(portCmd)
}

var portCmd = &cobra.Command{
	Use:   "port",
	Short: "Print the allocated port for the current worktree",
	Long:  `Prints the primary allocated port for the current directory's worktree. Useful for scripts, agents, and CI that need the port without parsing status output.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)

		reg := registry.New("")
		entry := reg.Find(absPath)
		if entry == nil {
			return fmt.Errorf("no allocation found for %s — run `gtl setup` first", absPath)
		}

		ports := format.GetPorts(format.Allocation(entry))
		if len(ports) == 0 {
			return fmt.Errorf("allocation exists but has no ports")
		}

		if portJSON {
			data, err := json.MarshalIndent(map[string]any{
				"port":  ports[0],
				"ports": ports,
			}, "", "  ")
			if err != nil {
				return fmt.Errorf("encoding port info: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		fmt.Println(ports[0])
		return nil
	},
}
