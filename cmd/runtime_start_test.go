package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/google/uuid"
)

func TestRuntimeStartRegistersBeforeLaunchAndDoesNotReplayInterruptedHandoff(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	dir, err := os.MkdirTemp("/tmp", "gtl-runtime-start-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store := runtimeowner.New(filepath.Join(dir, "outcomes.json"))
	ctx := context.Background()
	locks, err := registry.AcquireLifecycleLocks(ctx, []string{intent.Target.Path})
	if err != nil {
		t.Fatal(err)
	}
	defer locks.Close()
	h, err := prepareRuntimeStart(ctx, e.Registry, locks, store, intent.Target, nil, 12345, intent.DatabaseConfigRevision)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := e.Registry.FindForRelease(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := registry.AllocationRuntimeBinding(allocation)
	if err != nil || binding.Generation != h.Binding.Generation {
		t.Fatal(binding, err)
	}
	if record, err := store.Query(ctx, h.Binding); err != nil || record.State != runtimeowner.Prepared {
		t.Fatal(record, err)
	}
	if _, err := prepareRuntimeStart(ctx, e.Registry, locks, store, intent.Target, nil, 12345, intent.DatabaseConfigRevision); err == nil {
		t.Fatal("replayed interrupted launch")
	}
	owner := runtimeowner.Owner{BootID: uuid.NewString(), PID: 42, BirthID: "100:1"}
	if _, err := store.RegisterOwner(ctx, h.Binding, owner); err != nil {
		t.Fatal(err)
	}
	operation := uuid.NewString()
	if _, _, err := store.BeginStop(ctx, h.Binding, owner, operation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Complete(ctx, h.Binding, owner, operation); err != nil {
		t.Fatal(err)
	}
	next, err := prepareRuntimeStart(ctx, e.Registry, locks, store, intent.Target, nil, 12345, intent.DatabaseConfigRevision)
	if err != nil || next.Binding.Generation == h.Binding.Generation {
		t.Fatal(next, err)
	}
}

func TestRuntimeStartRefusesChangedConfigAndUnknownLegacyOwnership(t *testing.T) {
	for _, kind := range []string{"changed config", "legacy", "missing lock", "lost receipt"} {
		t.Run(kind, func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			dir, err := os.MkdirTemp("/tmp", "gtl-runtime-start-refusal-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			store := runtimeowner.New(filepath.Join(dir, "outcomes.json"))
			ctx := context.Background()
			locks, err := registry.AcquireLifecycleLocks(ctx, []string{intent.Target.Path})
			if err != nil {
				t.Fatal(err)
			}
			defer locks.Close()
			switch kind {
			case "changed config":
				if err := os.WriteFile(filepath.Join(intent.Target.Path, ".treeline.yml"), []byte("project: changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				if _, err := e.Registry.Release(intent.Target.Path); err != nil {
					t.Fatal(err)
				}
				if err := e.Registry.Allocate(registry.Allocation{"worktree": intent.Target.Path}); err != nil {
					t.Fatal(err)
				}
			case "missing lock":
				_ = locks.Close()
			case "lost receipt":
				if _, err := e.Registry.RegisterRuntime(locks, intent.Target, intent.Allocation.Generation, *intent.RuntimeBinding, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := prepareRuntimeStart(ctx, e.Registry, locks, store, intent.Target, nil, 12345, intent.DatabaseConfigRevision); err == nil {
				t.Fatal("uncertain start accepted")
			}
		})
	}
}
