package registry

import (
	"bytes"
	"encoding/json"
	"os"
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
