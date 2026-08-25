package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/process"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var statusProject string
var statusJSON bool
var statusCheck bool
var statusWatch bool
var statusInterval int

func init() {
	statusCmd.Flags().StringVar(&statusProject, "project", "", "Filter by project name")
	_ = statusCmd.RegisterFlagCompletionFunc("project", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		reg := registry.New("")
		seen := make(map[string]bool)
		var projects []string
		for _, a := range reg.Allocations() {
			if p, ok := a["project"].(string); ok && !seen[p] {
				seen[p] = true
				projects = append(projects, p)
			}
		}
		return projects, cobra.ShellCompDirectiveNoFileComp
	})
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().BoolVar(&statusCheck, "check", false, "Probe allocated ports to check if services are running")
	statusCmd.Flags().BoolVar(&statusWatch, "watch", false, "Auto-refresh status on a loop (implies --check)")
	statusCmd.Flags().IntVar(&statusInterval, "interval", 5, "Refresh interval in seconds (used with --watch)")
	rootCmd.AddCommand(statusCmd)
}

// Status is polled by agents and dashboards, so it must return in bounded time
// no matter what any single worktree is doing. Every per-worktree probe gets
// its own deadline, the whole command gets a budget, and a SIGTERM/SIGINT from
// a caller that gave up cancels the probes so their git children are killed
// rather than orphaned into the next poll.
const (
	statusBudget           = 10 * time.Second
	gitProbeTimeout        = 3 * time.Second
	supervisorProbeTimeout = 2 * time.Second
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all active allocations across projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if statusWatch {
			statusCheck = true
			return cliErr(cmd, runStatusWatch(ctx))
		}
		return cliErr(cmd, renderStatus(ctx))
	},
}

func runStatusWatch(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(statusInterval) * time.Second)
	defer ticker.Stop()

	for {
		fmt.Print("\033[H\033[2J") // clear terminal
		if err := renderStatus(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println()
				return nil
			}
			return err
		}
		fmt.Printf("\nRefreshing every %ds. Ctrl+C to exit.", statusInterval)

		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-ticker.C:
		}
	}
}

// probeAll runs probe over inputs concurrently and applies each result on the
// calling goroutine until ctx is done. Inputs must be plain values snapshotted
// from the allocation maps before the call — probe goroutines never see the
// maps, so a probe that outlives ctx cannot race the rendering that follows.
// Late results are dropped rather than applied, so a stalled probe can neither
// block the command nor corrupt its output.
func probeAll[K, T any](ctx context.Context, inputs []K, probe func(context.Context, K) T, apply func(int, T)) {
	type result struct {
		i     int
		value T
	}
	results := make(chan result, len(inputs))
	for i, in := range inputs {
		go func() { results <- result{i, probe(ctx, in)} }()
	}
	for range inputs {
		if ctx.Err() != nil {
			return
		}
		select {
		case r := <-results:
			apply(r.i, r.value)
		case <-ctx.Done():
			return
		}
	}
}

// worktreeProbe is everything status reads from a single worktree.
type worktreeProbe struct {
	branch          string
	repo            string
	supervisorState string
}

// probeWorktree gathers one worktree's state. The two git reads share a single
// deadline — a hung mount stalls both, so paying gitProbeTimeout twice buys
// nothing — keeping the worst case per worktree well inside the status budget.
// forJSON adds the repo slug and supervisor state that only JSON output uses.
func probeWorktree(ctx context.Context, wt string, forJSON bool) worktreeProbe {
	gitCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
	defer cancel()
	p := worktreeProbe{branch: worktree.CurrentBranchContext(gitCtx, wt)}
	if !forJSON {
		return p
	}
	p.repo = worktree.RepoSlugFromRemoteContext(gitCtx, wt)
	state, err := supervisor.SendWithTimeout(supervisor.SocketPath(wt), "status", supervisorProbeTimeout)
	if err != nil {
		state = "not running"
	}
	p.supervisorState = state
	return p
}

// withWorktree filters allocs to those with a non-empty worktree path.
func withWorktree(allocs []registry.Allocation) []registry.Allocation {
	var out []registry.Allocation
	for _, a := range allocs {
		if wt, _ := a["worktree"].(string); wt != "" {
			out = append(out, a)
		}
	}
	return out
}

// worktreePaths snapshots each allocation's worktree path, giving probe
// goroutines something safe to hold after their deadline has passed.
func worktreePaths(allocs []registry.Allocation) []string {
	paths := make([]string, len(allocs))
	for i, a := range allocs {
		paths[i], _ = a["worktree"].(string)
	}
	return paths
}

// filterByProject filters in memory rather than via reg.FindByProject, so the
// filtered view shares maps with the full set that JSON output probes.
func filterByProject(allocs []registry.Allocation, project string) []registry.Allocation {
	var out []registry.Allocation
	for _, a := range allocs {
		if p, _ := a["project"].(string); p == project {
			out = append(out, a)
		}
	}
	return out
}

