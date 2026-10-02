package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/devresources"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/worktree"
)

type releaseDriver struct {
	registry        *registry.Registry
	runtime         releaseRuntimeAdapter
	databases       releaseDatabasesAdapter
	hooks           releaseHooksAdapter
	hosts           releaseHostsAdapter
	skipHosts       bool
	checkHosts      func(service.ManagedHostsSnapshot) error
	validateRemoval func(string) (string, error)
	remove          func(context.Context, registry.ReleaseOperation) registry.PhaseResult
}

func validateManagedDevRelease(op registry.ReleaseOperation) error {
	if !devresources.Active() {
		return nil
	}
	if err := validateManagedDevRetainedTarget(op.Target); err != nil {
		return err
	}
	if op.HooksDigest != (releaseHookPlan{}).digest() {
		return errors.New("managed dev release hooks are unavailable")
	}
	if op.Snapshot == nil {
		if op.Allocation.Kind != "unallocated" {
			return errors.New("managed dev release allocation unavailable")
		}
		if _, err := devresources.Check(); err != nil {
			return errors.New("managed dev release policy unavailable")
		}
		if _, err := os.Stat(op.Target.Path); err == nil {
			return setup.ValidateManagedDevProject(op.Target.Path, op.Target.Path, config.LoadProjectConfigReadOnlyQuiet(op.Target.Path))
		} else if !os.IsNotExist(err) || remainingReleasePhase(op, registry.PhaseWorktree) {
			return errors.New("managed dev release target unavailable")
		}
		return nil
	}
	snapshot := op.Snapshot
	if snapshot.DatabaseAdapter != "" || len(snapshot.Databases) != 0 || snapshot.RedisDB != nil {
		return errors.New("managed dev release resources are unavailable")
	}
	allocation := registry.Allocation{
		"worktree":      op.Target.Path,
		"project":       snapshot.Project,
		"branch":        snapshot.Branch,
		"main_worktree": snapshot.MainWorktree,
		"ports":         snapshot.Ports,
		"databases":     snapshot.Databases,
	}
	if snapshot.RedisDB != nil {
		allocation["redis_db"] = *snapshot.RedisDB
	}
	policy, err := devresources.Check()
	if err != nil || policy == nil {
		return errors.New("managed dev release policy unavailable")
	}
	if err := policy.ValidateAllocation(map[string]any(allocation)); err != nil {
		return err
	}
	current, err := registry.New("").FindForRelease(op.Target.Path)
	if err != nil {
		return err
	}
	if current != nil {
		if registry.GetString(current, "allocation_generation") != op.Allocation.Generation {
			return errors.New("managed dev release allocation changed")
		}
		if err := policy.ValidateAllocation(map[string]any(current)); err != nil {
			return err
		}
	} else if remainingReleasePhase(op, registry.PhaseAllocation) {
		return errors.New("managed dev release allocation proof unavailable")
	}
	if _, err := os.Stat(op.Target.Path); err == nil {
		return setup.ValidateManagedDevProject(op.Target.Path, op.Target.Path, config.LoadProjectConfigReadOnlyQuiet(op.Target.Path))
	} else if !os.IsNotExist(err) || remainingReleasePhase(op, registry.PhaseWorktree) {
		return errors.New("managed dev release target unavailable")
	}
	return nil
}

func validateManagedDevRetainedTarget(target registry.TargetIdentity) error {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return errors.New("managed dev workspace root unavailable")
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return errors.New("managed dev workspace root unavailable")
	}
	root := filepath.Join(home, ".treeline-dev", "workspaces")
	canonicalRoot, err := registry.CanonicalLifecycleTarget(root)
	if err != nil || canonicalRoot != root {
		return errors.New("managed dev workspace root unavailable")
	}
	contained := func(path string) bool {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return false
		}
		canonical, err := registry.CanonicalLifecycleTarget(path)
		return err == nil && canonical == path && strings.HasPrefix(path, root+string(filepath.Separator))
	}
	if !contained(target.Path) {
		return errors.New("managed dev release target outside disposable workspace")
	}
	if target.Common != nil && !contained(target.Common.Path) {
		return errors.New("managed dev release Git metadata outside disposable workspace")
	}
	return nil
}

