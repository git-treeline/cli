package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/spf13/cobra"
)

var statusProject string
var statusJSON bool
var statusCheck bool
var statusWatch bool
var statusInterval int

func init() {
	statusCmd.Flags().StringVar(&statusProject, "project", "", "Filter by project name")
	_ = statusCmd.RegisterFlagCompletionFunc("project", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		reg := registry.New("")
		seen := make(map[string]bool)
		var projects []string
		for _, a := range reg.Allocations() {
			if p, ok := a["project"].(string); ok && !seen[p] {
				seen[p] = true
				projects = append(projects, p)
			}
		}
		return projects, cobra.ShellCompDirectiveNoFileComp
	})
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().BoolVar(&statusCheck, "check", false, "Probe allocated ports to check if services are running")
	statusCmd.Flags().BoolVar(&statusWatch, "watch", false, "Auto-refresh status on a loop (implies --check)")
	statusCmd.Flags().IntVar(&statusInterval, "interval", 5, "Refresh interval in seconds (used with --watch)")
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all active allocations across projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		if statusWatch {
			statusCheck = true
			return runStatusWatch()
		}
		return renderStatus()
	},
}

func runStatusWatch() error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	ticker := time.NewTicker(time.Duration(statusInterval) * time.Second)
	defer ticker.Stop()

	for {
		fmt.Print("\033[H\033[2J") // clear terminal
		if err := renderStatus(); err != nil {
			return err
		}
		fmt.Printf("\nRefreshing every %ds. Ctrl+C to exit.", statusInterval)

		select {
		case <-sig:
			fmt.Println()
			return nil
		case <-ticker.C:
		}
	}
}

func syncBranches(reg *registry.Registry, allocs []registry.Allocation) {
	var wg sync.WaitGroup
	for i := range allocs {
		a := allocs[i]
		wt, _ := a["worktree"].(string)
		if wt == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
			cmd.Dir = wt
			out, err := cmd.Output()
			if err != nil {
				return
			}
			branch := strings.TrimSpace(string(out))
			if branch == "" || branch == "HEAD" {
				return
			}
			old, _ := a["branch"].(string)
			if branch != old {
				a["branch"] = branch
				if err := reg.UpdateField(wt, "branch", branch); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not update branch in registry for %s: %v\n", wt, err)
				}
			}
		}()
	}
	wg.Wait()
}

func renderStatus() error {
	reg := registry.New("")
	allocs := reg.Allocations()
	if statusProject != "" {
		allocs = reg.FindByProject(statusProject)
	}

	syncBranches(reg, allocs)

	if statusCheck || statusJSON {
		for _, a := range allocs {
			ports := format.GetPorts(format.Allocation(a))
			a["listening"] = allocator.CheckPortsListening(ports)
		}
	}

	if statusJSON {
		for _, a := range allocs {
			wt, _ := a["worktree"].(string)
			if wt == "" {
				continue
			}
			sockPath := supervisor.SocketPath(wt)
			if resp, err := supervisor.Send(sockPath, "status"); err == nil {
				a["supervisor"] = resp
			} else {
				a["supervisor"] = "not running"
			}
		}
		data, err := json.MarshalIndent(allocs, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding status: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	if len(allocs) == 0 {
		fmt.Println("No active allocations.")
		return nil
	}

	grouped := make(map[string][]registry.Allocation)
	for _, a := range allocs {
		project := ""
		if p, ok := a["project"].(string); ok {
			project = p
		}
		grouped[project] = append(grouped[project], a)
	}

	for project, entries := range grouped {
		sort.Slice(entries, func(i, j int) bool {
			pi, _ := entries[i]["port"].(float64)
			pj, _ := entries[j]["port"].(float64)
			return pi < pj
		})

		fmt.Printf("\n%s:\n", project)
		for _, a := range entries {
			fa := format.Allocation(a)
			ports := format.GetPorts(fa)
			portLabel := format.JoinInts(ports, ",")

			name := format.DisplayName(fa)
			db := format.GetStr(fa, "database")

			redis := ""
			if prefix, ok := a["redis_prefix"].(string); ok && prefix != "" {
				redis = "prefix:" + prefix
			} else if rdb, ok := a["redis_db"].(float64); ok {
				redis = fmt.Sprintf("db:%d", int(rdb))
			}

			line := fmt.Sprintf("  :%s  %s", portLabel, name)
			if db != "" {
				line += fmt.Sprintf("  db:%s", db)
			}
			if redis != "" {
				line += fmt.Sprintf("  %s", redis)
			}

			if statusCheck {
				if listening, ok := a["listening"].(bool); ok && listening {
					line += "  [up]"
				} else {
					line += "  [down]"
				}
			}

			fmt.Println(line)
			if links, ok := a["links"].(map[string]any); ok && len(links) > 0 {
				for proj, branch := range links {
					if b, ok := branch.(string); ok {
						fmt.Printf("  → %s linked to %s\n", proj, b)
					}
				}
			}
		}
	}

	return nil
}
