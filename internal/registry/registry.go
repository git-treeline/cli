// Package registry provides persistent allocation state with file locking.
// Allocations (port, database, Redis assignments) are stored in registry.json
// and protected by advisory file locks to support concurrent CLI invocations.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/platform"
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
	data, err := r.load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load registry: %v\n", err)
	}
	return data.Allocations
}

func (r *Registry) Find(worktreePath string) Allocation {
	resolved := resolvePath(worktreePath)
	for _, a := range r.Allocations() {
		if resolvePath(GetString(a, "worktree")) == resolved {
			return a
		}
	}
	return nil
}

func (r *Registry) FindByProject(project string) []Allocation {
	var result []Allocation
	for _, a := range r.Allocations() {
		if GetString(a, "project") == project {
			result = append(result, a)
		}
	}
	return result
}

// FindProjectBranch returns the allocation matching both project name and branch.
// Returns nil if no match is found.
func (r *Registry) FindProjectBranch(project, branch string) Allocation {
	for _, a := range r.Allocations() {
		if GetString(a, "project") == project && GetString(a, "branch") == branch {
			return a
		}
	}
	return nil
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
		worktree := GetString(entry, "worktree")
		resolved := resolvePath(worktree)
		entry["worktree"] = resolved

		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			if GetString(a, "worktree") != resolved {
				filtered = append(filtered, a)
			} else if links, ok := a["links"].(map[string]any); ok && len(links) > 0 {
				entry["links"] = links
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
			if resolvePath(GetString(a, "worktree")) == resolved {
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
		wtPath := resolvePath(GetString(a, "worktree"))
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
			if pathSet[resolvePath(GetString(a, "worktree"))] {
				count++
			} else {
				filtered = append(filtered, a)
			}
		}
		data.Allocations = filtered
	})
	return count, err
}

// UpdateField sets a single field on the allocation matching worktreePath.
func (r *Registry) UpdateField(worktreePath, key, value string) error {
	resolved := resolvePath(worktreePath)
	return r.withLock(func(data *RegistryData) {
		for _, a := range data.Allocations {
			if resolvePath(GetString(a, "worktree")) == resolved {
				a[key] = value
				return
			}
		}
	})
}

// SetLink stores a resolve override for the given worktree. When the worktree's
// env template contains {resolve:project}, the link causes it to resolve against
// the specified branch instead of the same-branch default.
func (r *Registry) SetLink(worktreePath, project, branch string) error {
	resolved := resolvePath(worktreePath)
	return r.withLock(func(data *RegistryData) {
		for _, a := range data.Allocations {
			if resolvePath(GetString(a, "worktree")) == resolved {
				links, _ := a["links"].(map[string]any)
				if links == nil {
					links = make(map[string]any)
				}
				links[project] = branch
				a["links"] = links
				return
			}
		}
	})
}

// RemoveLink removes a resolve override, reverting the worktree to same-branch
// default resolution. No-op if the link doesn't exist.
func (r *Registry) RemoveLink(worktreePath, project string) error {
	resolved := resolvePath(worktreePath)
	return r.withLock(func(data *RegistryData) {
		for _, a := range data.Allocations {
			if resolvePath(GetString(a, "worktree")) == resolved {
				links, _ := a["links"].(map[string]any)
				if links != nil {
					delete(links, project)
					if len(links) == 0 {
						delete(a, "links")
					}
				}
				return
			}
		}
	})
}

// GetLinks returns the active resolve overrides for a worktree as a map of
// project name to branch. Returns nil if the worktree has no links.
func (r *Registry) GetLinks(worktreePath string) map[string]string {
	alloc := r.Find(worktreePath)
	if alloc == nil {
		return nil
	}
	raw, _ := alloc["links"].(map[string]any)
	if raw == nil {
		return nil
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

func (r *Registry) Prune() (int, error) {
	count := 0
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			wt := GetString(a, "worktree")
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
			wt := GetString(a, "worktree")
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
	if err := os.MkdirAll(filepath.Dir(r.Path), platform.DirMode); err != nil {
		return err
	}

	lockPath := r.Path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, platform.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	defer func() { _ = lockFile.Close() }()

	start := time.Now()
	deadline := start.Add(lockTimeout)
	for {
		err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			waited := time.Since(start).Round(time.Millisecond)
			return fmt.Errorf(
				"timed out after %s waiting for registry lock\n\n"+
					"  Another gtl process may be holding the lock.\n"+
					"  If no other process is running: rm %s",
				waited, lockPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	data, err := r.load()
	if err != nil {
		return err
	}
	fn(&data)
	return r.save(data)
}

func (r *Registry) load() (RegistryData, error) {
	raw, err := os.ReadFile(r.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RegistryData{Version: 1, Allocations: []Allocation{}}, nil
		}
		return RegistryData{}, fmt.Errorf("reading registry: %w", err)
	}
	var data RegistryData
	if err := json.Unmarshal(raw, &data); err != nil {
		return RegistryData{}, fmt.Errorf("registry is corrupt (%s): %w — fix or delete the file", r.Path, err)
	}
	if data.Allocations == nil {
		data.Allocations = []Allocation{}
	}
	return data, nil
}

func (r *Registry) save(data RegistryData) error {
	dir := filepath.Dir(r.Path)
	if err := os.MkdirAll(dir, platform.DirMode); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".registry-*.json")
	if err != nil {
		return fmt.Errorf("creating temp registry file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Chmod(platform.PrivateFileMode)

	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, r.Path)
}

// GetString extracts a string field from an allocation.
func GetString(a Allocation, key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}

// ExtractPorts extracts the port list from an allocation, handling both
// the "ports" array and legacy single "port" field.
func ExtractPorts(a Allocation) []int {
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
