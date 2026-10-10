package cmd

import (
	"context"
	"errors"
	"reflect"

	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/google/uuid"
)

// prepareRuntimeStart consumes fresh or durably completed ownership while the
// caller holds lifecycle exclusion. It returns a new launch exactly once: a
// registration interrupted before handoff remains unknown and is not replayed.
func prepareRuntimeStart(ctx context.Context, reg *registry.Registry, locks *registry.LifecycleLocks, store *runtimeowner.Store, target registry.TargetIdentity, selected []startHookEntry, port int, configRevision string) (runtimeHandoff, error) {
	refuse := errors.New("runtime start ownership unavailable")
	if ctx.Err() != nil || reg == nil || store == nil || registry.VerifyReleaseTarget(target) != nil {
		return runtimeHandoff{}, refuse
	}
	revision, err := format.DatabaseCleanupConfigRevision(target.Path)
	if err != nil || revision != configRevision {
		return runtimeHandoff{}, refuse
	}
	allocation, err := reg.FindForRelease(target.Path)
	if err != nil || allocation == nil {
		return runtimeHandoff{}, refuse
	}
	prior, err := registry.AllocationRuntimeBinding(allocation)
	if err != nil || prior.Kind == "unknown" {
		return runtimeHandoff{}, refuse
	}
	ref := registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(allocation, "allocation_generation")}
	if prior.Kind == "supervisor" {
		completed, err := store.Get(ctx, prior.Generation)
		if err != nil || completed.State != runtimeowner.Completed || completed.Allocation != ref || !reflect.DeepEqual(completed.Target, target) {
			return runtimeHandoff{}, refuse
		}
	}
	h, err := captureRuntimeHandoff(runtimeowner.Binding{Target: target, Allocation: ref, Generation: uuid.NewString(), ConfigDigest: revision}, selected, port)
	if err != nil {
		return runtimeHandoff{}, err
	}
	if _, err := reg.RegisterRuntime(locks, target, ref.Generation, prior, h.Binding.Generation); err != nil {
		return runtimeHandoff{}, err
	}
	// Once the generation is retained, any later failure leaves that exact start
	// uncertain; neither a missing receipt nor an empty socket authorizes retry.
	if _, err := store.Prepare(ctx, h.Binding); err != nil {
		return runtimeHandoff{}, err
	}
	return h, nil
}
