package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	oldregistry "github.com/git-treeline/cli/internal/registry/testdata/schema2"
	"github.com/google/uuid"
)

func releaseRegistryFixture(t *testing.T) (*Registry, TargetIdentity, Allocation) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if strings.HasPrefix(k, "GIT_") {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := directoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := New(filepath.Join(t.TempDir(), "registry.json"))
	legacy := []byte(fmt.Sprintf(`{"version":2,"allocations":[{"worktree":%q,"project":"fixture","port":3010,"database":"fixture_db","database_adapter":"sqlite","env":{"secret":"never-copy-this"}}]}`, root))
	if err := os.WriteFile(reg.Path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := reg.FindForRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	return reg, TargetIdentity{Path: root, Directory: id}, a
}
func enrolledRequest(t *testing.T) (*Registry, ReleaseRequest) {
	t.Helper()
	reg, target, legacy := releaseRegistryFixture(t)
	a, err := reg.EnrollAllocation(target, legacy)
	if err != nil {
		t.Fatal(err)
	}
	return reg, ReleaseRequest{OperationID: uuid.NewString(), Target: target, Allocation: AllocationRef{Kind: "allocated", Generation: GetString(a, "allocation_generation")}, HooksDigest: strings.Repeat("0", 64)}
}
func operation(t *testing.T, r *Registry, id string) ReleaseOperation {
	t.Helper()
	o, e := r.GetRelease(id)
	if e != nil || o == nil {
		t.Fatalf("query: %v", e)
	}
	return *o
}
func begin(t *testing.T, r *Registry, id string, p ReleasePhase) PhaseToken {
	t.Helper()
	o := operation(t, r, id)
	token, e := r.BeginReleasePhase(id, p, o.Revision)
	if e != nil {
		t.Fatal(e)
	}
	return token
}
func complete(t *testing.T, r *Registry, id string, p ReleasePhase) {
	t.Helper()
	token := begin(t, r, id, p)
	var e error
	if p == PhaseAllocation {
		_, e = r.RetireReleaseAllocation(token)
	} else {
		_, e = r.RecordReleasePhase(token, PhaseResult{"completed", "ok"})
	}
	if e != nil {
		t.Fatal(e)
	}
}

func TestReleaseRegistryLegacyEnrollmentAndGenerations(t *testing.T) {
	reg, target, legacy := releaseRegistryFixture(t)
	before, _ := os.ReadFile(reg.Path)
	if o, e := reg.GetRelease(uuid.NewString()); e != nil || o != nil {
		t.Fatal(e)
	}
	if _, e := reg.ListReleases(); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(reg.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("readonly query migrated legacy")
	}
	if _, e := reg.AllocateTx(target.Path, func(UsedResources) (Allocation, error) { return nil, fmt.Errorf("fixture abort") }); e == nil {
		t.Fatal("failed transaction accepted")
	}
	after, _ = os.ReadFile(reg.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed transaction enrolled legacy")
	}
	a, e := reg.EnrollAllocation(target, legacy)
	if e != nil {
		t.Fatal(e)
	}
	generation := GetString(a, "allocation_generation")
	if !canonicalUUID(generation) {
		t.Fatal("missing generation")
	}
	if e := reg.Allocate(Allocation{"worktree": target.Path, "project": "updated"}); e != nil {
		t.Fatal(e)
	}
	if g := GetString(reg.Find(target.Path), "allocation_generation"); g != generation {
		t.Fatal("update rotated generation")
	}
	if e := reg.UpdateField(target.Path, "allocation_generation", uuid.NewString()); e == nil {
		t.Fatal("generation publicly mutable")
	}
	if _, e := reg.Release(target.Path); e != nil {
		t.Fatal(e)
	}
	if e := reg.Allocate(Allocation{"worktree": target.Path}); e != nil {
		t.Fatal(e)
	}
	if GetString(reg.Find(target.Path), "allocation_generation") == generation {
		t.Fatal("new enrollment reused generation")
	}
}

func TestReleaseRegistryBindingAndReservation(t *testing.T) {
	reg, request := enrolledRequest(t)
	o, existing, e := reg.PrepareRelease(request)
	if e != nil || existing {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(o)
	if bytes.Contains(raw, []byte("never-copy-this")) {
		t.Fatal("snapshot copied arbitrary metadata")
	}
	if _, existing, e := reg.PrepareRelease(request); e != nil || !existing {
		t.Fatal("same UUID did not observe", e)
	}
	for _, change := range []func(*ReleaseRequest){func(r *ReleaseRequest) { r.Policy.DropDatabases = true }, func(r *ReleaseRequest) { r.Allocation.Generation = uuid.NewString() }, func(r *ReleaseRequest) { r.HooksDigest = strings.Repeat("1", 64) }, func(r *ReleaseRequest) { r.OperationID = strings.ToUpper(r.OperationID) }} {
		changed := request
		change(&changed)
		if _, _, e := reg.PrepareRelease(changed); e == nil {
			t.Fatal("changed binding accepted")
		}
	}
	called := false
	if _, e := reg.AllocateTx(request.Target.Path, func(UsedResources) (Allocation, error) { called = true; return Allocation{}, nil }); e == nil || called {
		t.Fatal("reserved allocation compute ran")
	}
	for _, write := range []func() error{func() error { return reg.Allocate(Allocation{"worktree": request.Target.Path}) }, func() error { return reg.UpdateField(request.Target.Path, "project", "changed") }, func() error { _, e := reg.Release(request.Target.Path); return e }, func() error { _, e := reg.ReleaseMany([]string{request.Target.Path}); return e }, func() error { return reg.SetLink(request.Target.Path, "other", "branch") }} {
		if e := write(); e == nil {
			t.Fatal("reservation mutation accepted")
		}
	}
	other := t.TempDir()
	if e := reg.Allocate(Allocation{"worktree": other}); e != nil {
		t.Fatal("unrelated enrollment blocked", e)
	}
	o.Snapshot.Project = "mutated"
	o.Phases[0].State = "completed"
	if got := operation(t, reg, request.OperationID); got.Snapshot.Project == "mutated" || got.Phases[0].State != "pending" {
		t.Fatal("query alias leaked")
	}
}

func TestReleaseRegistryConcurrentBeginAndInterruptedOutcome(t *testing.T) {
	reg, request := enrolledRequest(t)
	o, _, e := reg.PrepareRelease(request)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	tokens := make(chan PhaseToken, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, e := New(reg.Path).BeginReleasePhase(request.OperationID, PhasePreHook, o.Revision)
			if e == nil {
				tokens <- token
			}
		}()
	}
	wg.Wait()
	close(tokens)
	var token PhaseToken
	count := 0
	for t := range tokens {
		token = t
		count++
	}
	if count != 1 {
		t.Fatalf("dispatches %d", count)
	}
	before, _ := os.ReadFile(reg.Path)
	reopened := New(reg.Path)
	observed := operation(t, reopened, request.OperationID)
	if observed.Outcome() != "uncertain" || observed.Phases[0].State != "started" {
		t.Fatal("lost started outcome inferred complete")
	}
	after, _ := os.ReadFile(reg.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("recovery query wrote evidence")
	}
	if _, e := reopened.BeginReleasePhase(request.OperationID, PhasePreHook, observed.Revision); e == nil {
		t.Fatal("interrupted hook replayed")
	}
	if _, e := reopened.RecordReleasePhase(token, PhaseResult{"uncertain", "unknown"}); e != nil {
		t.Fatal(e)
	}
	if _, e := reopened.RecordReleasePhase(token, PhaseResult{"completed", "ok"}); e == nil {
		t.Fatal("uncertain hook fabricated completion")
	}
}

func TestReleaseRegistryOrderedRetirementAndFinalReservation(t *testing.T) {
	reg, request := enrolledRequest(t)
	o, _, e := reg.PrepareRelease(request)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := reg.BeginReleasePhase(request.OperationID, PhaseHosts, o.Revision); e == nil {
		t.Fatal("phase order bypassed")
	}
	token := begin(t, reg, request.OperationID, PhasePreHook)
	if _, e := reg.RecordReleasePhase(token, PhaseResult{"blocked", "failed"}); e != nil {
		t.Fatal(e)
	}
	token = begin(t, reg, request.OperationID, PhasePreHook)
	if _, e := reg.RecordReleasePhase(token, PhaseResult{"completed", "ok"}); e != nil {
		t.Fatal(e)
	}
	complete(t, reg, request.OperationID, PhaseSupervisor)
	token = begin(t, reg, request.OperationID, PhaseAllocation)
	if _, e := reg.RecordReleasePhase(token, PhaseResult{"completed", "ok"}); e == nil {
		t.Fatal("retirement completed without atomic allocation change")
	}
	if _, e := reg.RetireReleaseAllocation(token); e != nil {
		t.Fatal(e)
	}
	if reg.Find(request.Target.Path) != nil {
		t.Fatal("allocation not retired")
	}
	if e := reg.Allocate(Allocation{"worktree": request.Target.Path}); e == nil {
		t.Fatal("reservation ended before hosts/post")
	}
	complete(t, reg, request.OperationID, PhaseHosts)
	complete(t, reg, request.OperationID, PhasePostHook)
	if operation(t, reg, request.OperationID).Outcome() != "completed" {
		t.Fatal("completion absent")
	}
	if e := reg.Allocate(Allocation{"worktree": request.Target.Path}); e != nil {
		t.Fatal(e)
	}
	if GetString(reg.Find(request.Target.Path), "allocation_generation") == request.Allocation.Generation {
		t.Fatal("reallocation reused enrollment")
	}
	if _, existing, e := reg.PrepareRelease(request); e != nil || !existing {
		t.Fatal("completed receipt not queryable", e)
	}
}

func TestReleaseRegistryReplacementAndMissingPathNeverComplete(t *testing.T) {
	reg, request := enrolledRequest(t)
	o, _, e := reg.PrepareRelease(request)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(request.Target.Path, request.Target.Path+"-old"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(request.Target.Path + "-old") })
	if e := os.Mkdir(request.Target.Path, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := reg.BeginReleasePhase(request.OperationID, PhasePreHook, o.Revision); e == nil {
		t.Fatal("replaced target acted on")
	}
	if operation(t, reg, request.OperationID).Outcome() != "prepared" {
		t.Fatal("query changed replaced target receipt")
	}
	if e := os.Remove(request.Target.Path); e != nil {
		t.Fatal(e)
	}
	if _, e := reg.BeginReleasePhase(request.OperationID, PhasePreHook, o.Revision); e == nil {
		t.Fatal("missing directory inferred completion")
	}
}

func TestReleaseRegistryUnallocatedGitOnlyOperation(t *testing.T) {
	reg, _, _ := releaseRegistryFixture(t)
	base := t.TempDir()
	git := func(root string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("Git: %s %v", b, e)
		}
	}
	git(base, "init", "-q")
	git(base, "commit", "--allow-empty", "-qm", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	git(base, "worktree", "add", "-qb", "linked", linked)
	linked, _ = filepath.EvalSymlinks(linked)
	base, _ = filepath.EvalSymlinks(base)
	id, _ := directoryIdentity(linked)
	common := filepath.Join(base, ".git")
	ci, _ := directoryIdentity(common)
	request := ReleaseRequest{OperationID: uuid.NewString(), Target: TargetIdentity{Path: linked, Directory: id, Common: &CommonIdentity{common, ci}}, Allocation: AllocationRef{Kind: "unallocated"}, Policy: ReleasePolicy{RemoveWorktree: true}, HooksDigest: strings.Repeat("0", 64)}
	bad := request
	bad.Policy.DropDatabases = true
	if _, _, e := reg.PrepareRelease(bad); e == nil {
		t.Fatal("unallocated database policy accepted")
	}
	o, _, e := reg.PrepareRelease(request)
	if e != nil {
		t.Fatal(e)
	}
	if len(o.Phases) != 1 || o.Phases[0].Name != PhaseWorktree || o.Snapshot != nil {
		t.Fatal("invented unallocated effects")
	}
	token := begin(t, reg, request.OperationID, PhaseWorktree)
	if _, e := reg.RecordReleasePhase(token, PhaseResult{"completed", "ok"}); e == nil {
		t.Fatal("existing directory counted as removed")
	}
	git(base, "worktree", "remove", linked)
	if _, e := reg.RecordReleasePhase(token, PhaseResult{"completed", "ok"}); e != nil {
		t.Fatal(e)
	}
	if operation(t, reg, request.OperationID).Outcome() != "completed" {
		t.Fatal("known removal incomplete")
	}
}

func TestReleaseRegistryMalformedEvidenceRefusesWithoutRewrite(t *testing.T) {
	for _, damage := range []string{"missing generation", "duplicate generation", "out of order", "unknown snapshot field", "missing policy", "started missing attempt", "retired enrollment present"} {
		t.Run(damage, func(t *testing.T) {
			reg, request := enrolledRequest(t)
			if _, _, e := reg.PrepareRelease(request); e != nil {
				t.Fatal(e)
			}
			raw, _ := os.ReadFile(reg.Path)
			var data map[string]any
			_ = json.Unmarshal(raw, &data)
			allocations := data["allocations"].([]any)
			op := data["release_operations"].([]any)[0].(map[string]any)
			phases := op["phases"].([]any)
			switch damage {
			case "missing generation":
				delete(allocations[0].(map[string]any), "allocation_generation")
			case "duplicate generation":
				a := map[string]any{"worktree": filepath.Join(t.TempDir(), "other"), "allocation_generation": request.Allocation.Generation}
				data["allocations"] = append(allocations, a)
			case "out of order":
				phases[1].(map[string]any)["name"] = "managed_hosts"
			case "unknown snapshot field":
				op["snapshot"].(map[string]any)["env"] = "must-not-copy"
			case "missing policy":
				delete(op["policy"].(map[string]any), "force_dirty")
			case "started missing attempt":
				phases[0].(map[string]any)["state"] = "started"
			case "retired enrollment present":
				for _, p := range phases {
					v := p.(map[string]any)
					v["state"] = "completed"
					v["attempt"] = uuid.NewString()
					v["code"] = "ok"
				}
			}
			before, _ := json.Marshal(data)
			if e := os.WriteFile(reg.Path, before, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := reg.GetRelease(request.OperationID); e == nil {
				t.Fatal("malformed evidence accepted")
			}
			if e := reg.Allocate(Allocation{"worktree": t.TempDir()}); e == nil {
				t.Fatal("malformed evidence overwritten")
			}
			after, _ := os.ReadFile(reg.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal rewrote evidence")
			}
		})
	}
}

func TestReleaseRegistryCapacityNeverEvictsUnresolved(t *testing.T) {
	reg, request := enrolledRequest(t)
	first, _, e := reg.PrepareRelease(request)
	if e != nil {
		t.Fatal(e)
	}
	data, e := reg.readReleaseStore()
	if e != nil {
		t.Fatal(e)
	}
	for len(data.ReleaseOperations) < maxReleaseOperations {
		o := cloneOperation(first)
		o.OperationID = uuid.NewString()
		o.Allocation.Generation = uuid.NewString()
		for i := range o.Phases {
			o.Phases[i].State = "completed"
			o.Phases[i].Code = "ok"
			o.Phases[i].Attempt = uuid.NewString()
		}
		data.ReleaseOperations = append(data.ReleaseOperations, o)
	}
	raw, _ := json.Marshal(data)
	if e := os.WriteFile(reg.Path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	next := request
	next.OperationID = uuid.NewString()
	if _, _, e := reg.PrepareRelease(next); e == nil || !strings.Contains(e.Error(), "capacity") {
		t.Fatalf("capacity refusal missing: %v", e)
	}
	records, e := reg.ListReleases()
	if e != nil || len(records) != maxReleaseOperations {
		t.Fatal("capacity dropped records", e)
	}
	if operation(t, reg, request.OperationID).Outcome() != "prepared" {
		t.Fatal("unresolved record evicted")
	}
	after, _ := os.ReadFile(reg.Path)
	if !bytes.Equal(raw, after) {
		t.Fatal("capacity refusal rewrote store")
	}
}

func TestReleaseRegistryPriorReaderAndWriterCompatibility(t *testing.T) {
	source, e := os.ReadFile("testdata/schema2/registry.go")
	if e != nil {
		t.Fatal(e)
	}
	if fmt.Sprintf("%x", sha256.Sum256(source)) != "eed8ca25243098a200ab7ebdab1c7c2edced02d86e1fe5c9eaa20fa6f2ca20c4" {
		t.Fatal("historical source changed")
	}
	reg, request := enrolledRequest(t)
	if _, _, e := reg.PrepareRelease(request); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(reg.Path)
	old := oldregistry.New(reg.Path)
	if a, e := old.FindStrict(request.Target.Path); e != nil || a == nil {
		t.Fatal("older read-only allocation view failed", e)
	}
	if _, e := old.ReleasePreflight(); e == nil {
		t.Fatal("prior release accepted future schema")
	}
	if e := old.Allocate(oldregistry.Allocation{"worktree": request.Target.Path}); e == nil {
		t.Fatal("old writer accepted schema3")
	}
	after, _ := os.ReadFile(reg.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("old writer changed new evidence")
	}
}

func TestReleaseRegistryIgnoredHookOutcomeRequiresExplicitPolicy(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run(fmt.Sprint(ignore), func(t *testing.T) {
			reg, request := enrolledRequest(t)
			request.Policy.IgnoreHookFailure = ignore
			if _, _, e := reg.PrepareRelease(request); e != nil {
				t.Fatal(e)
			}
			token := begin(t, reg, request.OperationID, PhasePreHook)
			_, e := reg.RecordReleasePhase(token, PhaseResult{"completed", "ignored_hook_failure"})
			if (e == nil) != ignore {
				t.Fatalf("ignored failure policy: %v", e)
			}
			if ignore {
				token = begin(t, reg, request.OperationID, PhaseSupervisor)
				if _, e := reg.RecordReleasePhase(token, PhaseResult{"completed", "ignored_hook_failure"}); e == nil {
					t.Fatal("ignored runtime failure accepted")
				}
			}
		})
	}
}

func TestReleaseRegistryFailedBeginCommitGrantsNoToken(t *testing.T) {
	reg, request := enrolledRequest(t)
	o, _, e := reg.PrepareRelease(request)
	if e != nil {
		t.Fatal(e)
	}
	data, e := reg.readReleaseStore()
	if e != nil {
		t.Fatal(e)
	}
	data.Allocations[0]["padding"] = ""
	raw, _ := json.Marshal(data)
	data.Allocations[0]["padding"] = strings.Repeat("x", maxReleaseStoreBytes-len(raw))
	before, _ := json.Marshal(data)
	if len(before) != maxReleaseStoreBytes {
		t.Fatal("invalid capacity fixture")
	}
	if e := os.WriteFile(reg.Path, before, 0600); e != nil {
		t.Fatal(e)
	}
	token, e := reg.BeginReleasePhase(request.OperationID, PhasePreHook, o.Revision)
	if e == nil || token != (PhaseToken{}) {
		t.Fatalf("failed commit granted dispatch: %+v %v", token, e)
	}
	after, _ := os.ReadFile(reg.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed begin changed evidence")
	}
	if operation(t, reg, request.OperationID).Phases[0].State != "pending" {
		t.Fatal("failed begin recorded dispatch")
	}
}

func TestReleaseRegistryDuplicateOperationKeysRefuse(t *testing.T) {
	for _, key := range []string{"operation_id", "force_dirty", "path"} {
		t.Run(key, func(t *testing.T) {
			reg, request := enrolledRequest(t)
			if _, _, e := reg.PrepareRelease(request); e != nil {
				t.Fatal(e)
			}
			data, e := reg.readReleaseStore()
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(data)
			var field string
			switch key {
			case "operation_id":
				field = fmt.Sprintf(`"operation_id":%q`, request.OperationID)
			case "force_dirty":
				field = `"force_dirty":false`
			case "path":
				field = fmt.Sprintf(`"path":%q`, request.Target.Path)
			}
			damaged := []byte(strings.Replace(string(raw), field, field+","+field, 1))
			if bytes.Equal(raw, damaged) {
				t.Fatal("duplicate fixture missing")
			}
			if e := os.WriteFile(reg.Path, damaged, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := reg.GetRelease(request.OperationID); e == nil {
				t.Fatal("duplicate JSON key accepted")
			}
			after, _ := os.ReadFile(reg.Path)
			if !bytes.Equal(damaged, after) {
				t.Fatal("query changed evidence")
			}
		})
	}
}

func TestReleaseRegistryRejectsChangedEnrolledSnapshot(t *testing.T) {
	for _, field := range []string{"worktree", "port", "project"} {
		t.Run(field, func(t *testing.T) {
			reg, request := enrolledRequest(t)
			if _, _, e := reg.PrepareRelease(request); e != nil {
				t.Fatal(e)
			}
			data, e := reg.readReleaseStore()
			if e != nil {
				t.Fatal(e)
			}
			switch field {
			case "worktree":
				data.Allocations[0][field] = t.TempDir()
			case "port":
				data.Allocations[0][field] = float64(4001)
			case "project":
				data.Allocations[0][field] = "changed"
			}
			before, _ := json.Marshal(data)
			if e := os.WriteFile(reg.Path, before, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := reg.GetRelease(request.OperationID); e == nil {
				t.Fatal("changed enrolled binding accepted")
			}
			if _, e := reg.BeginReleasePhase(request.OperationID, PhasePreHook, 1); e == nil {
				t.Fatal("changed enrollment dispatched")
			}
			after, _ := os.ReadFile(reg.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal changed evidence")
			}
		})
	}
}

func TestReleaseRegistryCleanupSnapshotRejectsLossyFields(t *testing.T) {
	for key, value := range map[string]any{"port": 1.5, "ports": []any{float64(3010), 1.5}, "redis_db": 1.5, "databases": []any{"good", float64(2)}, "database_adapter": float64(2), "main_worktree": "true"} {
		t.Run(key, func(t *testing.T) {
			reg, request := enrolledRequest(t)
			data, e := reg.readReleaseStore()
			if e != nil {
				t.Fatal(e)
			}
			data.Allocations[0][key] = value
			before, _ := json.Marshal(data)
			if e := os.WriteFile(reg.Path, before, 0600); e != nil {
				t.Fatal(e)
			}
			if o, _, e := reg.PrepareRelease(request); e == nil || o.OperationID != "" {
				t.Fatalf("lossy snapshot granted operation: %+v %v", o, e)
			}
			after, _ := os.ReadFile(reg.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal changed evidence")
			}
		})
	}
	reg, request := enrolledRequest(t)
	data, e := reg.readReleaseStore()
	if e != nil {
		t.Fatal(e)
	}
	a := data.Allocations[0]
	a["main_worktree"] = true
	a["ports"] = []any{float64(3010), float64(3011)}
	a["databases"] = []any{"primary", "auxiliary"}
	a["redis_db"] = float64(2)
	raw, _ := json.Marshal(data)
	if e := os.WriteFile(reg.Path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	o, _, e := reg.PrepareRelease(request)
	if e != nil {
		t.Fatal(e)
	}
	if !o.Snapshot.MainWorktree || len(o.Snapshot.Ports) != 2 || len(o.Snapshot.Databases) != 2 || *o.Snapshot.RedisDB != 2 {
		t.Fatal("genuine cleanup metadata lost")
	}
}
