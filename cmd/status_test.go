package cmd

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/registry"
)

func TestProbeAll(t *testing.T) {
	tests := []struct {
		name    string
		delays  []time.Duration // per-allocation probe latency
		budget  time.Duration
		applied []string // worktrees whose result must be applied
		dropped []string // worktrees whose result must never be applied
	}{
		{
			name:    "all fast probes are applied",
			delays:  []time.Duration{0, 0, 0},
			budget:  time.Second,
			applied: []string{"wt0", "wt1", "wt2"},
		},
		{
			name:    "one stalled probe does not block the others",
			delays:  []time.Duration{0, time.Hour, 0},
			budget:  200 * time.Millisecond,
			applied: []string{"wt0", "wt2"},
			dropped: []string{"wt1"},
		},
		{
			name:   "no allocations returns immediately",
			delays: nil,
			budget: time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var allocs []registry.Allocation
			for i := range tt.delays {
				allocs = append(allocs, registry.Allocation{"worktree": "wt" + strconv.Itoa(i)})
			}
			ctx, cancel := context.WithTimeout(context.Background(), tt.budget)
			defer cancel()

			start := time.Now()
			probeAll(ctx, allocs,
				func(ctx context.Context, a registry.Allocation) bool {
					i, _ := strconv.Atoi(strings.TrimPrefix(a["worktree"].(string), "wt"))
					select {
					case <-time.After(tt.delays[i]):
						return true
					case <-ctx.Done():
						return false
					}
				},
				func(a registry.Allocation, ok bool) { a["probed"] = ok })
			elapsed := time.Since(start)

			if elapsed > tt.budget+500*time.Millisecond {
				t.Fatalf("probeAll took %v, budget was %v", elapsed, tt.budget)
			}
			for _, a := range allocs {
				wt := a["worktree"].(string)
				_, got := a["probed"]
				switch {
				case contains(tt.applied, wt) && !got:
					t.Errorf("%s: result not applied", wt)
				case contains(tt.dropped, wt) && got:
					t.Errorf("%s: stalled result applied after deadline", wt)
				}
			}
		})
	}
}
