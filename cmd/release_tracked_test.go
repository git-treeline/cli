package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
)

func trackedReleaseFixture(t *testing.T) (*registry.Registry, string) {
	t.Helper()
	commandLifecycleEnvironment(t)
	acquireCommandLifecycleLocks = registry.AcquireLifecycleLocks
	oldFactory := trackedReleaseExecutorFactory
	t.Cleanup(func() { trackedReleaseExecutorFactory = oldFactory })
	root := commandLifecycleRoot(t)
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, root)
	return reg, root
}

func fakeTrackedReleaseFactory(t *testing.T, run func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult) releaseExecutorFactory {
	t.Helper()
	return func(reg *registry.Registry) (*releaseExecutor, error) {
		return &releaseExecutor{Registry: reg,
			Acquire: func(context.Context, []string) (*registry.LifecycleLocks, error) {
				t.Fatal("borrowed lifecycle locks reacquired")
				return nil, nil
			},
			Preflight: func(context.Context, registry.ReleaseOperation) registry.PhaseResult {
				return registry.PhaseResult{State: "completed", Code: "ok"}
			},
			RunPhase: run,
		}, nil
	}
}

func TestTrackedReleaseSingleUsesCheckedPlanAndNeverLegacyTail(t *testing.T) {
	reg, root := trackedReleaseFixture(t)
	releaseDropDB = true
	var phases []registry.ReleasePhase
	var retained registry.ReleaseOperation
	trackedReleaseExecutorFactory = fakeTrackedReleaseFactory(t, func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		phases = append(phases, phase)
		retained = op
		return registry.PhaseResult{State: "completed", Code: "ok"}
	})
	if err := runReleaseSingleContext(context.Background(), []string{root}); err != nil {
		t.Fatal(err)
	}
	expected := []registry.ReleasePhase{registry.PhasePreHook, registry.PhaseSupervisor, registry.PhaseDatabases, registry.PhaseHosts, registry.PhasePostHook}
	if !reflect.DeepEqual(phases, expected) {
		t.Fatalf("wrong checked phase plan %v", phases)
	}
	plan, err := loadReleaseHookPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	if retained.HooksDigest != plan.digest() || retained.DatabaseConfigRevision != commandDatabaseRevision(t, root) || retained.RuntimeBinding == nil || retained.RuntimeBinding.Kind != "never_started" || !retained.Policy.IgnoreHookFailure || retained.Policy.ForceDirty {
		t.Fatalf("wrong retained intent: %+v", retained.ReleaseRequest)
	}
	if allocation, err := reg.FindForRelease(root); err != nil || allocation != nil {
		t.Fatal("allocation not retired through checked journal", err)
	}
	if _, err := os.Stat(filepath.Join(root, "release-hook-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy hook ran")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("non-removal release removed worktree", err)
	}
}

func TestTrackedReleaseDryRunNeverConstructsExecutor(t *testing.T) {
	reg, root := trackedReleaseFixture(t)
	releaseDryRun = true
	trackedReleaseExecutorFactory = func(*registry.Registry) (*releaseExecutor, error) {
		t.Fatal("dry run constructed executor")
		return nil, nil
	}
	before, _ := os.ReadFile(reg.Path)
	if err := runReleaseSingleContext(context.Background(), []string{root}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(reg.Path)
	if string(before) != string(after) {
		t.Fatal("dry run changed retained state")
	}
}

func TestTrackedReleaseBatchCountsCheckedSuccessesWithoutLegacyEffects(t *testing.T) {
	reg, first := trackedReleaseFixture(t)
	second := commandLifecycleRoot(t)
	commandLifecycleAllocate(t, reg, second)
	allocs, err := reg.ReleasePreflight()
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string][]registry.ReleasePhase{}
	trackedReleaseExecutorFactory = fakeTrackedReleaseFactory(t, func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		calls[op.Target.Path] = append(calls[op.Target.Path], phase)
		if op.DatabaseConfigRevision != "" || op.Policy.DropDatabases {
			t.Fatal("batch captured unrequested database cleanup")
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	})
	count, err := releaseAndTeardown(reg, allocs, false, false, true)
	if err != nil || count != 2 || len(calls[first]) != 4 || len(calls[second]) != 4 {
		t.Fatalf("count=%d err=%v calls=%v", count, err, calls)
	}
	for _, root := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(root, "release-hook-ran")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("legacy hook ran for tracked allocation")
		}
	}
	records, err := reg.ListReleases()
	if err != nil || len(records) != 2 || records[0].Outcome() != "completed" || records[1].Outcome() != "completed" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestTrackedReleaseFailureRetainsIdentityAndResumeHint(t *testing.T) {
	reg, root := trackedReleaseFixture(t)
	trackedReleaseExecutorFactory = fakeTrackedReleaseFactory(t, func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
		return registry.PhaseResult{State: "blocked", Code: "config_changed"}
	})
	err := runReleaseSingleContext(context.Background(), []string{root})
	var refusal *CliError
	if !errors.As(err, &refusal) {
		t.Fatalf("missing structured failure: %v", err)
	}
	records, readErr := reg.ListReleases()
	if readErr != nil || len(records) != 1 || records[0].Outcome() != "blocked" {
		t.Fatalf("records=%+v err=%v", records, readErr)
	}
	id := records[0].OperationID
	if !strings.Contains(refusal.Message, id) || refusal.Hint != "Resume with: gtl release resume "+id {
		t.Fatalf("missing retained operation recovery: %+v", refusal)
	}
	if allocation, err := reg.FindForRelease(root); err != nil || allocation == nil {
		t.Fatal("blocked release retired allocation", err)
	}
	if _, err := os.Stat(filepath.Join(root, "release-hook-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("blocked checked release fell through to legacy hook")
	}
}

