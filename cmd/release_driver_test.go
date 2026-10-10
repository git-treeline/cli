package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/service"
	"github.com/google/uuid"
)

func TestReleaseDriverConnectsHooksRuntimeAndRetirementWithLostReply(t *testing.T) {
	ctx := context.Background()
	e, intent, a, binding, owner := runtimeReleaseFixture(t)
	plan, err := loadReleaseHookPlan(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	intent.HooksDigest = plan.digest()
	hosts := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var effects []string
	a.Shutdown = func(_ context.Context, generation, operation string) error {
		effects = append(effects, "shutdown")
		if generation != binding.Generation {
			t.Fatal("wrong generation")
		}
		if _, claimed, err := a.Outcomes.BeginStop(ctx, binding, owner, operation); err != nil || !claimed {
			t.Fatal(err)
		}
		if _, err := a.Outcomes.Complete(ctx, binding, owner, operation); err != nil {
			t.Fatal(err)
		}
		return errors.New("lost response")
	}
	d := &releaseDriver{registry: e.Registry, runtime: a, databases: releaseDatabasesAdapter{Registry: e.Registry}, hooks: releaseHooksAdapter{Registry: e.Registry, RunHook: func(_ context.Context, _, command string) checkedHookOutcome {
		effects = append(effects, command)
		return hookSettledOK
	}}, hosts: releaseHostsAdapter{Registry: e.Registry, Domain: "example.test", Read: func() (service.ManagedHostsSnapshot, error) { return service.ReadManagedHostsStrict(hosts) }}}
	e.Preflight, e.RunPhase, e.RecoverSupervisor = d.preflight, d.run, a.Recover
	completed, err := e.Start(ctx, intent)
	if err != nil || completed.Outcome() != "completed" {
		t.Fatal(completed, err)
	}
	if !reflect.DeepEqual(effects, []string{"touch release-hook-ran", "shutdown"}) {
		t.Fatal(effects)
	}
	if allocation, err := e.Registry.FindForRelease(intent.Target.Path); err != nil || allocation != nil {
		t.Fatal("not retired", err)
	}
	again, err := e.Resume(ctx, intent)
	if err != nil || !reflect.DeepEqual(again, completed) || len(effects) != 2 {
		t.Fatal("completed release replayed", again, err, effects)
	}
}

func TestReleaseDriverRefusesRoutingChangesBeforeAnyEffects(t *testing.T) {
	e, intent, a, _, _ := runtimeReleaseFixture(t)
	plan, err := loadReleaseHookPlan(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	intent.HooksDigest = plan.digest()
	hosts := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hosts, []byte(releaseHostsContent("lifecycle-fixture.previous.test")), 0600); err != nil {
		t.Fatal(err)
	}
	d := &releaseDriver{registry: e.Registry, runtime: a, databases: releaseDatabasesAdapter{Registry: e.Registry}, hooks: releaseHooksAdapter{Registry: e.Registry, RunHook: func(context.Context, string, string) checkedHookOutcome {
		t.Fatal("hook ran before full preflight")
		return hookSettledOK
	}}, hosts: releaseHostsAdapter{Registry: e.Registry, Domain: "example.test", Read: func() (service.ManagedHostsSnapshot, error) { return service.ReadManagedHostsStrict(hosts) }}}
	e.Preflight, e.RunPhase = d.preflight, d.run
	op, err := e.Start(context.Background(), intent)
	if err == nil || op == nil || op.Outcome() != "blocked" || op.Phases[0].Code != "config_changed" {
		t.Fatal(op, err)
	}
	if allocation, err := e.Registry.FindForRelease(intent.Target.Path); err != nil || allocation == nil {
		t.Fatal("preflight retired allocation", err)
	}
}

func TestReleaseDriverHostsPermissionPrecedesCleanup(t *testing.T) {
	e, intent, a, _, _ := runtimeReleaseFixture(t)
	plan, err := loadReleaseHookPlan(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	intent.HooksDigest = plan.digest()
	hosts := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hosts, []byte(releaseHostsContent("lifecycle-fixture.example.test")), 0600); err != nil {
		t.Fatal(err)
	}
	d := &releaseDriver{registry: e.Registry, runtime: a, databases: releaseDatabasesAdapter{Registry: e.Registry}, hooks: releaseHooksAdapter{Registry: e.Registry, RunHook: func(context.Context, string, string) checkedHookOutcome {
		t.Fatal("hook ran without cleanup privilege")
		return hookSettledOK
	}}, hosts: releaseHostsAdapter{Registry: e.Registry, Domain: "example.test", Read: func() (service.ManagedHostsSnapshot, error) { return service.ReadManagedHostsStrict(hosts) }}, checkHosts: func(service.ManagedHostsSnapshot) error { return service.ErrHostsPrivilegeRequired }}
	e.Preflight, e.RunPhase = d.preflight, d.run
	op, err := e.Start(context.Background(), intent)
	if err == nil || op == nil || op.Phases[0].Code != "privilege_required" {
		t.Fatal(op, err)
	}
	if record, err := a.Outcomes.Get(context.Background(), intent.RuntimeBinding.Generation); err != nil || record.State != runtimeowner.Owned {
		t.Fatal("preflight stopped runtime", err)
	}
}

func TestReleaseDriverSharedRouteNeedsNoHostsPrivilegeOrWrite(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	intent.Policy.DropDatabases = false
	intent.DatabaseConfigRevision = ""
	if err := os.WriteFile(filepath.Join(intent.Target.Path, ".treeline.yml"), []byte("project: lifecycle_fixture\nhooks: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := loadReleaseHookPlan(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	intent.HooksDigest = plan.digest()
	survivor := commandLifecycleRoot(t)
	if err := e.Registry.Allocate(registry.Allocation{"worktree": survivor, "project": "lifecycle_fixture"}); err != nil {
		t.Fatal(err)
	}
	hosts := filepath.Join(t.TempDir(), "hosts")
	content := releaseHostsContent("lifecycle-fixture.example.test")
	if err := os.WriteFile(hosts, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	checks, writes := 0, 0
	d := &releaseDriver{registry: e.Registry,
		runtime: releaseRuntimeAdapter{Registry: e.Registry},
		hooks: releaseHooksAdapter{Registry: e.Registry, RunHook: func(context.Context, string, string) checkedHookOutcome {
			t.Fatal("empty plan ran a hook")
			return hookUncertain
		}},
		hosts:      releaseHostsAdapter{Registry: e.Registry, Domain: "example.test", Read: func() (service.ManagedHostsSnapshot, error) { return service.ReadManagedHostsStrict(hosts) }, Write: func(service.ManagedHostsSnapshot, []string) error { writes++; return service.ErrHostsPrivilegeRequired }},
		checkHosts: func(service.ManagedHostsSnapshot) error { checks++; return service.ErrHostsPrivilegeRequired },
	}
	e.Preflight, e.RunPhase = d.preflight, d.run
	completed, err := e.Start(context.Background(), intent)
	if err != nil || completed == nil || completed.Outcome() != "completed" || checks != 0 || writes != 0 {
		t.Fatalf("release=%+v err=%v privilege checks=%d writes=%d", completed, err, checks, writes)
	}
	after, err := os.ReadFile(hosts)
	if err != nil || string(after) != content {
		t.Fatal("unchanged shared route rewrote hosts", err)
	}
	if allocation, err := e.Registry.FindForRelease(survivor); err != nil || allocation == nil {
		t.Fatal("shared-route survivor retired", err)
	}
}

func TestReleaseDriverProjectedHostsExcludeOnlyExactRetiringAllocation(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	a := releaseHostsAdapter{Registry: e.Registry}
	operation := registry.ReleaseOperation{ReleaseRequest: intent}
	desired, err := a.desiredExcluding("example.test", &operation)
	if err != nil || len(desired) != 0 {
		t.Fatalf("retiring route retained: %v %v", desired, err)
	}
	for _, change := range []func(*registry.ReleaseOperation){
		func(op *registry.ReleaseOperation) { op.Allocation.Generation = uuid.NewString() },
		func(op *registry.ReleaseOperation) { op.Target.Path = filepath.Join(t.TempDir(), "other") },
	} {
		changed := operation
		change(&changed)
		desired, err := a.desiredExcluding("example.test", &changed)
		if err != nil || !reflect.DeepEqual(desired, []string{"lifecycle-fixture.example.test"}) {
			t.Fatalf("nonmatching identity removed route: %v %v", desired, err)
		}
	}
	desired, err = a.desired("example.test")
	if err != nil || !reflect.DeepEqual(desired, []string{"lifecycle-fixture.example.test"}) {
		t.Fatalf("actual adapter survivor calculation changed: %v %v", desired, err)
	}
}
