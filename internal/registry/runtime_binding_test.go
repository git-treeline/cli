package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestReleaseCompletionBindingsAreImmutable(t *testing.T) {
	reg, request := enrolledRequest(t)
	request.Policy.DropDatabases = true
	for _, digest := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		invalid := request
		invalid.DatabaseConfigRevision = digest
		before, _ := os.ReadFile(reg.Path)
		if _, _, err := reg.PrepareRelease(invalid); err == nil {
			t.Fatal("invalid database digest accepted")
		}
		after, _ := os.ReadFile(reg.Path)
		if !bytes.Equal(before, after) {
			t.Fatal("rejected request rewrote registry")
		}
	}
	request.DatabaseConfigRevision = strings.Repeat("a", 64)
	if _, _, err := reg.PrepareRelease(request); err != nil {
		t.Fatal(err)
	}
	reopened := New(reg.Path)
	retained := operation(t, reopened, request.OperationID)
	if !sameJSON(retained.ReleaseRequest, request) {
		t.Fatal("bindings lost after restart")
	}
	for _, mutate := range []func(*ReleaseRequest){
		func(r *ReleaseRequest) { r.DatabaseConfigRevision = strings.Repeat("b", 64) },
		func(r *ReleaseRequest) {
			r.RuntimeBinding = &RuntimeBinding{Kind: "supervisor", Generation: uuid.NewString()}
		},
		func(r *ReleaseRequest) { r.RuntimeBinding = nil },
		func(r *ReleaseRequest) { r.Policy.DropDatabases = false },
	} {
		changed := request
		mutate(&changed)
		if _, _, err := reopened.PrepareRelease(changed); err == nil {
			t.Fatal("binding replacement accepted")
		}
	}
	if _, existing, err := reopened.PrepareRelease(request); err != nil || !existing {
		t.Fatalf("exact replay: %v", err)
	}
}

func TestRuntimeLegacyAbsenceNeverGrantsFreshness(t *testing.T) {
	reg, target, legacy := releaseRegistryFixture(t)
	binding, err := AllocationRuntimeBinding(legacy)
	if err != nil || binding.Kind != "unknown" {
		t.Fatal("legacy absence invented freshness", err)
	}
	enrolled, err := reg.EnrollAllocation(target, legacy)
	if err != nil {
		t.Fatal(err)
	}
	binding, err = AllocationRuntimeBinding(enrolled)
	if err != nil || binding.Kind != "unknown" {
		t.Fatal("development enrollment adopted runtime", err)
	}
	request := ReleaseRequest{OperationID: uuid.NewString(), Target: target, Allocation: AllocationRef{Kind: "allocated", Generation: GetString(enrolled, "allocation_generation")}, HooksDigest: strings.Repeat("a", 64), RuntimeBinding: &binding}
	if _, _, err := reg.PrepareRelease(request); err == nil {
		t.Fatal("unknown runtime released")
	}
	request.RuntimeBinding = &RuntimeBinding{Kind: "never_started"}
	if _, _, err := reg.PrepareRelease(request); err == nil {
		t.Fatal("invented fresh request released")
	}
	_, dir := lifecycleFixture(t)
	locks := acquireForTest(t, dir, target.Path)
	if _, err := reg.RegisterRuntime(locks, target, request.Allocation.Generation, binding, uuid.NewString()); err == nil {
		t.Fatal("unknown owner adopted")
	}
	if _, err := reg.EnrollFreshAllocation(locks, target, Allocation{}); err == nil {
		t.Fatal("existing allocation relabeled fresh")
	}
	if err := reg.UpdateField(target.Path, "runtime_binding", "never_started"); err == nil {
		t.Fatal("runtime identity publicly mutable")
	}
	if err := reg.Allocate(Allocation{"worktree": target.Path, "runtime_binding": RuntimeBinding{Kind: "never_started"}}); err != nil {
		t.Fatal(err)
	}
	actual, err := AllocationRuntimeBinding(reg.Find(target.Path))
	if err != nil || actual.Kind != "unknown" {
		t.Fatal("ordinary allocator forged runtime identity", err)
	}
}