func TestTrackedReleaseMixedPartitionAndSupervisorBinding(t *testing.T) {
	reg, root := trackedReleaseFixture(t)
	legacyRoot := commandLifecycleRoot(t)
	if err := reg.Allocate(registry.Allocation{"worktree": legacyRoot, "project": "legacy"}); err != nil {
		t.Fatal(err)
	}
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root, legacyRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := locks.Close(); err != nil {
			t.Error(err)
		}
	}()
	target, err := registry.CaptureReleaseTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := reg.FindForRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	generation := uuid.NewString()
	if _, err := reg.RegisterRuntime(locks, target, registry.GetString(allocation, "allocation_generation"), registry.RuntimeBinding{Kind: "never_started"}, generation); err != nil {
		t.Fatal(err)
	}
	allocs, err := reg.ReleasePreflight()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	trackedReleaseExecutorFactory = fakeTrackedReleaseFactory(t, func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		calls++
		if op.Target.Path != root || op.RuntimeBinding == nil || op.RuntimeBinding.Kind != "supervisor" || op.RuntimeBinding.Generation != generation {
			t.Fatal("supervisor binding not retained")
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	})
	legacy, count, err := releaseTrackedAllocations(context.Background(), reg, locks, allocs, false, false, false)
	if err != nil || count != 1 || len(legacy) != 1 || registry.GetString(legacy[0], "worktree") != legacyRoot || calls != 4 {
		t.Fatalf("legacy=%+v count=%d err=%v calls=%d", legacy, count, err, calls)
	}
	if !slices.Contains(locks.Paths(), root) || !slices.Contains(locks.Paths(), legacyRoot) {
		t.Fatal("executor closed borrowed batch locks")
	}
	if allocation, err := reg.FindForRelease(legacyRoot); err != nil || allocation == nil {
		t.Fatal("partition executed legacy effects", err)
	}
}

func TestTrackedReleaseBatchFailureReturnsCompletedCount(t *testing.T) {
	reg, first := trackedReleaseFixture(t)
	second := commandLifecycleRoot(t)
	commandLifecycleAllocate(t, reg, second)
	allocs, err := reg.ReleasePreflight()
	if err != nil {
		t.Fatal(err)
	}
	trackedReleaseExecutorFactory = fakeTrackedReleaseFactory(t, func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		if op.Target.Path == second {
			return registry.PhaseResult{State: "uncertain", Code: "unknown"}
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	})
	count, err := releaseAndTeardown(reg, allocs, false, false, true)
	if err == nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if allocation, _ := reg.FindForRelease(first); allocation != nil {
		t.Fatal("first successful release not retired")
	}
	if allocation, _ := reg.FindForRelease(second); allocation == nil {
		t.Fatal("uncertain second release retired")
	}
	if _, err := os.Stat(filepath.Join(second, "release-hook-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("uncertain checked release fell through")
	}
}

func TestTrackedReleaseFactoryFailureDoesNotExposeDiagnostics(t *testing.T) {
	reg, root := trackedReleaseFixture(t)
	trackedReleaseExecutorFactory = func(*registry.Registry) (*releaseExecutor, error) {
		return nil, errors.New("secret factory diagnostics")
	}
	err := runReleaseSingleContext(context.Background(), []string{root})
	if err == nil || err.Error() != "executor_unavailable" {
		t.Fatalf("unbounded factory refusal: %v", err)
	}
	records, readErr := reg.ListReleases()
	if readErr != nil || len(records) != 0 {
		t.Fatal("failed factory prepared a release", readErr)
	}
}

func TestTrackedReleaseCapturesForcedRemovalWithoutRemovingFixture(t *testing.T) {
	_, root := releaseSafetyFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("project: fixture\nhooks: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, root)
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := locks.Close(); err != nil {
			t.Error(err)
		}
	}()
	oldFactory := trackedReleaseExecutorFactory
	t.Cleanup(func() { trackedReleaseExecutorFactory = oldFactory })
	trackedReleaseExecutorFactory = fakeTrackedReleaseFactory(t, func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
		return registry.PhaseResult{State: "blocked", Code: "config_changed"}
	})
	allocation, err := reg.FindForRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := releaseTrackedAllocation(context.Background(), reg, locks, allocation, false, true, true)
	if !handled || err == nil {
		t.Fatal("fixture did not stop at injected gate")
	}
	records, err := reg.ListReleases()
	if err != nil || len(records) != 1 {
		t.Fatal("missing retained removal intent", err)
	}
	expected := registry.ReleasePolicy{RemoveWorktree: true, ForceDirty: true, IgnoreHookFailure: true}
	if records[0].Policy != expected || records[0].Target.Common == nil {
		t.Fatalf("removal intent=%+v", records[0].ReleaseRequest)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("blocked request removed fixture", err)
	}
}
