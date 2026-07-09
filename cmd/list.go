package cmd

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

var (
	listJSON    bool
	listProject string
	listCheck   bool
)

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output as JSON")
	listCmd.Flags().StringVar(&listProject, "project", "", "Filter by project name")
	listCmd.Flags().BoolVar(&listCheck, "check", false, "Probe allocated ports to report up/down status")
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered worktrees (scriptable, non-interactive)",
	Long: `List every registered worktree with its project, branch, ports, database,
Redis assignment, path, and status.

Unlike 'gtl dashboard' (an interactive TUI), 'gtl list' prints plain text by
default and JSON with --json, making it suitable for scripts and pipelines.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg := registry.New("")
		allocs := reg.Allocations()
		if listProject != "" {
			allocs = reg.FindByProject(listProject)
		}

		// Probe ports when explicitly requested, and always for JSON so the
		// scriptable contract carries a definite status field.
		probe := listCheck || listJSON

		entries := make([]listEntry, 0, len(allocs))
		for _, a := range allocs {
			entries = append(entries, newListEntry(a, probe))
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Project != entries[j].Project {
				return entries[i].Project < entries[j].Project
			}
			return entries[i].Branch < entries[j].Branch
		})

		if listJSON {
			data, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				return fmt.Errorf("encoding list: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		if len(entries) == 0 {
			fmt.Println("No registered worktrees.")
			return nil
		}
		for _, e := range entries {
			printListEntry(e, listCheck)
		}
		return nil
	},
}

// listEntry is the stable, scriptable shape of one worktree in `gtl list`.
type listEntry struct {
	Project  string `json:"project"`
	Branch   string `json:"branch"`
	Ports    []int  `json:"ports"`
	Database string `json:"database,omitempty"`
	Redis    string `json:"redis,omitempty"`
	Path     string `json:"path"`
	Status   string `json:"status"` // "up" | "down" | "unknown" (not probed)
}

// newListEntry projects a registry allocation into a listEntry, optionally
// probing its ports to determine up/down status.
func newListEntry(a registry.Allocation, probe bool) listEntry {
	fa := format.Allocation(a)
	ports := format.GetPorts(fa)

	status := "unknown"
	if probe {
		if allocator.CheckPortsListening(ports) {
			status = "up"
		} else {
			status = "down"
		}
	}

	return listEntry{
		Project:  format.GetStr(fa, "project"),
		Branch:   format.DisplayName(fa),
		Ports:    ports,
		Database: format.GetStr(fa, "database"),
		Redis:    redisLabel(a),
		Path:     format.GetStr(fa, "worktree"),
		Status:   status,
	}
}

// redisLabel renders a worktree's Redis assignment the same way `gtl status`
// does: a prefix when using the prefixed strategy, else a db slot.
func redisLabel(a registry.Allocation) string {
	if prefix, ok := a["redis_prefix"].(string); ok && prefix != "" {
		return "prefix:" + prefix
	}
	if rdb, ok := a["redis_db"].(float64); ok {
		return fmt.Sprintf("db:%d", int(rdb))
	}
	return ""
}

func printListEntry(e listEntry, showStatus bool) {
	line := fmt.Sprintf("%s  %s", e.Project, e.Branch)
	if len(e.Ports) > 0 {
		line += fmt.Sprintf("  :%s", format.JoinInts(e.Ports, ","))
	}
	if e.Database != "" {
		line += "  db:" + e.Database
	}
	if e.Redis != "" {
		line += "  " + e.Redis
	}
	if showStatus {
		line += "  [" + e.Status + "]"
	}
	line += "  " + e.Path
	fmt.Println(line)
}