func TestRuntimeRegistrationIsLockedExactAndDurable(t *testing.T) {
	reg, request := enrolledRequest(t)
	_, dir := lifecycleFixture(t)
	locks := acquireForTest(t, dir, request.Target.Path)
	fresh := *request.RuntimeBinding
	generation := uuid.NewString()
	if _, err := reg.RegisterRuntime(nil, request.Target, request.Allocation.Generation, fresh, generation); err == nil {
		t.Fatal("unlocked registration accepted")
	}
	if _, err := reg.RegisterRuntime(locks, request.Target, uuid.NewString(), fresh, generation); err == nil {
		t.Fatal("replacement enrollment accepted")
	}
	allocation, err := reg.RegisterRuntime(locks, request.Target, request.Allocation.Generation, fresh, generation)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := AllocationRuntimeBinding(allocation)
	if err != nil || binding != (RuntimeBinding{Kind: "supervisor", Generation: generation}) {
		t.Fatal("wrong registration", err)
	}
	reopened := New(reg.Path)
	retained, err := reopened.FindForRelease(request.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := AllocationRuntimeBinding(retained)
	if err != nil || actual != binding {
		t.Fatal("interrupted start lost binding", err)
	}
	if _, err := reopened.RegisterRuntime(locks, request.Target, request.Allocation.Generation, fresh, uuid.NewString()); err == nil {
		t.Fatal("interrupted start replaced by fresh retry")
	}
	if _, err := reopened.RegisterRuntime(locks, request.Target, request.Allocation.Generation, fresh, generation); err != nil {
		t.Fatal("exact registration observation rejected", err)
	}
	if _, _, err := reopened.PrepareRelease(request); err == nil {
		t.Fatal("stale runtime release accepted")
	}
	request.RuntimeBinding = &binding
	if _, _, err := reopened.PrepareRelease(request); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RegisterRuntime(locks, request.Target, request.Allocation.Generation, binding, uuid.NewString()); err == nil {
		t.Fatal("release reservation bypassed")
	}
	if err := locks.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RegisterRuntime(locks, request.Target, request.Allocation.Generation, binding, generation); err == nil {
		t.Fatal("closed lifecycle lock accepted")
	}
}

func TestSchemaThreeHistoryReadOnlyWithoutInventedBindings(t *testing.T) {
	reg, request := enrolledRequest(t)
	if _, _, err := reg.PrepareRelease(request); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(reg.Path)
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	data["version"] = 3
	delete(data["allocations"].([]any)[0].(map[string]any), "runtime_binding")
	delete(data["release_operations"].([]any)[0].(map[string]any), "runtime_binding")
	raw, _ = json.Marshal(data)
	if err := os.WriteFile(reg.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	historical, err := reg.GetRelease(request.OperationID)
	if err != nil || historical == nil || historical.RuntimeBinding != nil {
		t.Fatal("history unavailable or synthesized", err)
	}
	if _, err := reg.ListReleases(); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.BeginReleasePhase(request.OperationID, PhasePreHook, historical.Revision); err == nil {
		t.Fatal("legacy operation dispatched")
	}
	if _, _, err := reg.PrepareRelease(historical.ReleaseRequest); err == nil {
		t.Fatal("legacy request resumed")
	}
	after, _ := os.ReadFile(reg.Path)
	if !bytes.Equal(raw, after) {
		t.Fatal("legacy query/refusal rewrote evidence")
	}
}

func TestMalformedRuntimeBindingRefuses(t *testing.T) {
	for _, value := range []any{nil, "never_started", map[string]any{}, map[string]any{"kind": "never_started", "generation": ""}, RuntimeBinding{Kind: "never_started", Generation: uuid.NewString()}, RuntimeBinding{Kind: "supervisor"}, map[string]any{"kind": "unknown", "extra": true}} {
		if _, err := AllocationRuntimeBinding(Allocation{"runtime_binding": value}); err == nil {
			t.Fatalf("accepted malformed binding %#v", value)
		}
	}
}

func TestFreshAllocationTxUsesCurrentResourcesAndRefusesAdoption(t *testing.T) {
	root, dir := lifecycleFixture(t)
	target, err := CaptureReleaseTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	locks := acquireForTest(t, dir, root)
	reg := New(filepath.Join(t.TempDir(), "registry.json"))
	if err := reg.Allocate(Allocation{"worktree": filepath.Join(root, "other"), "port": 4100, "redis_db": 2}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	entry, err := reg.EnrollFreshAllocationTx(locks, target, func(used UsedResources) (Allocation, error) {
		calls++
		if !slices.Equal(used.Ports, []int{4100}) || !slices.Equal(used.RedisDbs, []int{2}) {
			t.Fatalf("stale resources: %+v", used)
		}
		return Allocation{"port": 4101, "redis_db": 3}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := AllocationRuntimeBinding(entry)
	if err != nil || binding.Kind != "never_started" || GetString(entry, "allocated_at") == "" {
		t.Fatal("fresh allocation missing retained identity", err)
	}
	before, _ := os.ReadFile(reg.Path)
	if _, err := reg.EnrollFreshAllocationTx(locks, target, func(UsedResources) (Allocation, error) { calls++; return Allocation{}, nil }); err == nil || calls != 1 {
		t.Fatal("existing allocation computed or adopted")
	}
	after, _ := os.ReadFile(reg.Path)
	if string(before) != string(after) {
		t.Fatal("refused adoption changed registry")
	}
}

func TestFreshAllocationTxFailureNeverEnrollsOrMigrates(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			root, dir := lifecycleFixture(t)
			target, err := CaptureReleaseTarget(root)
			if err != nil {
				t.Fatal(err)
			}
			locks := acquireForTest(t, dir, root)
			reg := New(filepath.Join(t.TempDir(), "registry.json"))
			if legacy {
				if err := os.WriteFile(reg.Path, []byte(`{"version":3,"allocations":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(reg.Path)
			calls := 0
			if _, err := reg.EnrollFreshAllocationTx(locks, target, func(UsedResources) (Allocation, error) {
				calls++
				return nil, fmt.Errorf("injected computation failure")
			}); err == nil {
				t.Fatal("failure accepted")
			}
			if legacy && calls != 0 {
				t.Fatal("legacy store computed a fresh allocation")
			}
			after, _ := os.ReadFile(reg.Path)
			if string(before) != string(after) {
				t.Fatal("failed computation changed store")
			}
		})
	}
}

func TestCaptureReleaseTargetUsesGitIdentityAndIgnoresRedirection(t *testing.T) {
	_, _ = lifecycleFixture(t)
	main, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	releaseFixtureGit(t, main, "init", "-q")
	releaseFixtureGit(t, main, "commit", "--allow-empty", "-qm", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	releaseFixtureGit(t, main, "worktree", "add", "-qb", "linked", linked)
	linked, err = filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	target, err := CaptureReleaseTarget(linked)
	if err != nil || target.Path != linked || target.Common == nil || target.Common.Path != filepath.Join(main, ".git") {
		t.Fatalf("identity=%+v err=%v", target, err)
	}
	if err := os.Mkdir(filepath.Join(linked, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureReleaseTarget(filepath.Join(linked, "nested")); err == nil {
		t.Fatal("nested path accepted as worktree root")
	}
	if _, err := CaptureReleaseTarget(filepath.Join(linked, "missing")); err == nil {
		t.Fatal("missing target accepted")
	}
}
