package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var relatedJSON bool

func init() {
	relatedCmd.Flags().BoolVar(&relatedJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(relatedCmd)
}

var relatedCmd = &cobra.Command{
	Use:   "related",
	Short: "Show worktrees related to the current one",
	Long: `List the worktrees related to the worktree in the current directory.

Combines two sources:
  - edges you created with 'gtl relate' (source: edge)
  - structural siblings declared as related_repos in .treeline.yml (source: config)

Each entry resolves to the live worktree path for that (repo, branch), or null
when it isn't currently checked out (dangling).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		abs, _ := filepath.Abs(cwd)

		reg := registry.New("")
		idx := buildWorktreeIndex(reg.Allocations())

		selfRef, ok := idx.refByPath[resolveIndexPath(idx, abs)]
		if !ok {
			// Not a registered worktree (or no origin remote) — resolve directly.
			repo := worktree.RepoSlugFromRemote(abs)
			if repo == "" {
				return cliErr(cmd, &CliError{
					Message: "This directory has no GitHub 'origin' remote.",
					Hint:    "Relationships are keyed by the origin owner/name; add a remote or run from a worktree that has one.",
				})
			}
			selfRef = registry.RepoRef{Repo: repo, Branch: worktree.CurrentBranch(abs)}
		}

		entries := buildRelated(reg, idx, abs, selfRef)

		if relatedJSON {
			data, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				return fmt.Errorf("encoding related: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		if len(entries) == 0 {
			fmt.Println("No related worktrees.")
			return nil
		}
		for _, e := range entries {
			loc := "(not checked out)"
			if e.Path != nil {
				loc = *e.Path
			}
			fmt.Printf("  %s#%s  [%s/%s]  %s\n", e.Repo, e.Branch, e.Source, e.Type, loc)
		}
		return nil
	},
}

// relatedEntry is one item in a worktree's related[] array, exposed both by
// `gtl related` and inside each `gtl status --json` worktree entry. path is a
// pointer so it marshals to JSON null when the target isn't checked out.
type relatedEntry struct {
	Repo     string  `json:"repo"`
	Branch   string  `json:"branch"`
	Path     *string `json:"path"`
	Type     string  `json:"type"`
	Source   string  `json:"source"` // "edge" (runtime relate) | "config" (declared sibling)
	Dangling bool    `json:"dangling"`
	// CreatedAt is the edge's creation timestamp (RFC3339). Present only for
	// source "edge"; omitted for config siblings, which have no per-edge time.
	// Treeline uses it for recency ordering and stable cross-poll diffing.
	CreatedAt string `json:"createdAt,omitempty"`
}

// worktreeIndex maps live worktree paths to their durable (repo, branch)
// identity and back, so an edge endpoint can be resolved to a live path.
type worktreeIndex struct {
	refByPath map[string]registry.RepoRef
	pathByRef map[registry.RepoRef]string
}

// buildWorktreeIndex resolves the (repo, branch) identity of every allocation
// concurrently. Branch comes from the allocation (already synced by status);
// only the repo slug requires a git call, mirroring status's existing per-
// worktree git fan-out.
func buildWorktreeIndex(allocs []registry.Allocation) *worktreeIndex {
	idx := &worktreeIndex{
		refByPath: make(map[string]registry.RepoRef),
		pathByRef: make(map[registry.RepoRef]string),
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, a := range allocs {
		wt := registry.GetString(a, "worktree")
		branch := registry.GetString(a, "branch")
		if wt == "" {
			continue
		}
		wg.Add(1)
		go func(wt, branch string) {
			defer wg.Done()
			repo := worktree.RepoSlugFromRemote(wt)
			if repo == "" {
				return
			}
			ref := registry.RepoRef{Repo: repo, Branch: branch}
			mu.Lock()
			idx.refByPath[wt] = ref
			idx.pathByRef[ref] = wt
			mu.Unlock()
		}(wt, branch)
	}
	wg.Wait()
	return idx
}

// resolveIndexPath returns the key under which selfPath is indexed, accounting
// for symlink resolution done at allocation time. Falls back to selfPath.
func resolveIndexPath(idx *worktreeIndex, selfPath string) string {
	if _, ok := idx.refByPath[selfPath]; ok {
		return selfPath
	}
	if resolved, err := filepath.EvalSymlinks(selfPath); err == nil {
		if _, ok := idx.refByPath[resolved]; ok {
			return resolved
		}
	}
	return selfPath
}

// buildRelated assembles the related[] entries for the worktree identified by
// selfRef. Edges (explicit relate) and config siblings (declared topology) are
// merged into one list, deduped by (repo, branch) with edges taking priority.
func buildRelated(reg *registry.Registry, idx *worktreeIndex, selfPath string, selfRef registry.RepoRef) []relatedEntry {
	seen := make(map[registry.RepoRef]bool)
	// Always a non-nil slice so an empty result marshals to [] not null,
	// giving Treeline a stable array contract.
	entries := []relatedEntry{}

	add := func(ref registry.RepoRef, typ, source, createdAt string) {
		if ref.Repo == "" || seen[ref] {
			return
		}
		seen[ref] = true
		var pathPtr *string
		dangling := true
		if p, ok := idx.pathByRef[ref]; ok && p != "" {
			path := p
			pathPtr = &path
			dangling = false
		}
		entries = append(entries, relatedEntry{
			Repo:      ref.Repo,
			Branch:    ref.Branch,
			Path:      pathPtr,
			Type:      typ,
			Source:    source,
			Dangling:  dangling,
			CreatedAt: createdAt,
		})
	}

	// 1. Explicit runtime edges. These are specific (repo, branch) pairs.
	for _, e := range reg.EdgesFor(selfRef) {
		other := e.Other(selfRef)
		typ := e.Type
		if typ == "" {
			typ = "related"
		}
		add(other, typ, "edge", e.CreatedAt)
	}

	// 2. Declared structural siblings from .treeline.yml. A related_repo names a
	// repo, not a branch; expand it to every live worktree of that repo.
	root := worktree.DetectRepoRoot(selfPath)
	pc := config.LoadProjectConfig(root)
	for _, rr := range pc.RelatedRepos() {
		matchedLive := false
		for path, ref := range idx.refByPath {
			if ref.Repo != rr.Repo || ref == selfRef {
				continue
			}
			_ = path
			add(ref, rr.Type, "config", "")
			matchedLive = true
		}
		if !matchedLive {
			// Declared but nothing checked out — surface it as dangling so the
			// relationship is still visible.
			add(registry.RepoRef{Repo: rr.Repo}, rr.Type, "config", "")
		}
	}

	return entries
}
