package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGHUP)
		defer stop()
		model := tui.NewModel()
		defer model.Close()
		p := tea.NewProgram(model, tea.WithContext(ctx))
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error running dashboard: %v\n", err)
			return err
		}
		return nil
	},
}
