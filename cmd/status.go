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

// probeAll runs probe for every allocation concurrently and applies each
// result on the calling goroutine until ctx is done. Probes that outlive ctx
// are abandoned: their results are dropped rather than written late, so a
// stalled worktree can neither block the command nor race its output.
func probeAll[T any](ctx context.Context, allocs []registry.Allocation, probe func(context.Context, registry.Allocation) T, apply func(registry.Allocation, T)) {
	type result struct {
		alloc registry.Allocation
		value T
	}
	results := make(chan result, len(allocs))
	for _, a := range allocs {
		go func() { results <- result{a, probe(ctx, a)} }()
	}
	for range allocs {
		select {
		case r := <-results:
			apply(r.alloc, r.value)
		case <-ctx.Done():
			return
		}
	}
}

func syncBranches(ctx context.Context, reg *registry.Registry, allocs []registry.Allocation) {
	probeAll(ctx, withWorktree(allocs),
		func(ctx context.Context, a registry.Allocation) string {
			ctx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
			defer cancel()
			return worktree.CurrentBranchContext(ctx, a["worktree"].(string))
		},
		func(a registry.Allocation, branch string) {
			old, _ := a["branch"].(string)
			if branch == "" || branch == old {
				return
			}
			a["branch"] = branch
			wt := a["worktree"].(string)
			if err := reg.UpdateField(wt, "branch", branch); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not update branch in registry for %s: %v\n", wt, err)
			}
		})
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

func renderStatus(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, statusBudget)
	defer cancel()

	reg := registry.New("")
	allocs := reg.Allocations()
	if statusProject != "" {
		allocs = reg.FindByProject(statusProject)
	}

	syncBranches(ctx, reg, allocs)

	if statusCheck || statusJSON {
		for _, a := range allocs {
			ports := format.GetPorts(format.Allocation(a))
			a["listening"] = allocator.CheckPortsListening(ports)
		}
	}

	if statusJSON {
		probeAll(ctx, withWorktree(allocs),
			func(_ context.Context, a registry.Allocation) string {
				sockPath := supervisor.SocketPath(a["worktree"].(string))
				resp, err := supervisor.SendWithTimeout(sockPath, "status", supervisorProbeTimeout)
				if err != nil {
					return "not running"
				}
				return resp
			},
			func(a registry.Allocation, state string) { a["supervisor"] = state })
	}

	// A signal means the caller gave up; don't emit output they will never
	// read. An expired budget still renders whatever was gathered.
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}

	if statusJSON {
		// Index spans the whole registry, not just the (possibly project-
		// filtered) output set, so edge endpoints in other projects resolve.
		idx := buildWorktreeIndex(ctx, reg.Allocations())
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
