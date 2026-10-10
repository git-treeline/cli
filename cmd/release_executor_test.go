package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
)

func releaseExecutorFixture(t *testing.T) (*releaseExecutor, registry.ReleaseRequest) {
	t.Helper()
	commandLifecycleEnvironment(t)
	root := commandLifecycleRoot(t)
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, root)
	alloc, err := reg.FindForRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	intent := registry.ReleaseRequest{
		OperationID:    uuid.NewString(),
		Target:         registry.TargetIdentity{Path: root, Directory: commandDirectoryIdentity(t, root)},
		Allocation:     registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(alloc, "allocation_generation")},
		RuntimeBinding: &registry.RuntimeBinding{Kind: "never_started"}, DatabaseConfigRevision: commandDatabaseRevision(t, root), Policy: registry.ReleasePolicy{DropDatabases: true}, HooksDigest: strings.Repeat("0", 64),
	}
	executor := &releaseExecutor{
		Registry: reg,
		Acquire: func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
			if !reflect.DeepEqual(paths, []string{root}) {
				t.Fatalf("wrong targets: %q", paths)
			}
			return &registry.LifecycleLocks{}, nil
		},
		Preflight: func(context.Context, registry.ReleaseOperation) registry.PhaseResult {
			return registry.PhaseResult{State: "completed", Code: "ok"}
		},
		RunPhase: func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
			return registry.PhaseResult{State: "completed", Code: "ok"}
		},
	}
	return executor, intent
}

func requireReleaseExecutionCode(t *testing.T, err error, want string) {
	t.Helper()
	var refusal *releaseExecutionError
	if !errors.As(err, &refusal) || refusal.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestReleaseExecutorOrderingAndLostReply(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	var calls []registry.ReleasePhase
	e.RunPhase = func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		persisted, err := e.Registry.GetRelease(intent.OperationID)
		if err != nil || persisted == nil {
			t.Fatalf("dispatch without receipt: %v", err)
		}
		found := false
		for _, p := range persisted.Phases {
			if p.Name == phase {
				found = p.State == "started" && p.Attempt != ""
			}
		}
		if !found {
			t.Fatal("dispatch preceded persisted phase start")
		}
		if err := e.Registry.CheckReleaseReservations([]string{intent.Target.Path}); err == nil {
			t.Fatal("dispatch without reservation")
		}
		if !reflect.DeepEqual(op.ReleaseRequest, intent) {
			t.Fatal("intent changed")
		}
		calls = append(calls, phase)
		// Adapter mutations cannot rewrite the executor's immutable binding.
		op.Policy.DropDatabases = false
		op.Snapshot.Project = "mutated"
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	op, err := e.Start(context.Background(), intent)
	if err != nil || op.Outcome() != "completed" {
		t.Fatalf("start: %+v %v", op, err)
	}
	want := []registry.ReleasePhase{registry.PhasePreHook, registry.PhaseSupervisor, registry.PhaseDatabases, registry.PhaseHosts, registry.PhasePostHook}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("phase order: %v", calls)
	}
	if alloc, err := e.Registry.FindForRelease(intent.Target.Path); err != nil || alloc != nil {
		t.Fatalf("allocation not retired atomically: %v", err)
	}
	e.Registry = registry.New(e.Registry.Path)
	e.Preflight = func(context.Context, registry.ReleaseOperation) registry.PhaseResult {
		t.Fatal("completed receipt re-preflighted")
		return registry.PhaseResult{}
	}
	for _, resume := range []bool{false, true} {
		var recovered *registry.ReleaseOperation
		if resume {
			recovered, err = e.Resume(context.Background(), intent)
		} else {
			recovered, err = e.Start(context.Background(), intent)
		}
		if err != nil || !reflect.DeepEqual(op, recovered) {
			t.Fatalf("lost-reply recovery changed receipt: %v", err)
		}
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatal("lost reply caused repeated effects")
	}
}

func TestReleaseExecutorPreflightBlocksBeforeAnyEffects(t *testing.T) {
	for _, reason := range []string{"post-hook recovery", "runtime quiescence", "unknown prerequisite", "invalid result"} {
		t.Run(reason, func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			e.Preflight = func(context.Context, registry.ReleaseOperation) registry.PhaseResult {
				switch reason {
				case "post-hook recovery":
					return registry.PhaseResult{State: "blocked", Code: "config_changed"}
				case "runtime quiescence":
					return registry.PhaseResult{State: "blocked", Code: "failed"}
				case "unknown prerequisite":
					return registry.PhaseResult{State: "uncertain", Code: "unknown"}
				default:
					return registry.PhaseResult{}
				}
			}
			e.RunPhase = func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
				t.Fatal("preflight refusal dispatched effect")
				return registry.PhaseResult{}
			}
			op, err := e.Start(context.Background(), intent)
			want := "preflight_blocked"
			if reason == "unknown prerequisite" || reason == "invalid result" {
				want = "preflight_unavailable"
			}
			requireReleaseExecutionCode(t, err, want)
			if op == nil || op.Outcome() != "blocked" || op.Phases[0].State != "blocked" {
				t.Fatalf("missing blocked gate receipt: %+v", op)
			}
			for _, p := range op.Phases[1:] {
				if p.State != "pending" {
					t.Fatal("gate implied phase success")
				}
			}
			if alloc, err := e.Registry.FindForRelease(intent.Target.Path); err != nil || alloc == nil {
				t.Fatal("preflight retired allocation", err)
			}
		})
	}
}

