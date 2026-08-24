// Package format provides shared formatting utilities for CLI output
// and registry allocation field extraction.
package format

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
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
// Prints warnings to stderr for any failures but continues processing remaining
// databases. Returns a non-nil error naming the databases that failed to drop so
// callers can report accurately and exit non-zero.
//
// Every name in the entry's `databases` list is dropped, including framework-
// derived parallel-test shards (`<name>_0..N`) when the adapter can list
// databases. keep holds database names still tracked by live registry entries;
// matching names are never dropped, so a name collision across worktrees can't
// destroy a live database. keep may be nil.
func DropDatabases(allocs []Allocation, keep map[string]bool) error {
	var failed []string
	for _, a := range allocs {
		names := registry.ExtractDatabases(registry.Allocation(a))
		if len(names) == 0 {
			continue
		}
		adapterName := GetStr(a, "database_adapter")
		// Connection args come from the worktree's project config so listing
		// and dropping hit the configured server, not psql's default one. A
		// worktree whose checkout is already gone degrades to no args.
		connArgs := config.LoadProjectConfig(GetStr(a, "worktree")).DatabaseConnArgs()
		adapter, err := database.ForAdapter(adapterName, connArgs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s, skipping database drop for %s\n", err, strings.Join(names, ", "))
			failed = append(failed, names...)
			continue
		}
		serverDBs, haveList := listDatabasesOnce(adapter, names)
		for i, name := range names {
			for _, target := range shardTargets(name, i > 0, serverDBs, haveList, keep) {
				dropTarget := target
				if adapterName == "sqlite" {
					dropTarget = filepath.Join(GetStr(a, "worktree"), target)
				}
				fmt.Printf("==> Dropping database %s\n", target)
				if err := adapter.Drop(dropTarget); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to drop database %s: %s\n", target, err)
					failed = append(failed, fmt.Sprintf("%s (%s)", target, err))
				}
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to drop database(s): %s", strings.Join(failed, "; "))
	}
	return nil
}

// databaseLister is implemented by adapters that can enumerate databases on
// the server (PostgreSQL). Used to find framework-derived parallel-test
// shards; adapters without it (SQLite) drop by exact name only.
type databaseLister interface {
	ListDatabases() ([]string, error)
}

// listDatabasesOnce fetches the server's database list a single time per
// allocation, and only when shard expansion will need it: there are auxiliary
// entries and the adapter can list. A failed listing warns rather than
// blocking the release — every entry then drops by exact name (Drop is
// if-exists, so absence is harmless).
func listDatabasesOnce(adapter database.Adapter, names []string) ([]string, bool) {
	if len(names) < 2 {
		return nil, false
	}
	lister, ok := adapter.(databaseLister)
	if !ok {
		return nil, false
	}
	all, err := lister.ListDatabases()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not list databases to find parallel-test shards (%s); dropping exact names only\n", err)
		return nil, false
	}
	return all, true
}

// shardTargets returns the concrete databases to drop for one entry of an
// allocation's list. Only auxiliary entries (aux) get shard expansion —
// frameworks derive parallel-test shards (name_0..N) from those names, never
// from the primary, and on the main worktree the primary is the template
// itself, so a pattern sweep there could catch unrelated numbered databases.
// Names in keep are never dropped: they belong to live registry entries.
func shardTargets(name string, aux bool, serverDBs []string, haveList bool, keep map[string]bool) []string {
	if !aux || !haveList {
		if keep[name] {
			return nil
		}
		return []string{name}
	}
	var out []string
	for _, db := range serverDBs {
		if isShardOf(db, name) && !keep[db] {
			out = append(out, db)
		}
	}
	sort.Strings(out)
	return out
}

// isShardOf reports whether db is name itself or name_<digits>, the shape
// frameworks use for parallel-test shard databases.
func isShardOf(db, name string) bool {
	if db == name {
		return true
	}
	rest, ok := strings.CutPrefix(db, name+"_")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// DropSingleDB drops every database for a single allocation. Returns a non-nil
// error if any drop failed so callers can report accurately and exit non-zero.
// keep behaves as in DropDatabases and may be nil.
func DropSingleDB(alloc Allocation, worktreePath string, keep map[string]bool) error {
	a := maps.Clone(alloc)
	if a == nil {
		a = Allocation{}
	}
	a["worktree"] = worktreePath
	return DropDatabases([]Allocation{a}, keep)
}
