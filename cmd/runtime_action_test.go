package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
)

func TestTrackedRuntimeActionsUseExactOwnerWithoutFallback(t *testing.T) {
	for _, kind := range []string{"stop", "restart", "shutdown", "lost reply", "owner changed", "wrong status", "reservation", "ack without receipt"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			e, intent, a, binding, owner := runtimeReleaseFixture(t)
			acquireCommandLifecycleLocks = registry.AcquireLifecycleLocks
			action := kind
			if kind != "stop" && kind != "restart" {
				action = "shutdown"
			}
			if kind == "reservation" {
				if _, _, err := e.Registry.PrepareRelease(intent); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			effects := runtimeActionEffects{
				store: func(string) *runtimeowner.Store { return a.Outcomes },
				verify: func(actual runtimeowner.Owner) bool {
					if actual != owner {
						t.Fatal("wrong owner")
					}
					return kind != "owner changed"
				},
				status: func(_, generation string) (string, error) {
					if generation != binding.Generation {
						t.Fatal("wrong generation")
					}
					if kind == "wrong status" {
						return "owned", errors.New("replaced")
					}
					return "owned", nil
				},
				command: func(_, got string) error {
					calls++
					if got != action {
						t.Fatal(got)
					}
					return nil
				},
				shutdown: func(_, generation, operation string, _ time.Duration) error {
					calls++
					if generation != binding.Generation {
						t.Fatal("shutdown wrong generation")
					}
					if kind == "ack without receipt" {
						return nil
					}
					if _, claimed, err := a.Outcomes.BeginStop(ctx, binding, owner, operation); err != nil || !claimed {
						t.Fatal(err)
					}
					if _, err := a.Outcomes.Complete(ctx, binding, owner, operation); err != nil {
						t.Fatal(err)
					}
					if kind == "lost reply" {
						return errors.New("lost reply")
					}
					return nil
				},
			}
			handled, err := runTrackedRuntimeActionWith(ctx, intent.Target.Path, action, effects)
			if !handled {
				t.Fatal("tracked action used legacy fallback")
			}
			switch kind {
			case "owner changed", "wrong status", "reservation":
				if err == nil || calls != 0 {
					t.Fatal(err, calls)
				}
			case "ack without receipt":
				if err == nil || calls != 1 {
					t.Fatal(err, calls)
				}
			default:
				if err != nil || calls != 1 {
					t.Fatal(err, calls)
				}
			}
			if kind == "shutdown" || kind == "lost reply" {
				_, err := runTrackedRuntimeActionWith(ctx, intent.Target.Path, "shutdown", effects)
				if err != nil || calls != 1 {
					t.Fatal("completed shutdown repeated", err, calls)
				}
			}
		})
	}
}
