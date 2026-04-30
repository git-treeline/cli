// Package format provides shared formatting utilities for CLI output
// and registry allocation field extraction.
package format

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/database"
)

// JoinInts formats a slice of integers as a string with the given separator.
func JoinInts(ints []int, sep string) string {
	parts := make([]string, len(ints))
	for i, v := range ints {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return strings.Join(parts, sep)
}

// Allocation is a map representing a registry entry. Defined here to avoid
// import cycles between format and registry packages.
type Allocation map[string]any

// GetPorts extracts the port list from an allocation. Returns nil if no ports found.
// Handles both the "ports" array format and legacy single "port" field.
func GetPorts(a Allocation) []int {
	if ps, ok := a["ports"].([]any); ok {
		result := make([]int, 0, len(ps))
		for _, p := range ps {
			if f, ok := p.(float64); ok {
				result = append(result, int(f))
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	if p, ok := a["port"].(float64); ok {
		return []int{int(p)}
	}
	return nil
}

// GetStr extracts a string field from an allocation. Returns empty string if not found.
func GetStr(a Allocation, key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}

// DisplayName returns the best human-readable label for an allocation.
// Prefers branch (if set), falls back to worktree_name.
func DisplayName(a Allocation) string {
	if b := GetStr(a, "branch"); b != "" {
		return b
	}
	return GetStr(a, "worktree_name")
}

// PortDisplay returns a formatted port string like ":3000" or empty if no ports.
func PortDisplay(a Allocation) string {
	ports := GetPorts(a)
	if len(ports) > 0 {
		return fmt.Sprintf(":%d", ports[0])
	}
	return ""
}

// DropDatabases drops databases for the given allocations using the appropriate adapter.
// Prints warnings to stderr for any failures but continues processing remaining databases.
func DropDatabases(allocs []Allocation) {
	for _, a := range allocs {
		db := GetStr(a, "database")
		if db == "" {
			continue
		}
		adapterName := GetStr(a, "database_adapter")
		adapter, err := database.ForAdapter(adapterName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s, skipping database drop for %s\n", err, db)
			continue
		}
		dropTarget := db
		if adapterName == "sqlite" {
			dropTarget = filepath.Join(GetStr(a, "worktree"), db)
		}
		fmt.Printf("==> Dropping database %s\n", db)
		if err := adapter.Drop(dropTarget); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to drop database %s: %s\n", db, err)
		}
	}
}

// DropSingleDB drops the database for a single allocation.
func DropSingleDB(alloc Allocation, worktreePath string) {
	db := GetStr(alloc, "database")
	if db == "" {
		return
	}
	adapterName := GetStr(alloc, "database_adapter")
	adapter, err := database.ForAdapter(adapterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s, skipping database drop\n", err)
		return
	}
	dropTarget := db
	if adapterName == "sqlite" {
		dropTarget = filepath.Join(worktreePath, db)
	}
	fmt.Printf("==> Dropping database %s\n", db)
	if err := adapter.Drop(dropTarget); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to drop database: %s\n", err)
	}
}
