package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
)

func removalPhaseFixture(t *testing.T) (string, string, registry.ReleaseOperation) {
	t.Helper()
	main, linked := releaseSafetyFixture(t)
	op := registry.ReleaseOperation{ReleaseRequest: registry.ReleaseRequest{
		Target: registry.TargetIdentity{Path: linked, Common: &registry.CommonIdentity{Path: filepath.Join(main, ".git")}},
		Policy: registry.ReleasePolicy{RemoveWorktree: true},
	}, Phases: []registry.PhaseRecord{{Name: registry.PhaseWorktree, State: "started", Attempt: "fixture-start"}}}
	return main, linked, op
}

func TestReleaseWorktreePhaseStrictRemoval(t *testing.T) {
	main, linked, op := removalPhaseFixture(t)
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, linked)
	allocation, err := reg.FindForRelease(linked)
	if err != nil {
		t.Fatal(err)
	}
	intent := op.ReleaseRequest
	intent.OperationID = uuid.NewString()
	intent.Target.Directory = commandDirectoryIdentity(t, linked)
	intent.Target.Common.DirectoryIdentity = commandDirectoryIdentity(t, filepath.Join(main, ".git"))
	intent.Allocation = registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(allocation, "allocation_generation")}
	intent.HooksDigest = strings.Repeat("0", 64)
	postHookSeen, removeCalls := false, 0
	executor := &releaseExecutor{Registry: reg,
		Acquire: func(context.Context, []string) (*registry.LifecycleLocks, error) {
			return &registry.LifecycleLocks{}, nil
		},
		Preflight: func(context.Context, registry.ReleaseOperation) registry.PhaseResult {
			return registry.PhaseResult{State: "completed", Code: "ok"}
		},
		RunPhase: func(ctx context.Context, current registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
			if phase == registry.PhaseWorktree {
				if !postHookSeen {
					t.Fatal("directory removed before post hook")
				}
				removeCalls++
				return releaseWorktreePhase(ctx, current)
			}
			if _, err := os.Stat(linked); err != nil {
				t.Fatal("phase lost working directory", err)
			}
			if phase == registry.PhasePostHook {
				postHookSeen = true
				if allocation, err := reg.FindForRelease(linked); err != nil || allocation != nil {
					t.Fatal("post hook preceded allocation retirement", err)
				}
			}
			return registry.PhaseResult{State: "completed", Code: "ok"}
		},
	}
	completed, err := executor.Start(context.Background(), intent)
	if err != nil || completed.Outcome() != "completed" || removeCalls != 1 {
		t.Fatalf("removal result: %+v %v", completed, err)
	}
	if _, err := os.Stat(linked); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target remains: %v", err)
	}
	if _, err := os.Stat(main); err != nil {
		t.Fatal("primary was affected", err)
	}
	executor.Registry = registry.New(reg.Path)
	if recovered, err := executor.Resume(context.Background(), intent); err != nil || recovered.Outcome() != "completed" || removeCalls != 1 {
		t.Fatalf("completed removal was replayed: %+v %v", recovered, err)
	}
	if again := releaseWorktreePhase(context.Background(), op); again.State != "blocked" {
		t.Fatal("missing target was invented as successful removal", again)
	}
}

func TestReleaseWorktreePhaseRefusesPrimaryAndUnstarted(t *testing.T) {
	for _, kind := range []string{"primary", "subdirectory", "not started", "no removal", "no common", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			main, linked, op := removalPhaseFixture(t)
			ctx := context.Background()
			switch kind {
			case "primary":
				op.Target.Path = main
			case "subdirectory":
				op.Target.Path = filepath.Join(linked, "child")
				if err := os.Mkdir(op.Target.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "not started":
				op.Phases[0].State = "pending"
			case "no removal":
				op.Policy.RemoveWorktree = false
			case "no common":
				op.Target.Common = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if result := releaseWorktreePhase(ctx, op); result.State != "blocked" {
				t.Fatalf("invalid removal proceeded: %+v", result)
			}
			for _, target := range []string{main, linked} {
				if _, err := os.Stat(target); err != nil {
					t.Fatal("refusal removed files", err)
				}
			}
		})
	}
}

func TestReleaseWorktreePhaseExplicitDirtyPolicy(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain dirty", true: "explicit force"}[force], func(t *testing.T) {
			_, linked, op := removalPhaseFixture(t)
			op.Policy.ForceDirty = force
			if err := os.WriteFile(filepath.Join(linked, "untracked"), []byte("fixture only"), 0600); err != nil {
				t.Fatal(err)
			}
			result := releaseWorktreePhase(context.Background(), op)
			if force {
				if result.State != "completed" {
					t.Fatalf("explicit force refused: %+v", result)
				}
			} else {
				if result.State != "uncertain" {
					t.Fatalf("failed Git dispatch invented success: %+v", result)
				}
				if _, err := os.Stat(filepath.Join(linked, "untracked")); err != nil {
					t.Fatal("dirty bytes lost", err)
				}
			}
		})
	}
}

func TestReleaseWorktreePhaseUnknownReply(t *testing.T) {
	_, linked, op := removalPhaseFixture(t)
	calls := 0
	result := releaseWorktreePhaseWith(context.Background(), op,
		func(target string) (string, error) { return filepath.Dir(target), nil },
		func(target string, force bool) error {
			calls++
			if target != linked || force {
				t.Fatal("wrong immutable policy")
			}
			return errors.New("private failure after dispatch")
		})
	if calls != 1 || result != (registry.PhaseResult{State: "uncertain", Code: "unknown"}) {
		t.Fatalf("unknown result: %+v calls %d", result, calls)
	}
}
