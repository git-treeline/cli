package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/devresources"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/templates"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var setupMainRepo string
var setupDryRun bool
var setupCompleteAllocation string

func init() {
	setupCmd.Flags().StringVar(&setupMainRepo, "main-repo", "", "Path to the main repository (auto-detected if omitted)")
	setupCmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "Print what would be allocated without writing anything")
	setupCmd.Flags().StringVar(&setupCompleteAllocation, "complete-allocation", "", "Complete setup for the exact never-started allocation generation without replacing resources")
	setupCmd.MarkFlagsMutuallyExclusive("complete-allocation", "dry-run")
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
		if cmd.Flags().Changed("complete-allocation") && setupCompleteAllocation == "" {
			return fmt.Errorf("--complete-allocation requires an allocation generation")
		}
		if os.Getenv("GTL_DEFER_SETUP") == "1" {
			return nil
		}
		warnServeNotInstalled()

		path := "."
		if len(args) > 0 {
			path = args[0]
		}

		setupAbs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		mainRepo := setupMainRepo
		if devresources.Active() {
			if _, err := devresources.Check(); err != nil {
				return err
			}
			if mainRepo == "" {
				mainRepo = worktree.DetectMainRepo(setupAbs)
			}
			pc := config.LoadProjectConfigReadOnlyQuiet(setupAbs)
			if err := setup.ValidateManagedDevProject(setupAbs, mainRepo, pc); err != nil {
				return err
			}
		}
		var completionLocks *registry.LifecycleLocks
		if !setupDryRun {
			setupAbs, err = registry.CanonicalLifecycleTarget(path)
			if err != nil {
				return err
			}
			if mainRepo == "" {
				mainRepo = worktree.DetectMainRepo(setupAbs)
			}
			mainRepo, err = registry.CanonicalLifecycleTarget(mainRepo)
			if err != nil {
				return err
			}
			targets := []string{setupAbs, mainRepo}
			locks, err := acquireCommandLifecycleLocks(cmd.Context(), targets)
			if err != nil {
				return err
			}
			// Cleanup does not change the completed operation or its receipt.
			defer func() { _ = locks.Close() }()
			completionLocks = locks
			if err := registry.New(setup.RegistryPath).CheckReleaseReservations(targets); err != nil {
				return cliErr(cmd, err)
			}
			if setupCompleteAllocation == "" {
				if err := checkDriftOrAbortForSetup(setupAbs); err != nil {
					return cliErr(cmd, err)
				}
			}
		}

		uc := config.LoadUserConfig("")
		s := setup.NewWithOptions(setupAbs, mainRepo, uc, setup.Options{DryRun: setupDryRun, CompleteAllocation: setupCompleteAllocation})
		s.CompletionLocks = completionLocks
		if _, err := s.Run(); err != nil {
			return err
		}

		var pc *config.ProjectConfig
		if setupDryRun {
			pc = config.LoadProjectConfigReadOnly(setupAbs)
		} else {
			pc = config.LoadProjectConfig(setupAbs)
		}
		printSetupDiagnostics(setupAbs, pc)

		return nil
	},
}
