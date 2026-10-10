package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/google/uuid"
)

func runtimeReleaseFixture(t *testing.T) (*releaseExecutor, registry.ReleaseRequest, releaseRuntimeAdapter, runtimeowner.Binding, runtimeowner.Owner) {
	t.Helper()
	e, intent := releaseExecutorFixture(t)
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{intent.Target.Path})
	if err != nil {
		t.Fatal(err)
	}
	generation := uuid.NewString()
	_, err = e.Registry.RegisterRuntime(locks, intent.Target, intent.Allocation.Generation, *intent.RuntimeBinding, generation)
	_ = locks.Close()
	if err != nil {
		t.Fatal(err)
	}
	intent.RuntimeBinding = &registry.RuntimeBinding{Kind: "supervisor", Generation: generation}
	dir, err := os.MkdirTemp("/tmp", "gtl-runtime-release-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store := runtimeowner.New(filepath.Join(dir, "outcomes.json"))
	binding := runtimeowner.Binding{Target: intent.Target, Allocation: intent.Allocation, Generation: generation, ConfigDigest: intent.DatabaseConfigRevision, PostStopDigest: intent.HooksDigest}
	owner := runtimeowner.Owner{BootID: uuid.NewString(), PID: 42, BirthID: "123:456"}
	if _, err := store.Prepare(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterOwner(context.Background(), binding, owner); err != nil {
		t.Fatal(err)
	}
	a := releaseRuntimeAdapter{Registry: e.Registry, Outcomes: store, Probe: func(context.Context, runtimeowner.Owner) bool { return true }}
	e.RecoverSupervisor = a.Recover
	return e, intent, a, binding, owner
}

func TestRuntimeReleaseRecoversLostReplyWithoutRepeatingShutdown(t *testing.T) {
	for _, kind := range []string{"lost socket reply", "lost release receipt"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			e, intent, a, binding, owner := runtimeReleaseFixture(t)
			calls := 0
			a.Shutdown = func(_ context.Context, generation, operation string) error {
				calls++
				if generation != binding.Generation || operation != intent.OperationID {
					t.Fatal("wrong shutdown identity")
				}
				if _, claimed, err := a.Outcomes.BeginStop(ctx, binding, owner, operation); err != nil || !claimed {
					t.Fatal("stop not claimed", err)
				}
				if kind == "lost socket reply" {
					if _, err := a.Outcomes.Complete(ctx, binding, owner, operation); err != nil {
						t.Fatal(err)
					}
				}
				return errors.New("lost reply")
			}
			e.RunPhase = func(ctx context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
				if phase == registry.PhaseSupervisor {
					return a.Run(ctx, op)
				}
				return registry.PhaseResult{State: "completed", Code: "ok"}
			}
			op, err := e.Start(ctx, intent)
			if kind == "lost socket reply" {
				if err != nil || op.Outcome() != "completed" {
					t.Fatal(op, err)
				}
			} else {
				if err == nil || op.Outcome() != "uncertain" {
					t.Fatal(op, err)
				}
				if _, err := a.Outcomes.Complete(ctx, binding, owner, intent.OperationID); err != nil {
					t.Fatal(err)
				}
				a.Outcomes = runtimeowner.New(a.Outcomes.Path)
				e.RecoverSupervisor = a.Recover
			}
			a.Shutdown = func(context.Context, string, string) error { t.Fatal("replayed shutdown"); return nil }
			e.Registry = registry.New(e.Registry.Path)
			recovered, err := e.Resume(ctx, intent)
			if err != nil || recovered.Outcome() != "completed" || calls != 1 {
				t.Fatal(recovered, err, calls)
			}
		})
	}
}

func TestRuntimeReleaseRefusesMissingOrReplacedOwnerAndUncertainCompletion(t *testing.T) {
	for _, kind := range []string{"missing", "probe mismatch", "prepared", "wrong generation", "wrong allocation", "stopping", "uncertain", "ack without receipt"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			e, intent, a, binding, owner := runtimeReleaseFixture(t)
			switch kind {
			case "missing":
				a.Outcomes = runtimeowner.New(filepath.Join(filepath.Dir(a.Outcomes.Path), "absent.json"))
			case "probe mismatch":
				a.Probe = func(context.Context, runtimeowner.Owner) bool { return false }
			case "prepared":
				other := runtimeowner.New(filepath.Join(filepath.Dir(a.Outcomes.Path), "prepared.json"))
				if _, err := other.Prepare(ctx, binding); err != nil {
					t.Fatal(err)
				}
				a.Outcomes = other
			case "wrong generation":
				a.Outcomes = runtimeowner.New(filepath.Join(filepath.Dir(a.Outcomes.Path), "wrong.json"))
				binding.Generation = uuid.NewString()
				if _, err := a.Outcomes.Prepare(ctx, binding); err != nil {
					t.Fatal(err)
				}
			case "wrong allocation":
				a.Outcomes = runtimeowner.New(filepath.Join(filepath.Dir(a.Outcomes.Path), "wrong.json"))
				binding.Allocation.Generation = uuid.NewString()
				if _, err := a.Outcomes.Prepare(ctx, binding); err != nil {
					t.Fatal(err)
				}
			case "stopping", "uncertain":
				if _, _, err := a.Outcomes.BeginStop(ctx, binding, owner, intent.OperationID); err != nil {
					t.Fatal(err)
				}
				if kind == "uncertain" {
					if _, err := a.Outcomes.MarkUncertain(ctx, binding, &owner, intent.OperationID, "stop_interrupted"); err != nil {
						t.Fatal(err)
					}
				}
			}
			calls := 0
			a.Shutdown = func(context.Context, string, string) error { calls++; return nil }
			e.RecoverSupervisor = a.Recover
			e.RunPhase = func(ctx context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
				if phase == registry.PhaseSupervisor {
					return a.Run(ctx, op)
				}
				return registry.PhaseResult{State: "completed", Code: "ok"}
			}
			op, err := e.Start(ctx, intent)
			if err == nil || op == nil || op.Outcome() == "completed" {
				t.Fatal(op, err)
			}
			if kind == "ack without receipt" {
				if calls != 1 || op.Outcome() != "uncertain" {
					t.Fatal(calls, op)
				}
			} else if calls != 0 {
				t.Fatal("unowned shutdown", calls)
			}
			if a.Recover(ctx, *op) {
				t.Fatal("invented completion")
			}
		})
	}
}
