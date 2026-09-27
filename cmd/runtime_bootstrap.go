package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"time"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

func runtimeOutcomeStore(socket string) *runtimeowner.Store {
	return runtimeowner.New(filepath.Join(filepath.Dir(socket), "runtime-outcomes.json"))
}

func decodeDetachedSupervisorConfig(data []byte) (detachedSupervisorConfig, *runtimeHandoff, error) {
	var runtime detachedSupervisorConfig
	if err := json.Unmarshal(data, &runtime); err != nil {
		return runtime, nil, errors.New("invalid supervisor runtime configuration")
	}
	if len(runtime.RuntimeHandoff) == 0 {
		return runtime, nil, nil
	}
	handoff, err := decodeRuntimeHandoff(runtime.RuntimeHandoff)
	if err != nil {
		return runtime, nil, err
	}
	canonical, err := json.Marshal(runtime)
	if err != nil || !bytes.Equal(canonical, data) {
		return runtime, nil, errors.New("noncanonical tracked supervisor configuration")
	}
	return runtime, &handoff, nil
}

func validateRuntimeBootstrap(runtime detachedSupervisorConfig, h runtimeHandoff, allocation registry.Allocation, configRevision string) error {
	if configRevision != h.Binding.ConfigDigest || runtime.Dir != h.Binding.Target.Path || runtime.SocketPath != supervisor.SocketPath(h.Binding.Target.Path) || runtime.Port != h.Port {
		return runtimeowner.ErrConflict
	}
	if allocation == nil || registry.GetString(allocation, "worktree") != h.Binding.Target.Path || registry.GetString(allocation, "allocation_generation") != h.Binding.Allocation.Generation {
		return runtimeowner.ErrConflict
	}
	binding, err := registry.AllocationRuntimeBinding(allocation)
	if err != nil || binding.Kind != "supervisor" || binding.Generation != h.Binding.Generation {
		return runtimeowner.ErrConflict
	}
	return nil
}

func trackedDetachedReady(ctx context.Context, detached *detachedSupervisor, h runtimeHandoff, store *runtimeowner.Store, observe func(int) (runtimeowner.Owner, error), status func(string, string) (string, error)) bool {
	if ctx == nil || ctx.Err() != nil || detached == nil || detached.owner == nil || detached.socket != supervisor.SocketPath(h.Binding.Target.Path) || store == nil || observe == nil || status == nil {
		return false
	}
	record, err := store.Query(ctx, h.Binding)
	if err != nil || record.State != runtimeowner.Owned || record.Owner == nil || *record.Owner != *detached.owner {
		return false
	}
	actual, err := observe(detached.owner.PID)
	if err != nil || actual != *detached.owner {
		return false
	}
	state, err := status(detached.socket, h.Binding.Generation)
	if err != nil || state != "owned" || ctx.Err() != nil {
		return false
	}
	current, err := store.Query(ctx, h.Binding)
	return err == nil && reflect.DeepEqual(current, record)
}

// cleanupTrackedDetachedSupervisor never guesses from PID sidecars or falls
// back to signals. Unknown ownership remains retained for exact recovery.
func cleanupTrackedDetachedSupervisor(ctx context.Context, detached *detachedSupervisor, h runtimeHandoff, store *runtimeowner.Store, observe func(int) (runtimeowner.Owner, error), shutdown func(string, string, string, time.Duration) error) error {
	if ctx == nil || ctx.Err() != nil || detached == nil || detached.owner == nil || detached.socket != supervisor.SocketPath(h.Binding.Target.Path) || store == nil || observe == nil || shutdown == nil {
		return runtimeowner.ErrUnknown
	}
	record, err := store.Query(ctx, h.Binding)
	if err != nil {
		return err
	}
	if record.Owner == nil || *record.Owner != *detached.owner {
		return runtimeowner.ErrConflict
	}
	if record.State == runtimeowner.Completed {
		return nil
	}
	if record.State != runtimeowner.Owned && record.State != runtimeowner.Stopping {
		return runtimeowner.ErrUncertain
	}
	actual, err := observe(detached.owner.PID)
	if err != nil || actual != *detached.owner {
		return runtimeowner.ErrUnknown
	}
	current, err := store.Query(ctx, h.Binding)
	if err != nil || !reflect.DeepEqual(current, record) {
		return runtimeowner.ErrUncertain
	}
	operation := record.OperationID
	if operation == "" {
		operation = uuid.NewString()
	}
	_ = shutdown(detached.socket, h.Binding.Generation, operation, 30*time.Second)
	current, err = store.Query(ctx, h.Binding)
	if err != nil || current.State != runtimeowner.Completed || current.Owner == nil || *current.Owner != *detached.owner {
		return runtimeowner.ErrUncertain
	}
	return nil
}
