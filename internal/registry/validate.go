package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Issue describes a registry-integrity finding.
type Issue struct {
	// Kind is a short stable identifier (e.g. "missing_worktree",
	// "duplicate_port", "duplicate_worktree", "duplicate_branch",
	// "missing_ports").
	Kind string
	// Worktree is the entry's worktree path. Empty when the issue spans
	// multiple entries.
	Worktree string
	// Detail is a one-line human-readable description.
	Detail string
	// Fix is a short hint at what to run to repair this. May be empty.
	Fix string
}

// Validate scans the registry and returns a list of integrity findings.
// Read-only — safe to call without holding the lock.
//
// Detected:
//   - Worktree directory listed in the registry no longer exists on disk
//     ("missing_worktree"; fix: gtl prune)
//   - Two entries share the same worktree path ("duplicate_worktree")
//   - Two entries share the same project + branch combo
//     ("duplicate_branch")
//   - A port is allocated to more than one worktree
//     ("duplicate_port")
//   - An entry has no ports assigned at all ("missing_ports"; fix:
//     gtl reallocate)
func (r *Registry) Validate() ([]Issue, error) {
	data, err := r.load()
	if err != nil {
		return nil, err
	}
	return validateData(data), nil
}

func validateData(data RegistryData) []Issue {
	var issues []Issue

	// Per-entry checks.
	worktreeOwners := map[string][]string{}
	branchOwners := map[string][]string{}
	portOwners := map[int][]string{}

	for _, a := range data.Allocations {
		wt := GetString(a, "worktree")
		project := GetString(a, "project")
		branch := GetString(a, "branch")

		if wt != "" {
			if info, err := os.Stat(wt); err != nil || !info.IsDir() {
				issues = append(issues, Issue{
					Kind:     "missing_worktree",
					Worktree: wt,
					Detail:   fmt.Sprintf("worktree directory does not exist: %s", wt),
					Fix:      "gtl prune",
				})
			}
			worktreeOwners[wt] = append(worktreeOwners[wt], wt)
		}

		if project != "" && branch != "" {
			key := project + "@" + branch
			branchOwners[key] = append(branchOwners[key], wt)
		}

		ports := ExtractPorts(a)
		if len(ports) == 0 {
			issues = append(issues, Issue{
				Kind:     "missing_ports",
				Worktree: wt,
				Detail:   "allocation has no ports",
				Fix:      "gtl reallocate",
			})
		}
		for _, p := range ports {
			portOwners[p] = append(portOwners[p], wt)
		}
	}

	// Cross-entry duplicate checks.
	for wt, owners := range worktreeOwners {
		if len(owners) > 1 {
			issues = append(issues, Issue{
				Kind:     "duplicate_worktree",
				Worktree: wt,
				Detail:   fmt.Sprintf("worktree path appears %d times", len(owners)),
				Fix:      "remove the duplicate manually with: gtl config edit",
			})
		}
	}
	for key, owners := range branchOwners {
		if len(owners) > 1 {
			issues = append(issues, Issue{
				Kind:   "duplicate_branch",
				Detail: fmt.Sprintf("project/branch %q owned by %d entries: %v", key, len(owners), owners),
				Fix:    "drop the duplicate with gtl prune --force or 'gtl registry forget <path>'",
			})
		}
	}

	// Sort port keys so output is stable.
	dupPorts := make([]int, 0)
	for p, owners := range portOwners {
		if len(owners) > 1 {
			dupPorts = append(dupPorts, p)
		}
		_ = owners
	}
	sort.Ints(dupPorts)
	for _, p := range dupPorts {
		issues = append(issues, Issue{
			Kind:   "duplicate_port",
			Detail: fmt.Sprintf("port %d is held by %d entries: %v", p, len(portOwners[p]), portOwners[p]),
			Fix:    "run 'gtl reallocate' to assign a fresh port to one of them",
		})
	}

	// Stable order: missing_worktree first (ready for prune), then
	// duplicates, then missing_ports.
	sort.SliceStable(issues, func(i, j int) bool {
		return issueOrder(issues[i].Kind) < issueOrder(issues[j].Kind)
	})
	return issues
}

func issueOrder(kind string) int {
	switch kind {
	case "missing_worktree":
		return 0
	case "duplicate_worktree":
		return 1
	case "duplicate_branch":
		return 2
	case "duplicate_port":
		return 3
	case "missing_ports":
		return 4
	default:
		return 99
	}
}

// Backup writes a copy of the current registry file to <path>.bak-<suffix>.
// Caller picks a suffix (typically a timestamp) so multiple backups don't
// clobber each other. Returns the backup path or an error.
func (r *Registry) Backup(suffix string) (string, error) {
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return "", fmt.Errorf("reading registry: %w", err)
	}
	backupPath := r.Path + ".bak-" + suffix
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return "", err
	}
	return backupPath, nil
}
