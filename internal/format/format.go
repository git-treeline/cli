// Package format provides shared formatting utilities for CLI output
// and registry allocation field extraction.
package format

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/worktree"
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
		// worktree whose checkout is already gone degrades to no args (and to
		// no template guard below).
		pc := config.LoadProjectConfig(GetStr(a, "worktree"))
		// An unparseable config must refuse the drop, not degrade to "no
		// template configured" — same rule handleProjectRename applies: a
		// config-derived destructive decision on garbage input loses data.
		if cfgErr := pc.LoadError(); cfgErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s — refusing to drop %s\n", cfgErr, strings.Join(names, ", "))
			failed = append(failed, names...)
			continue
		}
		adapter, err := database.ForAdapter(adapterName, pc.DatabaseConnArgs())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s, skipping database drop for %s\n", err, strings.Join(names, ", "))
			failed = append(failed, names...)
			continue
		}
		mainEntry := a["main_worktree"] == true
		serverDBs, haveList := listDatabasesOnce(adapter, names)
		for i, name := range names {
			for _, target := range shardTargets(name, i > 0, serverDBs, haveList, keep) {
				if keepAsTemplate(pc, GetStr(a, "worktree"), target, adapterName, mainEntry, i == 0) {
					fmt.Printf("==> Keeping template database %s (clone source); use `gtl db drop` to remove it deliberately\n", target)
					continue
				}
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

// keepAsTemplate reports whether a drop target must be kept because it is (or
// may be) the template database — the clone source every future worktree
// needs, which is never dropped as a side effect (same rule
// handleProjectRename applies on rename; deliberate removal stays with
// `gtl db drop`). Three cases:
//
//   - The primary of a main-worktree entry IS the template by construction
//     (allocateMain), even when the config's template was since renamed and
//     the stored name no longer matches.
//   - On server adapters, a name equal to the configured template denotes the
//     same server-global object regardless of which worktree tracked it.
//   - On sqlite a database's identity is (directory, name): a per-worktree
//     clone legitimately shares the template's file name, so the name match
//     only counts for the main worktree's entry, whose files live beside the
//     template.
func keepAsTemplate(pc *config.ProjectConfig, worktree, target, adapterName string, mainEntry, primary bool) bool {
	if mainEntry && primary {
		return true
	}
	if !pc.IsTemplateDatabase(target) {
		return false
	}
	if adapterName != "sqlite" {
		return true
	}
	// Registry entries written before the main_worktree flag existed leave
	// the sqlite name match ambiguous: main entry (the template file itself)
	// or a per-worktree clone? Ask git which directory is the main repo; a
	// directory git can't answer for keeps the database — a destroyed
	// template is unrecoverable, a leaked clone file is not.
	return mainEntry || detectMainRepo(worktree) == worktree
}

// detectMainRepo is worktree.DetectMainRepo, injectable for tests (the real
// one shells out to git, which a bare test tempdir can't answer).
var detectMainRepo = worktree.DetectMainRepo

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

var ErrDatabaseCleanupConfig = errors.New("database_cleanup_config_changed")
var ErrDatabaseCleanupPlan = errors.New("database_cleanup_plan_unavailable")
var ErrDatabaseCleanupEffect = errors.New("database_cleanup_effect_unknown")

type DatabaseCleanupCheck struct {
	ValidateOnly   bool
	ConfigRevision string
	BeforeDrop     func() error
	NewAdapter     func(string, []string) (database.Adapter, error)
}

// DatabaseCleanupConfigRevision binds the exact existing config bytes. Its caller
// must establish that this configuration identifies the intended allocation.
func DatabaseCleanupConfigRevision(root string) (string, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(root) || canonical != root {
		return "", ErrDatabaseCleanupConfig
	}
	name := filepath.Join(root, config.ProjectConfigFile)
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 1024*1024 {
		return "", ErrDatabaseCleanupConfig
	}
	fd, err := syscall.Open(name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", ErrDatabaseCleanupConfig
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", ErrDatabaseCleanupConfig
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	after, statErr := os.Lstat(name)
	if err != nil || statErr != nil || len(data) > 1024*1024 || !os.SameFile(before, after) || !after.Mode().IsRegular() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", ErrDatabaseCleanupConfig
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// DropDatabasesChecked prepares the complete target set before the first drop.
// attempted remains true after any dispatched drop, including a partial failure.
// The caller owns lifecycle exclusion; these checks do not lock external writers.
func DropDatabasesChecked(ctx context.Context, root string, snapshot registry.CleanupSnapshot, keep map[string]bool, check DatabaseCleanupCheck) (attempted bool, err error) {
	names := append([]string(nil), snapshot.Databases...)
	keep = maps.Clone(keep)
	if len(names) > 256 || len(check.ConfigRevision) != 64 || check.BeforeDrop == nil {
		return false, ErrDatabaseCleanupPlan
	}
	recheck := func() error {
		if ctx.Err() != nil {
			return ErrDatabaseCleanupPlan
		}
		if err := check.BeforeDrop(); err != nil {
			return ErrDatabaseCleanupPlan
		}
		actual, err := DatabaseCleanupConfigRevision(root)
		if err != nil || actual != check.ConfigRevision {
			return ErrDatabaseCleanupConfig
		}
		return nil
	}
	if err := recheck(); err != nil {
		return false, err
	}
	if len(names) == 0 {
		return false, nil
	}
	pc := config.LoadProjectConfigReadOnlyQuiet(root)
	if pc.LoadError() != nil || pc.Validate() != nil || !checkedDatabaseConfig(pc) {
		return false, ErrDatabaseCleanupPlan
	}
	if err := recheck(); err != nil {
		return false, err
	}
	adapterName := snapshot.DatabaseAdapter
	if adapterName == "" {
		adapterName = "postgresql"
	}
	if (adapterName != "postgresql" && adapterName != "sqlite") || pc.DatabaseAdapter() != adapterName {
		return false, ErrDatabaseCleanupPlan
	}
	for _, name := range names {
		if name == "" || len(name) > 4096 || !utf8.ValidString(name) || strings.ContainsRune(name, 0) || adapterName == "postgresql" && !config.IsValidIdentifier(name) {
			return false, ErrDatabaseCleanupPlan
		}
	}
	factory := check.NewAdapter
	if factory == nil {
		factory = database.ForCheckedCleanup
	}
	adapter, err := factory(adapterName, pc.DatabaseConnArgs())
	if err != nil || adapter == nil {
		return false, ErrDatabaseCleanupPlan
	}
	var listed []string
	haveList := false
	if len(names) > 1 {
		if lister, ok := adapter.(databaseLister); ok {
			listed, err = lister.ListDatabases()
			if err != nil || len(listed) > 4096 {
				return false, ErrDatabaseCleanupPlan
			}
			for _, name := range listed {
				if !config.IsValidIdentifier(name) {
					return false, ErrDatabaseCleanupPlan
				}
			}
			haveList = true
		} else if adapterName != "sqlite" {
			return false, ErrDatabaseCleanupPlan
		}
	}
	var targets []string
	seen := map[string]bool{}
	for index, name := range names {
		for _, target := range shardTargets(name, index > 0, listed, haveList, keep) {
			if keepAsTemplate(pc, root, target, adapterName, snapshot.MainWorktree, index == 0) || seen[target] {
				continue
			}
			seen[target] = true
			if adapterName == "sqlite" {
				if err := checkedSQLiteTarget(root, target); err != nil {
					return false, err
				}
			}
			targets = append(targets, target)
		}
	}
	if check.ValidateOnly {
		return false, recheck()
	}
	for _, target := range targets {
		if err := recheck(); err != nil {
			return attempted, err
		}
		if adapterName == "sqlite" {
			if err := checkedSQLiteTarget(root, target); err != nil {
				return attempted, err
			}
			target = filepath.Join(root, target)
		}
		attempted = true
		if err := adapter.Drop(target); err != nil {
			return true, ErrDatabaseCleanupEffect
		}
	}
	return attempted, recheck()
}

func checkedSQLiteTarget(root, name string) error {
	if name == "." || name == ".." || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "../") {
		return ErrDatabaseCleanupPlan
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		parts := strings.Split(name+suffix, string(filepath.Separator))
		current := root
		for index, part := range parts {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				break
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 || (index < len(parts)-1 && !info.IsDir()) || (index == len(parts)-1 && !info.Mode().IsRegular()) {
				return ErrDatabaseCleanupPlan
			}
		}
	}
	return nil
}

func checkedDatabaseConfig(pc *config.ProjectConfig) bool {
	fields, ok := pc.Data["database"].(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"adapter", "template", "host", "user"} {
		value, exists := fields[key]
		if !exists || key == "template" && value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok || len(text) > 4096 || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return false
		}
	}
	if value, exists := fields["port"]; exists {
		switch port := value.(type) {
		case string:
			if port == "" {
				return true
			}
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
				return false
			}
		case int:
			if port < 1 || port > 65535 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
