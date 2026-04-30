// Package resolve provides cross-worktree service discovery. It looks up
// another project's allocated port from the registry, respecting link
// overrides and falling back to same-branch matching.
package resolve

import (
	"fmt"

	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
)

// Resolver resolves cross-worktree URLs from the registry. It checks link
// overrides first, then falls back to same-branch matching.
type Resolver struct {
	Registry     *registry.Registry
	WorktreePath string
	Branch       string
}

// New creates a Resolver for the given worktree and its current branch.
func New(reg *registry.Registry, worktreePath, branch string) *Resolver {
	return &Resolver{Registry: reg, WorktreePath: worktreePath, Branch: branch}
}

// Resolve returns the base URL (http://127.0.0.1:PORT) for a project. Without
// an explicit branch, it checks link overrides then falls back to the current
// worktree's branch. Returns an error if the target is not allocated.
func (r *Resolver) Resolve(project string, explicitBranch ...string) (string, error) {
	targetBranch := r.defaultBranch(project)
	if len(explicitBranch) > 0 && explicitBranch[0] != "" {
		targetBranch = explicitBranch[0]
	}

	alloc := r.Registry.FindProjectBranch(project, targetBranch)
	if alloc == nil {
		return "", fmt.Errorf("no allocation for project %q on branch %q", project, targetBranch)
	}

	fa := format.Allocation(alloc)
	ports := format.GetPorts(fa)
	if len(ports) == 0 {
		return "", fmt.Errorf("allocation for %s/%s has no ports", project, targetBranch)
	}

	return fmt.Sprintf("http://127.0.0.1:%d", ports[0]), nil
}

func (r *Resolver) defaultBranch(project string) string {
	links := r.Registry.GetLinks(r.WorktreePath)
	if branch, ok := links[project]; ok {
		return branch
	}
	return r.Branch
}
