package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/templates"
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
		fmt.Fprintln(os.Stderr, style.Warnf("env vars configured but no env_file — values won't be written to disk."))
		fmt.Fprintln(os.Stderr, style.Dimf("  Add an env_file block to .treeline.yml if needed."))
	}

	diags := templates.Diagnose(det)
	for _, d := range diags {
		fmt.Fprintln(os.Stderr)
		if d.Level == "warn" {
			fmt.Fprintln(os.Stderr, style.Warn.Render("Warning:"))
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
		warnServeNotInstalled()

		path := "."
		if len(args) > 0 {
			path = args[0]
		}

		setupAbs, _ := filepath.Abs(path)
		if err := checkDriftOrAbort(setupAbs); err != nil {
			return cliErr(cmd, err)
		}

		uc := config.LoadUserConfig("")
		s := setup.New(path, setupMainRepo, uc)
		s.Options.DryRun = setupDryRun
		if _, err := s.Run(); err != nil {
			return err
		}

		absPath, _ := filepath.Abs(path)
		pc := config.LoadProjectConfig(absPath)
		printSetupDiagnostics(absPath, pc)

		return nil
	},
}
