package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/templates"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var setupMainRepo string
var setupDryRun bool

func init() {
	setupCmd.Flags().StringVar(&setupMainRepo, "main-repo", "", "Path to the main repository (auto-detected if omitted)")
	setupCmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "Print what would be allocated without writing anything")
	rootCmd.AddCommand(setupCmd)
}

func printSetupDiagnostics(absPath string, pc *config.ProjectConfig) {
	det := detect.Detect(absPath)

	hasEnvConfig := pc.EnvTemplate() != nil
	hasEnvFileConfig := pc.HasEnvFileConfig()

	if hasEnvConfig && !hasEnvFileConfig && !det.AutoLoadsEnvFile() {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "⚠  Config has env vars but no env_file block.")
		fmt.Fprintln(os.Stderr, "  gtl start injects these into the process environment,")
		fmt.Fprintln(os.Stderr, "  but they won't be written to disk for other tools.")
		fmt.Fprintln(os.Stderr, "  Add an env_file block to .treeline.yml if your app reads .env files.")
	}

	diags := templates.Diagnose(det)
	for _, d := range diags {
		fmt.Fprintln(os.Stderr)
		if d.Level == "warn" {
			fmt.Fprintln(os.Stderr, "⚠  Action needed:")
		}
		for _, line := range strings.Split(d.Message, "\n") {
			fmt.Fprintf(os.Stderr, "  %s\n", line)
		}
	}
}

var setupCmd = &cobra.Command{
	Use:   "setup [PATH]",
	Short: "Allocate resources and set up a worktree environment",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}

		uc := config.LoadUserConfig("")
		s := setup.New(path, setupMainRepo, uc)
		s.Options.DryRun = setupDryRun
		_, err := s.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}

		absPath, _ := filepath.Abs(path)
		mainRepo := worktree.DetectMainRepo(absPath)
		pc := config.LoadProjectConfig(mainRepo)
		printSetupDiagnostics(absPath, pc)

		return nil
	},
}
