package cmd

import (
	"context"
	"fmt"
	"slices"

	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
)

var trackedReleaseExecutorFactory releaseExecutorFactory = productionReleaseExecutor

// releaseTrackedAllocation consumes only explicit tracked ownership. Unknown
// legacy ownership remains with the compatibility release path.
func releaseTrackedAllocation(ctx context.Context, reg *registry.Registry, locks *registry.LifecycleLocks, allocation registry.Allocation, dropDB, removeWT, force bool) (bool, error) {
	binding, err := registry.AllocationRuntimeBinding(allocation)
	if err != nil {
		return true, releaseRefusal("runtime_binding_unavailable")
	}
	if binding.Kind == "unknown" {
		return false, nil
	}
	path := registry.GetString(allocation, "worktree")
	if locks == nil || !slices.Contains(locks.Paths(), path) {
		return true, releaseRefusal("execution_lock_unavailable")
	}
	target, err := registry.CaptureReleaseTarget(path)
	if err != nil {
		return true, releaseRefusal("identity_changed")
	}
	plan, err := loadReleaseHookPlan(path)
	if err != nil {
		return true, releaseRefusal("config_unavailable")
	}
	intent := registry.ReleaseRequest{
		OperationID: uuid.NewString(), Target: target,
		Allocation:     registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(allocation, "allocation_generation")},
		RuntimeBinding: &binding, HooksDigest: plan.digest(),
		Policy: registry.ReleasePolicy{DropDatabases: dropDB, RemoveWorktree: removeWT, ForceDirty: force && removeWT, IgnoreHookFailure: force},
	}
	if dropDB {
		intent.DatabaseConfigRevision, err = format.DatabaseCleanupConfigRevision(path)
		if err != nil {
			return true, releaseRefusal("config_unavailable")
		}
	}
	if trackedReleaseExecutorFactory == nil {
		return true, releaseRefusal("executor_unconfigured")
	}
	executor, err := trackedReleaseExecutorFactory(reg)
	if err != nil {
		return true, releaseRefusal("executor_unavailable")
	}
	if executor == nil || executor.Registry == nil || executor.Registry.Path != reg.Path {
		return true, releaseRefusal("executor_unconfigured")
	}
	executor.HeldLocks = locks
	_, executionErr := executor.Start(ctx, intent)
	retained, readErr := reg.GetRelease(intent.OperationID)
	code := publicReleaseErrorCode(executionErr)
	if readErr != nil {
		code = "registry_unavailable"
	}
	if code == "" {
		if retained == nil {
			code = "receipt_unavailable"
		} else if retained.Outcome() != "completed" {
			code = retained.Outcome()
		}
	}
	if code == "" {
		return true, nil
	}
	if retained != nil {
		return true, &CliError{Message: fmt.Sprintf("Release operation %s stopped (%s).", intent.OperationID, code), Hint: "Resume with: gtl release resume " + intent.OperationID}
	}
	if readErr != nil {
		return true, &CliError{Message: fmt.Sprintf("Release operation %s could not be read (%s).", intent.OperationID, code), Hint: "Inspect with: gtl release status " + intent.OperationID}
	}
	return true, releaseRefusal(code)
}

func releaseTrackedAllocations(ctx context.Context, reg *registry.Registry, locks *registry.LifecycleLocks, allocations []registry.Allocation, dropDB, removeWT, force bool) ([]registry.Allocation, int, error) {
	var legacy []registry.Allocation
	completed := 0
	for _, allocation := range allocations {
		handled, err := releaseTrackedAllocation(ctx, reg, locks, allocation, dropDB, removeWT, force)
		if err != nil {
			return nil, completed, err
		}
		if handled {
			completed++
		} else {
			legacy = append(legacy, allocation)
		}
	}
	return legacy, completed, nil
}
