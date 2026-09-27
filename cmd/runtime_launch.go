package cmd

import (
	"context"
	"errors"
	"os"
	"reflect"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/supervisor"
)

func hasTrackedRuntime(reg *registry.Registry, dir string) (bool, error) {
	allocation, err := reg.FindForRelease(dir)
	if err != nil {
		return false, err
	}
	if allocation == nil {
		return false, nil
	}
	binding, err := registry.AllocationRuntimeBinding(allocation)
	return binding.Kind != "unknown", err
}

// runTrackedStart is shared by explicit start and fresh worktree --start. It
// accepts an already-held creation lock instead of deadlocking by reacquiring.
type runtimeLaunchEffects struct {
	store   func(string) *runtimeowner.Store
	observe func(int) (runtimeowner.Owner, error)
	run     func(*supervisor.Supervisor) error
	hook    checkedHookRunner
}

func runTrackedStart(ctx context.Context, dir string, locks *registry.LifecycleLocks, with string, await bool) error {
	return runTrackedStartWith(ctx, dir, locks, with, await, runtimeLaunchEffects{runtimeOutcomeStore, runtimeowner.ObserveOwner, func(sv *supervisor.Supervisor) error { return sv.Run() }, RunCheckedHook})
}

func runTrackedStartWith(ctx context.Context, dir string, locks *registry.LifecycleLocks, with string, await bool, effects runtimeLaunchEffects) error {
	if effects.store == nil || effects.observe == nil || effects.run == nil || effects.hook == nil {
		return runtimeowner.ErrUnknown
	}
	var err error
	dir, err = registry.CanonicalLifecycleTarget(dir)
	if err != nil {
		return err
	}
	if locks == nil {
		locks, err = acquireCommandLifecycleLocks(ctx, []string{dir})
		if err != nil {
			return err
		}
	}
	defer locks.Close()
	reg := registry.New("")
	if err := reg.CheckReleaseReservations([]string{dir}); err != nil {
		return err
	}
	target, err := registry.CaptureReleaseTarget(dir)
	if err != nil {
		return err
	}
	revision, err := format.DatabaseCleanupConfigRevision(dir)
	if err != nil {
		return err
	}
	pc := config.LoadProjectConfigReadOnlyQuiet(dir)
	if pc.LoadError() != nil || pc.Validate() != nil || pc.StartCommand() == "" {
		return errors.New("tracked runtime configuration unavailable")
	}
	selected, err := resolveStartHooks(pc, with)
	if err != nil {
		return err
	}
	if after, err := format.DatabaseCleanupConfigRevision(dir); err != nil || after != revision {
		return errors.New("runtime configuration changed")
	}
	socket := supervisor.SocketPath(dir)
	store := effects.store(socket)
	allocation, err := reg.FindForRelease(dir)
	if err != nil {
		return err
	}
	binding, err := registry.AllocationRuntimeBinding(allocation)
	if err != nil {
		return err
	}
	if binding.Kind == "supervisor" {
		prior, err := store.Get(ctx, binding.Generation)
		if err != nil {
			return err
		}
		if prior.Allocation.Generation != registry.GetString(allocation, "allocation_generation") || !reflect.DeepEqual(prior.Target, target) {
			return runtimeowner.ErrConflict
		}
		if prior.State == runtimeowner.Owned {
			if prior.Owner == nil || !runtimeowner.VerifyOwner(*prior.Owner) || prior.ConfigDigest != revision {
				return runtimeowner.ErrUnknown
			}
			if state, err := supervisor.RuntimeGenerationStatus(socket, binding.Generation); err != nil || state != "owned" {
				return runtimeowner.ErrUnknown
			}
			status, err := supervisor.Send(socket, "status")
			if err != nil {
				return err
			}
			if status == "stopped" {
				return resumeSupervisor(socket, dir)
			}
			if status == "running" && await {
				return awaitReady(socket)
			}
			return errServerAlreadyRunning()
		}
	}
	port := resolvePort(dir)
	h, err := prepareRuntimeStart(ctx, reg, locks, store, target, selected, port, revision)
	if err != nil {
		return err
	}
	// The generation was retained before any hook can start work. Failure is
	// retained as uncertain; it cannot authorize a second execution of a prefix.
	for _, entry := range selected {
		for _, command := range entry.Hook.PreStart {
			if effects.hook(ctx, dir, interpolateCommand(command, port)) != hookSettledOK {
				_, _ = store.MarkUncertain(context.Background(), h.Binding, nil, "", "child_failed")
				return errors.New("tracked pre-start hook did not settle")
			}
		}
	}
	if registry.VerifyReleaseTarget(target) != nil {
		return runtimeowner.ErrConflict
	}
	if after, err := format.DatabaseCleanupConfigRevision(dir); err != nil || after != revision {
		return errors.New("runtime configuration changed")
	}
	env, err := setup.SyncRuntimeEnv(dir, config.LoadUserConfig(""))
	if err != nil {
		return err
	}
	if registry.VerifyReleaseTarget(target) != nil {
		return runtimeowner.ErrConflict
	}
	if after, err := format.DatabaseCleanupConfigRevision(dir); err != nil || after != revision {
		return errors.New("runtime configuration changed")
	}
	if await {
		return startAwaitDetached(ctx, pc.StartCommand(), dir, socket, port, env, pc, &h)
	}
	owner, err := effects.observe(os.Getpid())
	if err != nil {
		return err
	}
	sv := supervisor.New(pc.StartCommand(), dir, socket)
	sv.Env, sv.Port = env, port
	if err := configureTrackedRuntime(sv, store, h, owner, effects.hook); err != nil {
		return err
	}
	sv.Started = func() { _ = locks.Close() }
	return effects.run(sv)
}
