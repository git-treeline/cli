package cmd

import (
	"context"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/worktree"
)

// The executor has already bound the target and persisted this phase's start.
// Removal still performs Git's strict root/primary checks; missing is never a
// successful first execution. Only the retained receipt resolves a lost reply.
func releaseWorktreePhase(ctx context.Context, op registry.ReleaseOperation) registry.PhaseResult {
	return releaseWorktreePhaseWith(ctx, op, worktree.ValidateRemoval, worktree.Remove)
}

func releaseWorktreePhaseWith(
	ctx context.Context, op registry.ReleaseOperation,
	validate func(string) (string, error), remove func(string, bool) error,
) registry.PhaseResult {
	blocked := registry.PhaseResult{State: "blocked", Code: "failed"}
	if ctx.Err() != nil || !op.Policy.RemoveWorktree || op.Target.Common == nil {
		return blocked
	}
	started := false
	for _, phase := range op.Phases {
		if phase.Name == registry.PhaseWorktree {
			started = phase.State == "started" && phase.Attempt != ""
		}
	}
	if !started {
		return blocked
	}
	if _, err := validate(op.Target.Path); err != nil {
		return registry.PhaseResult{State: "blocked", Code: "identity_changed"}
	}
	if ctx.Err() != nil {
		return blocked
	}
	if err := remove(op.Target.Path, op.Policy.ForceDirty); err != nil {
		// A timeout/nonzero exit can follow partial filesystem changes. Do not
		// translate absence or a diagnostic string into completion/retry proof.
		return registry.PhaseResult{State: "uncertain", Code: "unknown"}
	}
	return registry.PhaseResult{State: "completed", Code: "ok"}
}
