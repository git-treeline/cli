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

// currentVersion is the registry schema version this build writes. load()
// migrates older data forward in memory (see migrate); the bumped version is
// persisted on the next save, so upgrading never requires wiping the file.
const currentVersion = 2

// RepoRef identifies one endpoint of a relationship by durable coordinates:
// the GitHub remote identity (owner/name) plus a branch. It deliberately does
// NOT include a worktree path — paths are unstable across archive/recreate, so
// the live path is resolved at read time from this (repo, branch) pair.
type RepoRef struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

// Edge is a durable, undirected relationship between two (repo, branch)
// endpoints. Stored canonically (A <= B) so each pair has exactly one row.
type Edge struct {
	A         RepoRef `json:"a"`
	B         RepoRef `json:"b"`
	Type      string  `json:"type,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

// Other returns the endpoint of the edge that is not ref. If ref matches
// neither endpoint, the zero RepoRef is returned.
func (e Edge) Other(ref RepoRef) RepoRef {
	switch {
	case e.A == ref:
		return e.B
	case e.B == ref:
		return e.A
	default:
		return RepoRef{}
	}
}

// less reports whether a sorts before b, used to store edges canonically so
// relate(A, B) and relate(B, A) collapse to one row.
func (a RepoRef) less(b RepoRef) bool {
	if a.Repo != b.Repo {
		return a.Repo < b.Repo
	}
	return a.Branch < b.Branch
}

// canonicalEdge orders two endpoints deterministically.
func canonicalEdge(a, b RepoRef) (RepoRef, RepoRef) {
	if b.less(a) {
		return b, a
	}
	return a, b
}

// RegistryData is the JSON structure stored in registry.json.
type RegistryData struct {
	Version     int          `json:"version"`
	Allocations []Allocation `json:"allocations"`
	Edges       []Edge       `json:"edges,omitempty"`
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
	return allocationsUsedPorts(r.Allocations())
}

func (r *Registry) UsedRedisDbs() []int {
	return allocationsUsedRedisDbs(r.Allocations())
}

// allocationsUsedPorts derives every port claimed by the given allocations,
// covering both the "ports" array and the legacy single "port" field. It is a
// pure function so it can be evaluated against an in-lock RegistryData snapshot.
func allocationsUsedPorts(allocs []Allocation) []int {
	var ports []int
	for _, a := range allocs {
		ports = append(ports, ExtractPorts(a)...)
	}
	return ports
}

// allocationsUsedRedisDbs derives every Redis database index claimed by the
// given allocations. Pure, for the same reason as allocationsUsedPorts.
func allocationsUsedRedisDbs(allocs []Allocation) []int {
	var dbs []int
	for _, a := range allocs {
		if v, ok := a["redis_db"].(float64); ok {
			dbs = append(dbs, int(v))
		}
	}
	return dbs
}

// UsedResources is the in-lock snapshot of already-claimed resources handed to
// an AllocateTx compute callback so it can choose non-colliding values.
type UsedResources struct {
	Ports    []int
	RedisDbs []int
}

// AllocateTx performs resource selection and persistence as a single locked
// transaction. Under one lock acquisition it computes the resources already in
// use (excluding any prior entry for worktree, which is being replaced), invokes
// compute to choose a new allocation against that fresh state, and — only if
// compute succeeds — applies the same worktree-filtering, links-preservation,
// allocated_at stamping and append that Allocate does before saving. If compute
// returns an error nothing is written. This closes the read→choose→write race
// where two concurrent callers pick the same Redis DB from a stale snapshot.
func (r *Registry) AllocateTx(worktree string, compute func(used UsedResources) (Allocation, error)) (Allocation, error) {
	resolved := resolvePath(worktree)
	var result Allocation
	err := r.withLockE(func(data *RegistryData) error {
		filtered := make([]Allocation, 0, len(data.Allocations))
		var links map[string]any
		for _, a := range data.Allocations {
			if GetString(a, "worktree") != resolved {
				filtered = append(filtered, a)
			} else if l, ok := a["links"].(map[string]any); ok && len(l) > 0 {
				links = l
			}
		}

		entry, cerr := compute(UsedResources{
			Ports:    allocationsUsedPorts(filtered),
			RedisDbs: allocationsUsedRedisDbs(filtered),
		})
		if cerr != nil {
			return cerr
		}

		entry["worktree"] = resolved
		if links != nil {
			entry["links"] = links
		}
		if entry["allocated_at"] == nil {
			entry["allocated_at"] = time.Now().UTC().Format(time.RFC3339)
		}
		data.Allocations = append(filtered, entry)
		result = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
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

// AllEdges returns every stored relationship edge.
func (r *Registry) AllEdges() []Edge {
	data, err := r.load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load registry: %v\n", err)
	}
	return data.Edges
}

// EdgesFor returns every edge that has ref as one of its endpoints.
func (r *Registry) EdgesFor(ref RepoRef) []Edge {
	var result []Edge
	for _, e := range r.AllEdges() {
		if e.A == ref || e.B == ref {
			result = append(result, e)
		}
	}
	return result
}

// RelateOutcome reports how Relate reconciled a (pair, type) request against
// the stored edge for that pair.
type RelateOutcome int

const (
	// RelateUnchanged means the pair already existed with the requested type.
	RelateUnchanged RelateOutcome = iota
	// RelateCreated means a new edge was inserted.
	RelateCreated
	// RelateUpdated means the pair existed but its Type was changed.
	RelateUpdated
)

// Relate creates or reconciles a durable, symmetric edge between two endpoints.
// Edges dedupe on the (A, B) pair regardless of type, so re-relating an existing
// pair with a different edgeType updates the stored Type in place rather than
// silently dropping the request. It is idempotent: re-relating with the same
// type is a no-op. edgeType defaults to "related" when empty. Returns which of
// create / update / no-op occurred.
func (r *Registry) Relate(a, b RepoRef, edgeType string) (RelateOutcome, error) {
	if edgeType == "" {
		edgeType = "related"
	}
	ca, cb := canonicalEdge(a, b)
	outcome := RelateCreated
	err := r.withLock(func(data *RegistryData) {
		for i, e := range data.Edges {
			if e.A == ca && e.B == cb {
				if e.Type == edgeType {
					outcome = RelateUnchanged
				} else {
					data.Edges[i].Type = edgeType
					outcome = RelateUpdated
				}
				return
			}
		}
		data.Edges = append(data.Edges, Edge{
			A:         ca,
			B:         cb,
			Type:      edgeType,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	})
	return outcome, err
}

// Unrelate removes the edge between two endpoints. It is idempotent:
// unrelating a pair that isn't related is a no-op success. Returns true when
// an edge was removed, false when there was nothing to remove.
func (r *Registry) Unrelate(a, b RepoRef) (bool, error) {
	ca, cb := canonicalEdge(a, b)
	removed := false
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Edge, 0, len(data.Edges))
		for _, e := range data.Edges {
			if e.A == ca && e.B == cb {
				removed = true
				continue
			}
			filtered = append(filtered, e)
		}
		data.Edges = filtered
	})
	return removed, err
}

// GCDanglingEdges removes relationship edges whose BOTH endpoints are
// unresolvable according to resolvable, returning the removed edges. The
// two-sided rule is deliberately conservative: an edge with even one live
// endpoint is kept, so an edge pointing at a worktree that is only temporarily
// archived (its sibling still checked out) survives to be re-linked later. Only
// edges where neither side maps to anything live are treated as truly obsolete
// (typo'd or long-abandoned) and reclaimed.
func (r *Registry) GCDanglingEdges(resolvable func(RepoRef) bool) ([]Edge, error) {
	var removed []Edge
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Edge, 0, len(data.Edges))
		for _, e := range data.Edges {
			if !resolvable(e.A) && !resolvable(e.B) {
				removed = append(removed, e)
				continue
			}
			filtered = append(filtered, e)
		}
		data.Edges = filtered
	})
	return removed, err
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
// directory exists, is a git worktree (not a standalone clone), and is not
// registered with its parent repo's worktree list.
//
// Standalone clones are explicitly preserved: they are full repositories
// not tied to a parent's worktree list, and the older check incorrectly
// flagged them as stale (this is what nuked Conductor workspaces).
func (r *Registry) PruneStale() (int, error) {
	count := 0
	err := r.withLock(func(data *RegistryData) {
		filtered := make([]Allocation, 0, len(data.Allocations))
		for _, a := range data.Allocations {
			if isStaleEntry(a) {
				count++
				continue
			}
			filtered = append(filtered, a)
		}
		data.Allocations = filtered
	})
	return count, err
}

// isStaleEntry returns true when the registry entry should be removed by
// PruneStale. Stale = directory missing, OR the entry is a git worktree
// (not a standalone clone) that no longer appears in the parent repo's
// worktree list.
func isStaleEntry(a Allocation) bool {
	wt := GetString(a, "worktree")
	if wt == "" {
		return false
	}
	info, err := os.Stat(wt)
	if err != nil || !info.IsDir() {
		return true
	}
	gitMarker, err := os.Stat(filepath.Join(wt, ".git"))
	if err != nil {
		// No .git at all — directory is not a git checkout; treat as stale.
		return true
	}
	if gitMarker.IsDir() {
		// Standalone clone — directory exists, full repo. Keep.
		return false
	}
	// Git worktree (.git is a pointer file). Cross-reference parent's
	// worktree list. If the parent can't be queried, prefer to keep the
	// entry rather than nuke it.
	known := listGitWorktreesFor(wt)
	if known == nil {
		return false
	}
	return !known[wt]
}

// listGitWorktreesFor returns the worktrees registered with the parent of
// the given worktree directory (`git -C <dir> worktree list --porcelain`).
// Returns nil if the command fails so callers can default to "keep".
func listGitWorktreesFor(dir string) map[string]bool {
	out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
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
	return r.withLockE(func(data *RegistryData) error {
		fn(data)
		return nil
	})
}

// withLockE runs fn under the exclusive registry file lock, saving only when fn
// returns nil. Callers that must abort the transaction (leaving the file
// untouched) return a non-nil error from fn.
func (r *Registry) withLockE(fn func(data *RegistryData) error) error {
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
					"  Another gtl process is holding the lock. Find and quit it, then retry.\n"+
					"  The lock (%s) releases automatically when that process exits — even\n"+
					"  on a crash — so do not delete it; removing the file breaks mutual\n"+
					"  exclusion for any process still running.",
				waited, lockPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	data, err := r.load()
	if err != nil {
		return err
	}
	if err := fn(&data); err != nil {
		return err
	}
	return r.save(data)
}

func (r *Registry) load() (RegistryData, error) {
	raw, err := os.ReadFile(r.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RegistryData{Version: currentVersion, Allocations: []Allocation{}, Edges: []Edge{}}, nil
		}
		return RegistryData{}, fmt.Errorf("reading registry: %w", err)
	}
	var data RegistryData
	if err := json.Unmarshal(raw, &data); err != nil {
		return RegistryData{}, fmt.Errorf("registry is corrupt (%s): %w — fix or delete the file", r.Path, err)
	}
	migrate(&data)
	return data, nil
}

// migrate upgrades registry data loaded from disk to the current schema in
// place. It is additive and idempotent: existing allocations and edges are
// preserved untouched, absent fields are initialized, and the version is
// bumped so the next save persists the upgrade. This is why a schema change
// never requires deleting registry.json — old files are read, upgraded in
// memory, and written back with their existing contents intact.
func migrate(data *RegistryData) {
	if data.Allocations == nil {
		data.Allocations = []Allocation{}
	}
	// v1 -> v2: introduce relationship edges. Nothing to transform on existing
	// rows; the field simply didn't exist before, so initialize it empty.
	if data.Edges == nil {
		data.Edges = []Edge{}
	}
	if data.Version < currentVersion {
		data.Version = currentVersion
	}
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
