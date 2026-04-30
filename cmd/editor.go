package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

func init() {
	editorCmd.AddCommand(editorRefreshCmd)
	rootCmd.AddCommand(editorCmd)
}

var editorCmd = &cobra.Command{
	Use:   "editor",
	Short: "Editor integration commands",
}

var editorRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Update editor window title, colors, and theme for the current branch",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		absPath, _ := filepath.Abs(cwd)

		// Load from worktree (not mainRepo) so branch-specific config is respected
		pc := config.LoadProjectConfig(absPath)
		if pc.Project() == "" {
			return cliErr(cmd, errNoProjectConfig())
		}

		uc := config.LoadUserConfig("")
		reg := registry.New("")
		alloc := reg.Find(absPath)

		port := 0
		if alloc != nil {
			if p, ok := alloc["port"].(float64); ok {
				port = int(p)
			}
		}

		branch := worktree.CurrentBranch(absPath)

		results := setup.ConfigureEditor(absPath, pc, uc, port, branch)
		if len(results) == 0 {
			fmt.Fprintln(os.Stderr, "No editor config defined in .treeline.yml")
			return nil
		}

		for _, r := range results {
			if r.Err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", r.Label, r.Err)
			} else if r.Path != "" {
				fmt.Printf("==> %s updated in %s\n", r.Label, filepath.Base(r.Path))
			}
		}
		return nil
	},
}