func TestReleaseExecutorResumeBlockedAndNeverReplayUnknown(t *testing.T) {
	for _, outcome := range []string{"blocked", "uncertain", "malformed", "started"} {
		t.Run(outcome, func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			calls := 0
			e.RunPhase = func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
				calls++
				switch outcome {
				case "blocked":
					return registry.PhaseResult{State: "blocked", Code: "failed"}
				case "uncertain":
					return registry.PhaseResult{State: "uncertain", Code: "unknown"}
				default:
					return registry.PhaseResult{State: "completed", Code: "not-a-receipt"}
				}
			}
			if outcome == "started" {
				op, _, err := e.Registry.PrepareRelease(intent)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.Registry.BeginReleasePhase(intent.OperationID, registry.PhasePreHook, op.Revision); err != nil {
					t.Fatal(err)
				}
			} else {
				op, err := e.Start(context.Background(), intent)
				want := "uncertain"
				if outcome == "blocked" {
					want = "blocked"
				}
				requireReleaseExecutionCode(t, err, want)
				if op == nil || op.Outcome() != want || calls != 1 {
					t.Fatalf("wrong first outcome: %+v calls %d", op, calls)
				}
			}
			e.Registry = registry.New(e.Registry.Path)
			beforeCalls := calls
			e.RunPhase = func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
				calls++
				return registry.PhaseResult{State: "completed", Code: "ok"}
			}
			if outcome != "blocked" {
				e.Preflight = func(context.Context, registry.ReleaseOperation) registry.PhaseResult {
					t.Fatal("unknown operation re-preflighted")
					return registry.PhaseResult{}
				}
			}
			op, err := e.Resume(context.Background(), intent)
			if outcome == "blocked" {
				if err != nil || op.Outcome() != "completed" || calls <= beforeCalls {
					t.Fatalf("known failure did not resume: %v", err)
				}
			} else {
				requireReleaseExecutionCode(t, err, "uncertain")
				if calls != beforeCalls {
					t.Fatal("unknown hook replayed")
				}
			}
		})
	}
}

func TestReleaseExecutorBindingReservationAndBeginRefusals(t *testing.T) {
	for _, reason := range []string{"policy", "generation", "other operation", "missing receipt", "changed identity", "busy", "corrupt"} {
		t.Run(reason, func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			if reason != "missing receipt" {
				if _, _, err := e.Registry.PrepareRelease(intent); err != nil {
					t.Fatal(err)
				}
			}
			want := "binding_conflict"
			switch reason {
			case "policy":
				intent.Policy.DropDatabases = false
			case "generation":
				intent.Allocation.Generation = uuid.NewString()
			case "other operation":
				intent.OperationID = uuid.NewString()
				want = "reservation_refused"
			case "missing receipt":
				want = "not_found"
			case "changed identity":
				original := intent.Target.Path
				if err := os.Rename(original, original+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(original, 0700); err != nil {
					t.Fatal(err)
				}
				want = "phase_refused"
			case "busy":
				e.Acquire = func(context.Context, []string) (*registry.LifecycleLocks, error) {
					return nil, errors.New("private diagnostic")
				}
				want = "execution_lock_unavailable"
			case "corrupt":
				if err := os.WriteFile(e.Registry.Path, []byte(`{broken`), 0600); err != nil {
					t.Fatal(err)
				}
				want = "registry_unavailable"
			}
			before, _ := os.ReadFile(e.Registry.Path)
			e.RunPhase = func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
				t.Fatal("refusal dispatched effect")
				return registry.PhaseResult{}
			}
			var err error
			if reason == "other operation" {
				_, err = e.Start(context.Background(), intent)
			} else {
				_, err = e.Resume(context.Background(), intent)
			}
			requireReleaseExecutionCode(t, err, want)
			after, _ := os.ReadFile(e.Registry.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal rewrote registry")
			}
		})
	}
}

