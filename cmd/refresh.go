package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	refreshDryRun bool
	refreshForce  bool
)

func init() {
	refreshCmd.Flags().BoolVar(&refreshDryRun, "dry-run", false, "Show what would change without doing it")
	refreshCmd.Flags().BoolVarP(&refreshForce, "force", "f", false, "Skip confirmation prompt")
	rootCmd.AddCommand(refreshCmd)
}

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Re-allocate all registered worktrees with current config and reservations",
	Long: `Walks the allocation registry and re-resolves port assignments based on
current user config (reservations) and project config (port_count).

Supervised servers (started via gtl start) are restarted automatically.
Servers started manually are listed as needing a manual restart.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRefresh()
	},
}

type refreshChange struct {
	worktree    string
	project     string
	branch      string
	displayName string
	oldPorts    []int
	reason      string
	supervised  bool
	listening   bool
	isMain      bool
}

func runRefresh() error {
	uc := config.LoadUserConfig("")
	reg := registry.New("")
	allocs := reg.Allocations()
	reservations := uc.PortReservations()

	if len(allocs) == 0 {
		fmt.Println("No allocations in registry.")
		return nil
	}

	fmt.Printf("Scanning %d allocation(s)...\n\n", len(allocs))

	var changes []refreshChange
	var unchanged, stale int

	for _, entry := range allocs {
		wt := format.GetStr(format.Allocation(entry), "worktree")
		project := format.GetStr(format.Allocation(entry), "project")
		branch := format.GetStr(format.Allocation(entry), "branch")
		ports := format.GetPorts(format.Allocation(entry))
		displayName := format.DisplayName(format.Allocation(entry))

		if _, err := os.Stat(wt); err != nil {
			stale++
			continue
		}
		if len(ports) == 0 {
			unchanged++
			continue
		}

		mainRepo := worktree.DetectMainRepo(wt)
		isMain := mainRepo == wt
		needsChange, reason := detectPortChange(project, branch, ports, isMain, reservations, uc, wt)

		if !needsChange {
			unchanged++
			continue
		}

		sockPath := supervisor.SocketPath(wt)
		status, err := supervisor.Send(sockPath, "status")
		isSupervised := err == nil && (status == "running" || status == "stopped")
		isListening := !isSupervised && allocator.CheckPortsListening(ports)

		changes = append(changes, refreshChange{
			worktree:    wt,
			project:     project,
			branch:      branch,
			displayName: displayName,
			oldPorts:    ports,
			reason:      reason,
			supervised:  isSupervised,
			listening:   isListening,
			isMain:      isMain,
		})
	}

	if len(changes) == 0 {
		fmt.Println("All allocations are up to date.")
		if stale > 0 {
			fmt.Printf("(%d stale entries — run gtl prune --stale to clean up)\n", stale)
		}
		return nil
	}

	var autoRestart, manualRestart int
	for _, c := range changes {
		portLabel := fmt.Sprintf(":%d → (re-allocate)", c.oldPorts[0])
		if p := reservedPort(c.project, c.branch, c.isMain, reservations); p > 0 {
			portLabel = fmt.Sprintf(":%d → :%d", c.oldPorts[0], p)
		}

		status := ""
		if c.supervised {
			autoRestart++
			status = "  (supervised, will restart)"
		} else if c.listening {
			manualRestart++
			status = fmt.Sprintf("  ⚠ port %d in use, needs manual restart", c.oldPorts[0])
		}

		fmt.Printf("  %-24s %s%s\n", c.displayName, portLabel, status)
		fmt.Printf("  %-24s %s\n", "", c.reason)
	}

	fmt.Println()
	summary := fmt.Sprintf("%d allocation(s) to update", len(changes))
	if autoRestart > 0 {
		summary += fmt.Sprintf(", %d supervised server(s) will restart", autoRestart)
	}
	if manualRestart > 0 {
		summary += fmt.Sprintf(", %d server(s) need manual restart", manualRestart)
	}
	fmt.Println(summary + ".")

	if stale > 0 {
		fmt.Printf("(%d stale entries skipped — run gtl prune --stale to clean up)\n", stale)
	}

	if refreshDryRun {
		fmt.Println("\nDry run — no changes made.")
		return nil
	}

	if !confirm.Prompt("\nProceed?", refreshForce, nil) {
		fmt.Println("Aborted.")
		return nil
	}

	fmt.Println()
	var manualWarnings []refreshChange
	var succeeded, failed int
	for _, c := range changes {
		if c.supervised {
			sockPath := supervisor.SocketPath(c.worktree)
			if _, err := supervisor.Send(sockPath, "stop"); err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ %s: could not stop supervisor: %s\n", c.displayName, err)
			}
		}

		mainRepo := worktree.DetectMainRepo(c.worktree)
		s := setup.New(c.worktree, mainRepo, uc)
		s.Options.RefreshOnly = true
		s.Log = io.Discard

		newAlloc, err := s.Run()
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  ✗ %s: %s\n", c.displayName, err)
			continue
		}

		succeeded++
		fmt.Printf("  ✓ %s: :%d → :%s\n", c.displayName, c.oldPorts[0], format.JoinInts(newAlloc.Ports, ", "))

		if c.supervised {
			sockPath := supervisor.SocketPath(c.worktree)
			if _, err := supervisor.Send(sockPath, "start"); err == nil {
				fmt.Printf("    Restarted via supervisor\n")
			} else {
				fmt.Fprintf(os.Stderr, "    ⚠ Failed to restart: %s\n", err)
			}
		}

		if c.listening {
			manualWarnings = append(manualWarnings, c)
		}
	}

	if len(manualWarnings) > 0 {
		fmt.Printf("\n⚠  %d server(s) need manual restart:\n", len(manualWarnings))
		for _, c := range manualWarnings {
			fmt.Printf("  %s (was :%d, now check your .env for the new port)\n", c.displayName, c.oldPorts[0])
		}
	}

	fmt.Printf("\nDone. %d succeeded", succeeded)
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println(".")
	return nil
}

func detectPortChange(project, branch string, currentPorts []int, isMain bool, reservations map[string]int, uc *config.UserConfig, wtPath string) (bool, string) {
	expected := reservedPort(project, branch, isMain, reservations)

	if expected > 0 && expected != currentPorts[0] {
		key := project
		if branch != "" {
			if _, ok := reservations[project+"/"+branch]; ok {
				key = project + "/" + branch
			}
		}
		return true, fmt.Sprintf("reservation %s → %d", key, expected)
	}

	if expected == 0 {
		reserved := uc.ReservedPorts()
		if reserved[currentPorts[0]] {
			return true, fmt.Sprintf("port %d is now reserved by another project", currentPorts[0])
		}
	}

	pc := config.LoadProjectConfig(wtPath)
	if pc != nil && len(currentPorts) != pc.PortsNeeded() {
		return true, fmt.Sprintf("port_count changed (%d → %d)", len(currentPorts), pc.PortsNeeded())
	}

	return false, ""
}

func reservedPort(project, branch string, isMain bool, reservations map[string]int) int {
	if branch != "" {
		if p, ok := reservations[project+"/"+branch]; ok {
			return p
		}
	}
	if isMain {
		if p, ok := reservations[project]; ok {
			return p
		}
	}
	return 0
}