func productionReleaseExecutor(reg *registry.Registry) (*releaseExecutor, error) {
	d, err := productionReleaseDriver(reg)
	if err != nil {
		return nil, err
	}
	return &releaseExecutor{Registry: reg, Preflight: d.preflight, RunPhase: d.run, RecoverSupervisor: func(ctx context.Context, op registry.ReleaseOperation) bool { return d.runtimeFor(op).Recover(ctx, op) }}, nil
}

func productionReleaseDriver(reg *registry.Registry) (*releaseDriver, error) {
	if appManagedDev() && appManagedDevHome() != nil {
		return nil, releaseRefusal("config_unavailable")
	}
	uc := config.LoadUserConfig("")
	if uc.LoadError() != nil || !releaseHostsDomainValid(uc.RouterDomain()) {
		return nil, releaseRefusal("config_unavailable")
	}
	d := &releaseDriver{registry: reg, databases: releaseDatabasesAdapter{Registry: reg}, hooks: releaseHooksAdapter{Registry: reg, RunHook: RunCheckedHook}, hosts: releaseHostsAdapter{Registry: reg, Domain: uc.RouterDomain()}, checkHosts: service.CheckManagedHostsWrite, validateRemoval: worktree.ValidateRemoval, remove: releaseWorktreePhase}
	d.skipHosts = appManagedDev() && uc.RouterMode() == config.RouterModeDisabled
	d.runtime = releaseRuntimeAdapter{Registry: reg, Probe: func(ctx context.Context, owner runtimeowner.Owner) bool {
		return ctx.Err() == nil && runtimeowner.VerifyOwner(owner)
	}}
	return d, nil
}

func (d *releaseDriver) runtimeFor(op registry.ReleaseOperation) releaseRuntimeAdapter {
	a := d.runtime
	if a.Outcomes == nil {
		a.Outcomes = runtimeOutcomeStore(supervisor.SocketPath(op.Target.Path))
	}
	if a.Shutdown == nil {
		a.Shutdown = func(ctx context.Context, generation, operation string) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return supervisor.ShutdownSettledGeneration(supervisor.SocketPath(op.Target.Path), generation, operation, 30*time.Second)
		}
	}
	return a
}

func remainingReleasePhase(op registry.ReleaseOperation, name registry.ReleasePhase) bool {
	for _, p := range op.Phases {
		if p.Name == name {
			return p.State != "completed"
		}
	}
	return false
}