func TestReleaseExecutorFailedReceiptNeverAdvances(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	calls := 0
	e.RunPhase = func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
		calls++
		// Simulate replacement after dispatch: the result cannot be committed
		// against the captured target, so the durable start must remain.
		if err := os.Rename(intent.Target.Path, intent.Target.Path+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(intent.Target.Path, 0700); err != nil {
			t.Fatal(err)
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	op, err := e.Start(context.Background(), intent)
	requireReleaseExecutionCode(t, err, "receipt_unavailable")
	if calls != 1 || op == nil || op.Phases[0].State != "started" {
		t.Fatalf("missing durable uncertainty: %+v", op)
	}
	_, err = e.Resume(context.Background(), intent)
	requireReleaseExecutionCode(t, err, "uncertain")
	if calls != 1 {
		t.Fatal("failed receipt replayed effect")
	}
}

func TestReleaseExecutorResumeSkipsCompletedPrefix(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []registry.ReleasePhase
	e.RunPhase = func(_ context.Context, _ registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		calls = append(calls, phase)
		cancel()
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	op, err := e.Start(ctx, intent)
	requireReleaseExecutionCode(t, err, "cancelled")
	if op.Phases[0].State != "completed" || op.Phases[1].State != "pending" {
		t.Fatalf("known result lost at cancellation: %+v", op)
	}
	e.Registry = registry.New(e.Registry.Path)
	e.RunPhase = func(_ context.Context, _ registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		calls = append(calls, phase)
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	op, err = e.Resume(context.Background(), intent)
	want := []registry.ReleasePhase{registry.PhasePreHook, registry.PhaseSupervisor, registry.PhaseDatabases, registry.PhaseHosts, registry.PhasePostHook}
	if err != nil || op.Outcome() != "completed" || !reflect.DeepEqual(calls, want) {
		t.Fatalf("completed prefix replayed or remaining phases skipped: %v %v", calls, err)
	}
}

func TestReleaseExecutorHookFailureRequiresImmutablePolicy(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run(map[bool]string{false: "not allowed", true: "allowed"}[ignore], func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			intent.Policy.IgnoreHookFailure = ignore
			e.RunPhase = func(_ context.Context, _ registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
				if phase == registry.PhasePreHook || phase == registry.PhasePostHook {
					return registry.PhaseResult{State: "completed", Code: "ignored_hook_failure"}
				}
				return registry.PhaseResult{State: "completed", Code: "ok"}
			}
			op, err := e.Start(context.Background(), intent)
			if ignore {
				if err != nil || op.Outcome() != "completed" {
					t.Fatalf("explicit hook policy refused: %v", err)
				}
			} else {
				requireReleaseExecutionCode(t, err, "uncertain")
				if op.Phases[1].State != "pending" {
					t.Fatal("invalid success advanced to destructive cleanup")
				}
			}
		})
	}
}

func TestReleaseExecutorUnallocatedGitOnly(t *testing.T) {
	main, linked := releaseSafetyFixture(t)
	reg := registry.New("")
	intent := registry.ReleaseRequest{OperationID: uuid.NewString(), Target: registry.TargetIdentity{
		Path: linked, Directory: commandDirectoryIdentity(t, linked), Common: &registry.CommonIdentity{Path: filepath.Join(main, ".git"), DirectoryIdentity: commandDirectoryIdentity(t, filepath.Join(main, ".git"))},
	}, Allocation: registry.AllocationRef{Kind: "unallocated"}, Policy: registry.ReleasePolicy{RemoveWorktree: true}, HooksDigest: strings.Repeat("0", 64)}
	calls := 0
	e := &releaseExecutor{Registry: reg,
		Acquire: func(context.Context, []string) (*registry.LifecycleLocks, error) {
			return &registry.LifecycleLocks{}, nil
		},
		Preflight: func(context.Context, registry.ReleaseOperation) registry.PhaseResult {
			return registry.PhaseResult{State: "completed", Code: "ok"}
		},
		RunPhase: func(_ context.Context, _ registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
			calls++
			if phase != registry.PhaseWorktree {
				t.Fatal("unallocated release ran managed cleanup")
			}
			if err := os.RemoveAll(linked); err != nil {
				t.Fatal(err)
			}
			return registry.PhaseResult{State: "completed", Code: "ok"}
		},
	}
	op, err := e.Start(context.Background(), intent)
	if err != nil || op.Outcome() != "completed" || calls != 1 {
		t.Fatalf("Git-only release: %+v %v", op, err)
	}
	if _, err := e.Resume(context.Background(), intent); err != nil || calls != 1 {
		t.Fatalf("missing path receipt recovery: %v", err)
	}
	intent.OperationID = uuid.NewString()
	if _, err := e.Start(context.Background(), intent); err == nil || calls != 1 {
		t.Fatal("missing path without receipt accepted")
	}
}

func commandDatabaseRevision(t *testing.T, root string) string {
	t.Helper()
	revision, err := format.DatabaseCleanupConfigRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
