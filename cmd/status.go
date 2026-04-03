package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/git-treeline/git-treeline/internal/allocator"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/spf13/cobra"
)

var statusProject string
var statusJSON bool
var statusCheck bool
var statusWatch bool
var statusInterval int

func init() {
	statusCmd.Flags().StringVar(&statusProject, "project", "", "Filter by project name")
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

func renderStatus() error {
	reg := registry.New("")
	allocs := reg.Allocations()
	if statusProject != "" {
		allocs = reg.FindByProject(statusProject)
	}

	if statusCheck {
		for _, a := range allocs {
			ports := format.GetPorts(format.Allocation(a))
			a["listening"] = allocator.CheckPortsListening(ports)
		}
	}

	if statusJSON {
		data, _ := json.MarshalIndent(allocs, "", "  ")
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

			name := format.GetStr(fa, "worktree_name")
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
		}
	}

	return nil
}
