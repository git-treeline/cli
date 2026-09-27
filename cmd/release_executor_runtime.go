package cmd

import (
	"context"
	"reflect"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
)

type releaseRuntimeAdapter struct {
	Registry *registry.Registry
	Outcomes *runtimeowner.Store
	// Probe must verify kernel boot and process birth before dispatch; PID-only
	// socket/sidecar observations cannot authorize a stop.
	Probe    func(context.Context, runtimeowner.Owner) bool
	Shutdown func(context.Context, string, string) error
}

func (a releaseRuntimeAdapter) current(op registry.ReleaseOperation) bool {
	if a.Registry == nil || op.RuntimeBinding == nil || op.Allocation.Kind != "allocated" {
		return false
	}
	current, err := a.Registry.GetRelease(op.OperationID)
	if err != nil || current == nil || !reflect.DeepEqual(*current, op) {
		return false
	}
	for _, phase := range op.Phases {
		if phase.Name == registry.PhaseSupervisor {
			return phase.State == "started" && phase.Attempt != ""
		}
	}
	return false
}

func (a releaseRuntimeAdapter) record(ctx context.Context, op registry.ReleaseOperation) (runtimeowner.Record, bool) {
	if a.Outcomes == nil || op.RuntimeBinding == nil || op.RuntimeBinding.Kind != "supervisor" {
		return runtimeowner.Record{}, false
	}
	record, err := a.Outcomes.Get(ctx, op.RuntimeBinding.Generation)
	return record, err == nil && record.Generation == op.RuntimeBinding.Generation && record.Allocation == op.Allocation && reflect.DeepEqual(record.Target, op.Target)
}

func (a releaseRuntimeAdapter) Recover(ctx context.Context, op registry.ReleaseOperation) bool {
	if ctx.Err() != nil || !a.current(op) {
		return false
	}
	record, ok := a.record(ctx, op)
	// Completion of this exact generation is sufficient even if an ordinary user
	// stop finished before the release request was created. No effect is replayed.
	return ok && record.State == runtimeowner.Completed && record.Owner != nil && record.Code == "ok"
}

func (a releaseRuntimeAdapter) Run(ctx context.Context, op registry.ReleaseOperation) registry.PhaseResult {
	blocked := registry.PhaseResult{State: "blocked", Code: "failed"}
	unknown := registry.PhaseResult{State: "uncertain", Code: "unknown"}
	completed := registry.PhaseResult{State: "completed", Code: "ok"}
	if ctx.Err() != nil || !a.current(op) {
		return blocked
	}
	if op.RuntimeBinding.Kind == "never_started" {
		return completed
	}
	record, ok := a.record(ctx, op)
	if !ok {
		return blocked
	}
	if record.State == runtimeowner.Completed {
		return completed
	}
	if record.State != runtimeowner.Owned || record.Owner == nil || a.Probe == nil || a.Shutdown == nil {
		return blocked
	}
	if !a.Probe(ctx, *record.Owner) || ctx.Err() != nil || registry.VerifyReleaseTarget(op.Target) != nil || !a.current(op) {
		return blocked
	}
	// Re-read after the probe, so a concurrent ordinary stop cannot be replayed.
	fresh, ok := a.record(ctx, op)
	if !ok || !reflect.DeepEqual(fresh, record) {
		return blocked
	}
	_ = a.Shutdown(ctx, record.Generation, op.OperationID)
	// An acknowledgement alone does not establish durable completion. Conversely,
	// a lost acknowledgement can be recovered from the exact generation receipt.
	if a.Recover(ctx, op) {
		return completed
	}
	return unknown
}
