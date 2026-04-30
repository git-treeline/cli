package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var envSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Re-sync the env file and editor settings from .treeline.yml",
	Long: `Re-read the env: block from this worktree's .treeline.yml, update the
env file (e.g. .env.local) with current values, and refresh editor settings.

Use this when you've edited .treeline.yml and don't use 'gtl start'
(which syncs automatically).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)

		if err := checkDriftOrAbort(absPath); err != nil {
			return cliErr(cmd, err)
		}

		pc := config.LoadProjectConfig(absPath)
		uc := config.LoadUserConfig("")

		reg := registry.New("")
		entry := reg.Find(absPath)
		if entry == nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("No allocation found for %s", absPath),
				Hint:    "Run 'gtl setup' first to allocate ports and generate the env file.",
			})
		}

		if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Env sync failed: %s", err),
				Hint:    "Check your env: block in .treeline.yml. If using {resolve:...} tokens, ensure the linked project is allocated.",
			})
		}

		tmpl := pc.EnvTemplate()
		keys := make([]string, 0, len(tmpl))
		for k := range tmpl {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		fmt.Printf("Synced %s (%d managed key(s)):\n", pc.EnvFileTarget(), len(keys))
		for _, k := range keys {
			fmt.Printf("  %s\n", k)
		}

		fa := format.Allocation(entry)
		ports := format.GetPorts(fa)
		port := 0
		if len(ports) > 0 {
			port = ports[0]
		}
		branch := worktree.CurrentBranch(absPath)
		results := setup.ConfigureEditor(absPath, pc, uc, port, branch)
		for _, r := range results {
			if r.Err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", r.Label, r.Err)
			} else if r.Path != "" {
				fmt.Printf("  %s updated\n", r.Label)
			}
		}

		return nil
	},
}
