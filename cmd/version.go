package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/git-treeline/cli/internal/service"
	"github.com/spf13/cobra"
)

// Set via ldflags at build time: -ldflags "-X github.com/git-treeline/cli/cmd.Version=v0.3.0"
var Version = "dev"

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().Bool("json", false, "Output version info as JSON")
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			router := service.RunningRouterVersion()
			out := map[string]interface{}{
				"cli":    Version,
				"router": nil,
			}
			if router != "" {
				out["router"] = router
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
			return
		}
		fmt.Printf("git-treeline %s\n", Version)
	},
}
