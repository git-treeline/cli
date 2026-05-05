package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	registryCmd.AddCommand(registryValidateCmd)
	registryRepairCmd.Flags().BoolVar(&registryRepairForce, "force", false, "Skip confirmation prompts")
	registryCmd.AddCommand(registryRepairCmd)
	registryCmd.AddCommand(registryForgetCmd)
	rootCmd.AddCommand(registryCmd)
}

var registryRepairForce bool

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Inspect and repair the allocation registry",
	Long: `Tools for working directly with the allocation registry — the JSON file
that maps worktrees to ports, databases, and Redis assignments.

Subcommands:
  validate  Report integrity issues without changing anything
  repair    Fix safe-to-fix issues (with confirmation)
  forget    Drop a single entry by path`,
}

var registryValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Report integrity issues in the registry",
	Long: `Walks the registry and surfaces:
  - missing_worktree   directory listed in registry no longer exists
  - duplicate_worktree two entries share the same worktree path
  - duplicate_branch   two entries claim the same project + branch
  - duplicate_port     a port is held by more than one entry
  - missing_ports      an entry has no ports assigned

Exits 0 when clean, 1 when issues are found. Read-only.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg := registry.New("")
		issues, err := reg.Validate()
		if err != nil {
			return err
		}
		if len(issues) == 0 {
			fmt.Println("Registry is healthy.")
			return nil
		}
		fmt.Printf("Registry has %d issue(s):\n\n", len(issues))
		printIssues(issues)
		return cliErr(cmd, fmt.Errorf("registry has %d unresolved issue(s)", len(issues)))
	},
}

var registryRepairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Fix safe-to-fix registry issues (with confirmation)",
	Long: `Backs up registry.json then applies repairs that are safe to do
automatically: prune entries whose worktree directory no longer exists.

Other findings (duplicate ports, duplicate worktree paths, entries
missing ports) are listed but require manual judgment — the command
points to the right tool ('gtl reallocate', 'gtl registry forget').`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg := registry.New("")
		issues, err := reg.Validate()
		if err != nil {
			return err
		}
		if len(issues) == 0 {
			fmt.Println("Registry is healthy. Nothing to repair.")
			return nil
		}

		fmt.Printf("Found %d issue(s):\n\n", len(issues))
		printIssues(issues)

		autoFixable := 0
		for _, iss := range issues {
			if iss.Kind == "missing_worktree" {
				autoFixable++
			}
		}
		if autoFixable == 0 {
			fmt.Println()
			fmt.Println("No automatic fixes are available. Resolve the issues above manually.")
			return nil
		}

		fmt.Println()
		if !confirm.Prompt(fmt.Sprintf("Prune %d entries with missing worktrees?", autoFixable), registryRepairForce, nil) {
			fmt.Println("Aborted.")
			return nil
		}

		backup, err := reg.Backup(time.Now().UTC().Format("20060102-150405"))
		if err != nil {
			return fmt.Errorf("creating backup before repair: %w", err)
		}
		fmt.Printf("Backed up registry to %s\n", backup)

		count, err := reg.Prune()
		if err != nil {
			return err
		}
		fmt.Printf("Pruned %d entries.\n", count)
		return nil
	},
}

var registryForgetCmd = &cobra.Command{
	Use:   "forget <path>",
	Short: "Drop the registry entry for the given worktree path",
	Long: `Removes one allocation from the registry without touching the worktree
directory, ports, or databases. Useful when an external tool (Conductor,
manual rm -rf) deleted a workspace and the registry still has the
entry. Idempotent — exits 0 even when no entry matches.

Prefer 'gtl prune' for general cleanup; this command is a precise
single-entry removal for tooling integrations.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		reg := registry.New("")
		removed, err := reg.Release(path)
		if err != nil {
			return err
		}
		if removed {
			fmt.Printf("Removed allocation for %s.\n", path)
		} else {
			fmt.Printf("No allocation found for %s.\n", path)
		}
		return nil
	},
}

func printIssues(issues []registry.Issue) {
	// Group by kind for readability.
	for _, iss := range issues {
		marker := "  ⚠"
		fmt.Printf("%s [%s] %s\n", marker, iss.Kind, iss.Detail)
		if iss.Fix != "" {
			fmt.Printf("    fix: %s\n", iss.Fix)
		}
	}
}

// shouldUseRegistryRepair reports whether the doctor should suggest running
// 'gtl registry repair'. Used by integration points that want a soft hint
// without scolding.
func shouldUseRegistryRepair() bool {
	reg := registry.New("")
	issues, err := reg.Validate()
	if err != nil {
		return false
	}
	for _, iss := range issues {
		if iss.Kind == "missing_worktree" {
			return true
		}
	}
	return false
}

