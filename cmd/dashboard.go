package cmd

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/git-treeline/cli/internal/tui"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(dashboardCmd)
}

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"dash", "ui"},
	Short:   "Launch the interactive TUI dashboard",
	Long:    "Real-time terminal dashboard for monitoring and managing all worktrees.",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := tea.NewProgram(tui.NewModel())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error running dashboard: %v\n", err)
			return err
		}
		return nil
	},
}