func (d *releaseDriver) preflight(ctx context.Context, op registry.ReleaseOperation) registry.PhaseResult {
	blocked := registry.PhaseResult{State: "blocked", Code: "failed"}
	if err := validateManagedDevRelease(op); err != nil {
		return blocked
	}
	if ctx.Err() != nil || d.registry == nil || registry.VerifyReleaseTarget(op.Target) != nil {
		return blocked
	}
	current, err := d.registry.GetRelease(op.OperationID)
	if err != nil || current == nil || !reflect.DeepEqual(*current, op) {
		return blocked
	}
	if remainingReleasePhase(op, registry.PhaseWorktree) {
		if d.validateRemoval == nil {
			return blocked
		}
		if _, err := d.validateRemoval(op.Target.Path); err != nil {
			return registry.PhaseResult{State: "blocked", Code: "identity_changed"}
		}
	}
	if op.Allocation.Kind == "unallocated" {
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	if op.Snapshot == nil || op.RuntimeBinding == nil {
		return blocked
	}
	if remainingReleasePhase(op, registry.PhasePreHook) || remainingReleasePhase(op, registry.PhasePostHook) {
		plan, err := loadReleaseHookPlan(op.Target.Path)
		if err != nil || plan.digest() != op.HooksDigest {
			return registry.PhaseResult{State: "blocked", Code: "config_changed"}
		}
		if (len(plan.pre) > 0 || len(plan.post) > 0) && d.hooks.RunHook == nil {
			return blocked
		}
	}
	if remainingReleasePhase(op, registry.PhaseSupervisor) && op.RuntimeBinding.Kind != "never_started" {
		a := d.runtimeFor(op)
		record, ok := a.record(ctx, op)
		if !ok || record.Owner == nil {
			return blocked
		}
		if record.State != runtimeowner.Completed && (record.State != runtimeowner.Owned || a.Probe == nil || !a.Probe(ctx, *record.Owner)) {
			return blocked
		}
	}
	if remainingReleasePhase(op, registry.PhaseDatabases) {
		keep, err := d.databases.survivors(op)
		if err != nil {
			return blocked
		}
		_, err = format.DropDatabasesChecked(ctx, op.Target.Path, *op.Snapshot, keep, format.DatabaseCleanupCheck{ConfigRevision: op.DatabaseConfigRevision, ValidateOnly: true, NewAdapter: d.databases.NewAdapter, BeforeDrop: func() error {
			if registry.VerifyReleaseTarget(op.Target) != nil {
				return errors.New("target changed")
			}
			return nil
		}})
		if err != nil {
			if errors.Is(err, format.ErrDatabaseCleanupConfig) {
				return registry.PhaseResult{State: "blocked", Code: "config_changed"}
			}
			return blocked
		}
	}
	if !d.skipHosts && remainingReleasePhase(op, registry.PhaseHosts) {
		domain := strings.ToLower(d.hosts.Domain)
		if !releaseHostsDomainValid(domain) {
			return registry.PhaseResult{State: "blocked", Code: "config_changed"}
		}
		desired, err := d.hosts.desiredExcluding(domain, &op)
		if err != nil {
			return blocked
		}
		read := d.hosts.Read
		if read == nil {
			read = func() (service.ManagedHostsSnapshot, error) { return service.ReadManagedHostsStrict("") }
		}
		snapshot, err := read()
		if err != nil {
			return blocked
		}
		released := strings.ToLower(proxy.RouteKey(op.Snapshot.Project, op.Snapshot.Branch) + "." + domain)
		// Unknown managed aliases may reflect changed routing configuration.
		// Do not certify cleanup using a newly selected domain after restart.
		managed := normalizedReleaseHosts(snapshot.Hostnames())
		for _, host := range managed {
			if host != released && !slices.Contains(desired, host) {
				return registry.PhaseResult{State: "blocked", Code: "config_changed"}
			}
		}
		if op.Snapshot.Project != "" && slices.Contains(managed, released) && !slices.Equal(managed, desired) {
			if d.checkHosts == nil {
				return blocked
			}
			if err := d.checkHosts(snapshot); err != nil {
				if errors.Is(err, service.ErrHostsPrivilegeRequired) {
					return registry.PhaseResult{State: "blocked", Code: "privilege_required"}
				}
				return blocked
			}
		}
	}
	return registry.PhaseResult{State: "completed", Code: "ok"}
}

func (d *releaseDriver) run(ctx context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
	if err := validateManagedDevRelease(op); err != nil {
		return registry.PhaseResult{State: "blocked", Code: "failed"}
	}
	switch phase {
	case registry.PhasePreHook, registry.PhasePostHook:
		return d.hooks.Run(ctx, op, phase)
	case registry.PhaseSupervisor:
		return d.runtimeFor(op).Run(ctx, op)
	case registry.PhaseDatabases:
		return d.databases.Run(ctx, op)
	case registry.PhaseHosts:
		if d.skipHosts {
			return registry.PhaseResult{State: "completed", Code: "ok"}
		}
		return d.hosts.Run(ctx, op)
	case registry.PhaseWorktree:
		if d.remove != nil {
			return d.remove(ctx, op)
		}
	}
	return registry.PhaseResult{State: "blocked", Code: "failed"}
}
