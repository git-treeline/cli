package cmd

import (
	"encoding/json"
	"fmt"

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
		alloc, err := currentAllocation()
		if err != nil {
			return cliErr(cmd, err)
		}
		ports := alloc.Ports()
		if len(ports) == 0 {
			return cliErr(cmd, errNoAllocationNoPorts(alloc.Path))
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
