package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
)

const checkedHooksConfig = "project: lifecycle_fixture\nhooks:\n  pre_release: ['first', 'second']\n  post_release: ['post-first', 'post-second']\n"

func releaseHooksFixture(t *testing.T, phase registry.ReleasePhase, ignore bool) (releaseHooksAdapter, registry.ReleaseOperation, registry.PhaseToken) {
	t.Helper()
	e, intent := releaseExecutorFixture(t)
	if err := os.WriteFile(filepath.Join(intent.Target.Path, config.ProjectConfigFile), []byte(checkedHooksConfig), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := loadReleaseHookPlan(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	intent.HooksDigest, intent.Policy.IgnoreHookFailure = plan.digest(), ignore
	op, _, err := e.Registry.PrepareRelease(intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range op.Phases {
		token, err := e.Registry.BeginReleasePhase(op.OperationID, record.Name, op.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if record.Name == phase {
			started, err := e.Registry.GetRelease(op.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			return releaseHooksAdapter{Registry: e.Registry}, *started, token
		}
		if record.Name == registry.PhaseAllocation {
			op, err = e.Registry.RetireReleaseAllocation(token)
		} else {
			op, err = e.Registry.RecordReleasePhase(token, registry.PhaseResult{State: "completed", Code: "ok"})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("phase missing")
	return releaseHooksAdapter{}, registry.ReleaseOperation{}, registry.PhaseToken{}
}

func TestReleaseHooksPlanStrictReadOnlyDigest(t *testing.T) {
	commandLifecycleEnvironment(t)
	root := commandLifecycleRoot(t)
	file := filepath.Join(root, config.ProjectConfigFile)
	for _, raw := range []string{"project: fixture\n", "project: fixture\nhooks: {}\n", "project: fixture\nhooks: {pre_release: [], post_release: []}\n"} {
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		plan, err := loadReleaseHookPlan(root)
		want := sha256.Sum256([]byte(`{"pre_release":[],"post_release":[]}`))
		if err != nil || plan.digest() != hex.EncodeToString(want[:]) {
			t.Fatalf("empty plan: %v %+v", err, plan)
		}
	}
	for _, raw := range []string{
		"hooks: []", "hooks: null", "hooks: {pre_release: scalar}", "hooks: {post_release: [valid, 17]}",
		"hooks: {pre_release: null}", "hooks: {pre_release: ['\\u0000']}", "hooks: [broken",
	} {
		// A YAML double-quoted NUL escape is decoded into the forbidden byte.
		raw = strings.ReplaceAll(raw, "'\\u0000'", `"\u0000"`)
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadReleaseHookPlan(root); err == nil || err.Error() != "invalid_release_hooks" {
			t.Fatalf("malformed hooks accepted or leaked: %q %v", raw, err)
		}
	}
	raw := "project: fixture\ndatabase: {pattern: old_name}\nhooks: {pre_release: [secret-command], post_release: []}\n"
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := loadReleaseHookPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	if string(after) != raw || len(plan.pre) != 1 || plan.pre[0] != "secret-command" {
		t.Fatal("read-only plan changed config or lost command")
	}
	if plan.digest() == (releaseHookPlan{pre: []string{"different"}}).digest() {
		t.Fatal("digest omitted commands")
	}
}

func TestReleaseHooksAdapterCheckedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcomes []checkedHookOutcome
		ignore   bool
		want     registry.PhaseResult
	}{
		{"success", []checkedHookOutcome{hookSettledOK, hookSettledOK}, false, registry.PhaseResult{State: "completed", Code: "ok"}},
		{"not_started", []checkedHookOutcome{hookNotStarted}, false, registry.PhaseResult{State: "blocked", Code: "failed"}},
		{"partial_not_started", []checkedHookOutcome{hookSettledOK, hookNotStarted}, false, registry.PhaseResult{State: "uncertain", Code: "unknown"}},
		{"failed", []checkedHookOutcome{hookSettledFailed}, false, registry.PhaseResult{State: "uncertain", Code: "unknown"}},
		{"ignored_settled_failure", []checkedHookOutcome{hookSettledFailed}, true, registry.PhaseResult{State: "completed", Code: "ignored_hook_failure"}},
		{"unknown_cannot_ignore", []checkedHookOutcome{hookUncertain}, true, registry.PhaseResult{State: "uncertain", Code: "unknown"}},
		{"malformed", []checkedHookOutcome{"bogus"}, true, registry.PhaseResult{State: "uncertain", Code: "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, op, token := releaseHooksFixture(t, registry.PhasePreHook, tc.ignore)
			var calls []string
			a.RunHook = func(_ context.Context, dir, command string) checkedHookOutcome {
				if dir != op.Target.Path {
					t.Fatal("wrong cwd")
				}
				current, err := a.Registry.GetRelease(op.OperationID)
				if err != nil || !reflect.DeepEqual(current, &op) {
					t.Fatal("missing persisted start")
				}
				calls = append(calls, command)
				return tc.outcomes[len(calls)-1]
			}
			got := a.Run(context.Background(), op, registry.PhasePreHook)
			if got != tc.want || len(calls) != len(tc.outcomes) {
				t.Fatalf("got %+v calls %v", got, calls)
			}
			if _, err := a.Registry.RecordReleasePhase(token, got); err != nil {
				t.Fatal(err)
			}
			bytes, err := os.ReadFile(a.Registry.Path)
			if err != nil || strings.Contains(string(bytes), "post-first") || strings.Contains(string(bytes), `"first"`) {
				t.Fatal("receipt contains hook commands")
			}
		})
	}
}

func TestReleaseHooksAdapterRefusesBeforeDispatch(t *testing.T) {
	for _, kind := range []string{"config", "identity", "record", "cancelled", "runner_missing", "not_started"} {
		t.Run(kind, func(t *testing.T) {
			a, op, token := releaseHooksFixture(t, registry.PhasePreHook, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a.RunHook = func(context.Context, string, string) checkedHookOutcome {
				t.Fatal("unexpected dispatch")
				return hookUncertain
			}
			switch kind {
			case "config":
				if err := os.WriteFile(filepath.Join(op.Target.Path, config.ProjectConfigFile), []byte("hooks: {}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "identity":
				if err := os.Rename(op.Target.Path, op.Target.Path+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(op.Target.Path + "-old") })
				if err := os.Mkdir(op.Target.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "record":
				if _, err := a.Registry.RecordReleasePhase(token, registry.PhaseResult{State: "blocked", Code: "failed"}); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "runner_missing":
				a.RunHook = nil
			case "not_started":
				op.Phases[0].State = "pending"
			}
			if got := a.Run(ctx, op, registry.PhasePreHook); got.State != "blocked" {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestReleaseHooksAdapterRechecksBeforeEveryCommand(t *testing.T) {
	for _, change := range []string{"config", "identity", "record", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			a, op, token := releaseHooksFixture(t, registry.PhasePreHook, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			a.RunHook = func(context.Context, string, string) checkedHookOutcome {
				calls++
				if calls > 1 {
					t.Fatal("changed binding dispatched second command")
				}
				switch change {
				case "config":
					if err := os.WriteFile(filepath.Join(op.Target.Path, config.ProjectConfigFile), []byte("hooks: {}\n"), 0600); err != nil {
						t.Fatal(err)
					}
				case "identity":
					if err := os.Rename(op.Target.Path, op.Target.Path+"-old"); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.RemoveAll(op.Target.Path + "-old") })
					if err := os.Mkdir(op.Target.Path, 0700); err != nil {
						t.Fatal(err)
					}
				case "record":
					if _, err := a.Registry.RecordReleasePhase(token, registry.PhaseResult{State: "uncertain", Code: "unknown"}); err != nil {
						t.Fatal(err)
					}
				case "cancelled":
					cancel()
				}
				return hookSettledOK
			}
			if got := a.Run(ctx, op, registry.PhasePreHook); got.State != "uncertain" || calls != 1 {
				t.Fatalf("got %+v calls %d", got, calls)
			}
		})
	}
}

func TestReleaseHooksAdapterPostAfterRetirementAndPanic(t *testing.T) {
	a, op, _ := releaseHooksFixture(t, registry.PhasePostHook, false)
	if allocation, err := a.Registry.FindForRelease(op.Target.Path); err != nil || allocation != nil {
		t.Fatalf("not retired: %v", err)
	}
	var calls []string
	a.RunHook = func(_ context.Context, _, command string) checkedHookOutcome {
		calls = append(calls, command)
		return hookSettledOK
	}
	if got := a.Run(context.Background(), op, registry.PhasePostHook); got.State != "completed" || !reflect.DeepEqual(calls, []string{"post-first", "post-second"}) {
		t.Fatalf("got %+v calls %v", got, calls)
	}
	a.RunHook = func(context.Context, string, string) checkedHookOutcome { panic("secret-output") }
	if got := a.Run(context.Background(), op, registry.PhasePostHook); got != (registry.PhaseResult{State: "uncertain", Code: "unknown"}) {
		t.Fatalf("panic: %+v", got)
	}
}

func TestReleaseHooksAdapterUncertainResumeNeverReplays(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	plan, err := loadReleaseHookPlan(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	intent.HooksDigest = plan.digest()
	calls := 0
	a := releaseHooksAdapter{Registry: e.Registry, RunHook: func(context.Context, string, string) checkedHookOutcome { calls++; return hookUncertain }}
	e.RunPhase = a.Run
	op, err := e.Start(context.Background(), intent)
	if err == nil || op.Outcome() != "uncertain" || calls != 1 {
		t.Fatalf("start: %+v %v calls %d", op, err, calls)
	}
	e.Registry = registry.New(e.Registry.Path)
	for range 2 {
		op, err = e.Resume(context.Background(), intent)
		if err == nil || op.Outcome() != "uncertain" || calls != 1 {
			t.Fatalf("resume: %+v %v calls %d", op, err, calls)
		}
	}
}

func TestReleaseHooksAdapterEmptyPlanNeedsNoRunner(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	file := filepath.Join(intent.Target.Path, config.ProjectConfigFile)
	if err := os.WriteFile(file, []byte("project: lifecycle_fixture\nhooks: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := loadReleaseHookPlan(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	intent.HooksDigest = plan.digest()
	a := releaseHooksAdapter{Registry: e.Registry}
	var calls []registry.ReleasePhase
	e.RunPhase = func(ctx context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		if phase == registry.PhasePreHook || phase == registry.PhasePostHook {
			calls = append(calls, phase)
			return a.Run(ctx, op, phase)
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	op, err := e.Start(context.Background(), intent)
	if err != nil || op.Outcome() != "completed" || !reflect.DeepEqual(calls, []registry.ReleasePhase{registry.PhasePreHook, registry.PhasePostHook}) {
		t.Fatalf("empty plan: %+v %v calls %v", op, err, calls)
	}
}

func TestReleaseHooksAdapterRechecksGitAssociation(t *testing.T) {
	main, linked := releaseSafetyFixture(t)
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	releaseSafetyGit(t, other, "init", "-qb", "main")
	if err := os.WriteFile(filepath.Join(linked, config.ProjectConfigFile), []byte(checkedHooksConfig), 0600); err != nil {
		t.Fatal(err)
	}
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, linked)
	allocation, err := reg.FindForRelease(linked)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := loadReleaseHookPlan(linked)
	if err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(main, ".git")
	op, _, err := reg.PrepareRelease(registry.ReleaseRequest{
		OperationID: uuid.NewString(),
		Target: registry.TargetIdentity{Path: linked, Directory: commandDirectoryIdentity(t, linked),
			Common: &registry.CommonIdentity{Path: common, DirectoryIdentity: commandDirectoryIdentity(t, common)}},
		Allocation:  registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(allocation, "allocation_generation")},
		HooksDigest: plan.digest(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.BeginReleasePhase(op.OperationID, registry.PhasePreHook, op.Revision); err != nil {
		t.Fatal(err)
	}
	started, err := reg.GetRelease(op.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	// Inherited Git redirection must not affect the bound identity check.
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	calls := 0
	a := releaseHooksAdapter{Registry: reg, RunHook: func(context.Context, string, string) checkedHookOutcome {
		calls++
		if calls != 1 {
			t.Fatal("dispatched after Git association changed")
		}
		if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+filepath.Join(other, ".git")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return hookSettledOK
	}}
	if got := a.Run(context.Background(), *started, registry.PhasePreHook); got.State != "uncertain" || calls != 1 {
		t.Fatalf("association change: %+v calls %d", got, calls)
	}
}