func renderStatus(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, statusBudget)
	defer cancel()

	// A caller that SIGKILLs us gets no chance to clean up, and macOS has no
	// way to tie a child's lifetime to its parent, so orphaned git probes are
	// reaped by the next run instead. This keeps a repeatedly-killed poll from
	// accumulating them — the process storm this bounding exists to prevent.
	stateDir := platform.ConfigDir()
	if n := process.ReapStale(stateDir); n > 0 {
		fmt.Fprintf(os.Stderr, "note: reaped %d orphaned git process group(s) from a previous run\n", n)
	}
	tracker := process.NewTracker(stateDir)
	defer tracker.Close()
	ctx = process.WithTracker(ctx, tracker)

	reg := registry.New("")
	all := reg.Allocations()
	allocs := all
	if statusProject != "" {
		allocs = filterByProject(all, statusProject)
	}

	// JSON output probes the whole registry — its worktree index spans every
	// project so edge endpoints elsewhere resolve — while text output only
	// needs the allocations it displays. One fan-out covers branch, repo
	// slug, and supervisor state per worktree.
	probed := withWorktree(allocs)
	if statusJSON {
		probed = withWorktree(all)
	}
	paths := worktreePaths(probed)
	branchChanges := make(map[string]string)
	repoByPath := make(map[string]string)
	probeAll(ctx, paths,
		func(ctx context.Context, wt string) worktreeProbe {
			return probeWorktree(ctx, wt, statusJSON)
		},
		func(i int, p worktreeProbe) {
			a := probed[i]
			if old, _ := a["branch"].(string); p.branch != "" && p.branch != old {
				a["branch"] = p.branch
				branchChanges[paths[i]] = p.branch
			}
			if statusJSON {
				a["supervisor"] = p.supervisorState
				repoByPath[paths[i]] = p.repo
			}
		})

	if statusCheck || statusJSON {
		portSets := make([][]int, len(allocs))
		for i, a := range allocs {
			portSets[i] = format.GetPorts(format.Allocation(a))
		}
		probeAll(ctx, portSets,
			func(_ context.Context, ports []int) bool {
				return allocator.CheckPortsListening(ports)
			},
			func(i int, up bool) { allocs[i]["listening"] = up })
	}

	// A signal means the caller gave up; don't emit output they will never
	// read. An expired budget still renders whatever was gathered.
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}

	// One lock acquisition for every branch write-back, and none once the
	// budget is spent, so a contended registry costs at most one lock wait.
	if len(branchChanges) > 0 && ctx.Err() == nil {
		if err := reg.UpdateBranches(branchChanges); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update branches in registry: %v\n", err)
		}
	}

	if statusJSON {
		idx := assembleWorktreeIndex(probed, repoByPath)
		for _, a := range withWorktree(allocs) {
			wt := a["worktree"].(string)
			if ref, ok := idx.refByPath[wt]; ok {
				a["repo"] = ref.Repo
				a["related"] = buildRelated(reg, idx, wt, ref)
			} else {
				a["related"] = []relatedEntry{}
			}
		}
		data, err := json.MarshalIndent(allocs, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding status: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	if len(allocs) == 0 {
		fmt.Println("No active allocations.")
		return nil
	}

	grouped := make(map[string][]registry.Allocation)
	for _, a := range allocs {
		project := ""
		if p, ok := a["project"].(string); ok {
			project = p
		}
		grouped[project] = append(grouped[project], a)
	}

	for project, entries := range grouped {
		sort.Slice(entries, func(i, j int) bool {
			pi, _ := entries[i]["port"].(float64)
			pj, _ := entries[j]["port"].(float64)
			return pi < pj
		})

		fmt.Printf("\n%s:\n", project)
		for _, a := range entries {
			fa := format.Allocation(a)
			ports := format.GetPorts(fa)
			portLabel := format.JoinInts(ports, ",")

			name := format.DisplayName(fa)
			db := format.GetStr(fa, "database")

			redis := redisLabel(a)

			line := fmt.Sprintf("  :%s  %s", portLabel, name)
			if db != "" {
				line += fmt.Sprintf("  db:%s", db)
			}
			if redis != "" {
				line += fmt.Sprintf("  %s", redis)
			}

			if statusCheck {
				if listening, ok := a["listening"].(bool); ok && listening {
					line += "  [up]"
				} else {
					line += "  [down]"
				}
			}

			fmt.Println(line)
			if links, ok := a["links"].(map[string]any); ok && len(links) > 0 {
				for proj, branch := range links {
					if b, ok := branch.(string); ok {
						fmt.Printf("  → %s linked to %s\n", proj, b)
					}
				}
			}
		}
	}

	renderRedisCapacity(reg)
	return nil
}

// renderRedisCapacity surfaces Redis database usage for the "database"
// strategy, where slots are a finite resource (1..N-1). It reports how full
// the pool is and, crucially, flags any database shared by more than one
// worktree — the silent collision that predates fail-loud allocation and the
// symptom users otherwise can't see. Computed over the whole registry because
// the DB pool is global, not per-project.
func renderRedisCapacity(reg *registry.Registry) {
	uc := config.LoadUserConfig("")
	if uc.RedisStrategy() != "database" {
		return
	}

	byDB := make(map[int][]string)
	for _, a := range reg.Allocations() {
		rdb, ok := a["redis_db"].(float64)
		if !ok || int(rdb) <= 0 {
			continue
		}
		byDB[int(rdb)] = append(byDB[int(rdb)], format.DisplayName(format.Allocation(a)))
	}
	if len(byDB) == 0 {
		return
	}

	usable := uc.RedisDatabases() - 1
	fmt.Printf("\nRedis (database strategy): %d/%d slots used on %s\n", len(byDB), usable, uc.RedisURL())

	var shared []int
	for db, names := range byDB {
		if len(names) > 1 {
			shared = append(shared, db)
		}
	}
	if len(shared) == 0 {
		return
	}
	sort.Ints(shared)
	fmt.Println("  ⚠ collisions — these worktrees share a Redis DB; background jobs will cross-contaminate:")
	for _, db := range shared {
		names := byDB[db]
		sort.Strings(names)
		fmt.Printf("      db%d: %s\n", db, strings.Join(names, ", "))
	}
	fmt.Println("  Fix: raise `databases` in redis.conf + `gtl config set redis.databases <N>`,")
	fmt.Println("       or `gtl config set redis.strategy prefixed`, then `gtl reallocate --all-registry --apply`.")
}
