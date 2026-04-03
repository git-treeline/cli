// Package registry provides persistent allocation state with file locking.
// Allocations (port, database, Redis assignments) are stored in registry.json
// and protected by advisory file locks to support concurrent CLI invocations.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/git-treeline/git-treeline/internal/platform"
)

func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

const lockTimeout = 5 * time.Second

// Allocation is a map representing a registry entry with fields like
// "worktree", "port", "ports", "database", "database_adapter", "project", etc.
type Allocation map[string]any

// RegistryData is the JSON structure stored in registry.json.
type RegistryData struct {
	Version     int          `json:"version"`
	Allocations []Allocation `json:"allocations"`
}

// Registry manages persistent allocation state in a JSON file.
// All mutating operations use file locking to prevent corruption.
type Registry struct {
	Path string
}

func New(path string) *Registry {
	if path == "" {
		path = platform.RegistryFile()
	}
	return &Registry{Path: path}
}

func (r *Registry) Allocations() []Allocation {
	data := r.load()
	return data.Allocations
}

func (r *Registry) Find(worktreePath string) Allocation {
	resolved := resolvePath(worktreePath)
	for _, a := range r.Allocations() {
		if resolvePath(getString(a, "worktree")) == resolved {
			return a
		}
	}
	return nil
}

func (r *Registry) FindByProject(project string) []Allocation {
	var result []Allocation
	for _, a := range r.Allocations() {
		if getString(a, "project") == project {
			result = append(result, a)
		}
	}
	return result
}

func (r *Registry) UsedPorts() []int {
	var ports []int
	for _, a := range r.Allocations() {
		if ps, ok := a["ports"].([]any); ok {
			for _, p := range ps {
				if f, ok := p.(float64); ok {
					ports = append(ports, int(f))
				}
			}
		} else if p, ok := a["port"].(float64); ok {
			ports = append(ports, int(p))
		}
	}
	return ports
}

func (r *Registry) UsedRedisDbs() []int {
	var dbs []int
	for _, a := range r.Allocations() {
		if v, ok := a["redis_db"].(float64); ok {
			dbs = append(dbs, int(v))
		}
	}
	return dbs
}

func (r *Registry) Allocate(entry Allocation) error {
	return r.withLock(func(data *RegistryData) {
		// Normalize worktree path to canonical form (resolve symlinks)
		// This ensures consistent matching on systems like macOS where
		// /var is a symlink to /private/var
		worktree := getString(entry, "worktree")
		resolved := resolvePath(worktree)
		entry["worktree"] = resolved

		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			if getString(a, "worktree") != resolved {
				filtered = append(filtered, a)
			}
		}
		if entry["allocated_at"] == nil {
			entry["allocated_at"] = time.Now().UTC().Format(time.RFC3339)
		}
		data.Allocations = append(filtered, entry)
	})
}

func (r *Registry) Release(worktreePath string) (bool, error) {
	resolved := resolvePath(worktreePath)
	removed := false
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			if resolvePath(getString(a, "worktree")) == resolved {
				removed = true
			} else {
				filtered = append(filtered, a)
			}
		}
		data.Allocations = filtered
	})
	return removed, err
}

// FindMergedAllocations returns allocations whose worktree path maps to a
// branch in the merged set. worktreeBranches maps worktree paths to branch
// names (from git worktree list). Paths are compared using canonical form
// (symlinks resolved) since Allocate normalizes paths on write.
func (r *Registry) FindMergedAllocations(mergedBranches []string, worktreeBranches map[string]string) []Allocation {
	branchSet := make(map[string]bool, len(mergedBranches))
	for _, b := range mergedBranches {
		branchSet[b] = true
	}

	var result []Allocation
	for _, a := range r.Allocations() {
		wtPath := resolvePath(getString(a, "worktree"))
		if branch, ok := worktreeBranches[wtPath]; ok && branchSet[branch] {
			result = append(result, a)
		}
	}
	return result
}

// ReleaseMany removes all allocations whose worktree paths match the given
// list. Uses a single lock acquisition. Returns the number of entries removed.
func (r *Registry) ReleaseMany(worktreePaths []string) (int, error) {
	pathSet := make(map[string]bool, len(worktreePaths))
	for _, p := range worktreePaths {
		pathSet[resolvePath(p)] = true
	}

	count := 0
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			if pathSet[resolvePath(getString(a, "worktree"))] {
				count++
			} else {
				filtered = append(filtered, a)
			}
		}
		data.Allocations = filtered
	})
	return count, err
}

func (r *Registry) Prune() (int, error) {
	count := 0
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			wt := getString(a, "worktree")
			if _, err := os.Stat(wt); err == nil {
				filtered = append(filtered, a)
			} else {
				count++
			}
		}
		data.Allocations = filtered
	})
	return count, err
}

// PruneStale removes allocations where the directory doesn't exist OR the
// directory exists but is not a registered git worktree.
func (r *Registry) PruneStale() (int, error) {
	knownWorktrees := listGitWorktrees()

	count := 0
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			wt := getString(a, "worktree")
			if _, err := os.Stat(wt); err != nil {
				count++
				continue
			}
			if knownWorktrees != nil && !knownWorktrees[wt] {
				count++
				continue
			}
			filtered = append(filtered, a)
		}
		data.Allocations = filtered
	})
	return count, err
}

func listGitWorktrees() map[string]bool {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	result := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			result[strings.TrimPrefix(line, "worktree ")] = true
		}
	}
	return result
}

func (r *Registry) withLock(fn func(data *RegistryData)) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}

	lockPath := r.Path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	defer func() { _ = lockFile.Close() }()

	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for registry lock (%s); if no other git-treeline process is running, remove the lock file", lockPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	data := r.load()
	fn(&data)
	return r.save(data)
}

func (r *Registry) load() RegistryData {
	raw, err := os.ReadFile(r.Path)
	if err != nil {
		return RegistryData{Version: 1}
	}
	var data RegistryData
	if err := json.Unmarshal(raw, &data); err != nil {
		return RegistryData{Version: 1}
	}
	if data.Allocations == nil {
		data.Allocations = []Allocation{}
	}
	return data
}

func (r *Registry) save(data RegistryData) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.Path, append(raw, '\n'), 0o644)
}

func getString(a Allocation, key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}
