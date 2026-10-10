package cmd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/devresources"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

type runtimeActionEffects struct {
	store    func(string) *runtimeowner.Store
	verify   func(runtimeowner.Owner) bool
	status   func(string, string) (string, error)
	command  func(string, string) error
	shutdown func(string, string, string, time.Duration) error
}

func runTrackedRuntimeAction(ctx context.Context, dir, action string) (bool, error) {
	return runTrackedRuntimeActionWith(ctx, dir, action, runtimeActionEffects{
		store: runtimeOutcomeStore, verify: runtimeowner.VerifyOwner, status: supervisor.RuntimeGenerationStatus, shutdown: supervisor.ShutdownSettledGeneration,
		command: func(socket, action string) error {
			if action == "restart" {
				env, err := setup.SyncRuntimeEnv(dir, config.LoadUserConfig(""))
				if err != nil {
					return err
				}
				_, err = supervisor.ConfigureAndSend(socket, action, env, resolvePort(dir))
				return err
			}
			response, err := supervisor.Send(socket, action)
			if err != nil {
				return err
			}
			if strings.TrimSpace(response) != "ok" {
				return errors.New("runtime action was not acknowledged")
			}
			return nil
		},
	})
}

func runTrackedRuntimeActionWith(ctx context.Context, dir, action string, effects runtimeActionEffects) (bool, error) {
	reg := registry.New("")
	tracked, err := hasTrackedRuntime(reg, dir)
	if err != nil || !tracked {
		if devresources.Active() && err == nil {
			return true, errors.New("managed dev runtime requires a tracked allocation")
		}
		return tracked, err
	}
	if ctx == nil || ctx.Err() != nil || (action != "stop" && action != "shutdown" && action != "restart") || effects.store == nil || effects.verify == nil || effects.status == nil || effects.command == nil || effects.shutdown == nil {
		return true, runtimeowner.ErrUnknown
	}
	dir, err = registry.CanonicalLifecycleTarget(dir)
	if err != nil {
		return true, err
	}
	locks, err := acquireCommandLifecycleLocks(ctx, []string{dir})
	if err != nil {
		return true, err
	}
	// Cleanup does not change the completed operation or its receipt.
	defer func() { _ = locks.Close() }()
	if err := reg.CheckReleaseReservations([]string{dir}); err != nil {
		return true, err
	}
	allocation, err := reg.FindForRelease(dir)
	if err != nil {
		return true, err
	}
	if err := validateManagedDevRuntime(dir, allocation, false); err != nil {
		return true, err
	}
	binding, err := registry.AllocationRuntimeBinding(allocation)
	if err != nil {
		return true, err
	}
	if binding.Kind == "never_started" && (action == "stop" || action == "shutdown") {
		return true, nil
	}
	if binding.Kind != "supervisor" {
		return true, runtimeowner.ErrUnknown
	}
	target, err := registry.CaptureReleaseTarget(dir)
	if err != nil {
		return true, err
	}
	socket := supervisor.SocketPath(dir)
	store := effects.store(socket)
	if store == nil {
		return true, runtimeowner.ErrUnknown
	}
	record, err := store.Get(ctx, binding.Generation)
	if err != nil {
		return true, err
	}
	if record.Allocation.Generation != registry.GetString(allocation, "allocation_generation") || !reflect.DeepEqual(record.Target, target) {
		return true, runtimeowner.ErrConflict
	}
	if record.State == runtimeowner.Completed && (action == "stop" || action == "shutdown") {
		return true, nil
	}
	if record.State != runtimeowner.Owned || record.Owner == nil || !effects.verify(*record.Owner) {
		return true, runtimeowner.ErrUnknown
	}
	state, err := effects.status(socket, binding.Generation)
	if err != nil || state != "owned" {
		return true, runtimeowner.ErrUnknown
	}
	if devresources.Active() {
		if err := supervisor.RequireManagedDevAdmission(socket); err != nil {
			return true, err
		}
	}
	current, err := store.Query(ctx, record.Binding)
	if err != nil || !reflect.DeepEqual(current, record) || ctx.Err() != nil {
		return true, runtimeowner.ErrUnknown
	}
	if action == "shutdown" {
		_ = effects.shutdown(socket, binding.Generation, uuid.NewString(), 30*time.Second)
		result, err := store.Query(ctx, record.Binding)
		if err != nil || result.State != runtimeowner.Completed || result.Owner == nil || *result.Owner != *record.Owner {
			return true, runtimeowner.ErrUncertain
		}
		return true, nil
	}
	return true, effects.command(socket, action)
}
