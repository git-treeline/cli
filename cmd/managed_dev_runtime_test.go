package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/supervisor"
)

func managedDevRuntimeFixture(t *testing.T) (string, *registry.Registry, registry.Allocation) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	normal := filepath.Join(home, "Library", "Application Support", "git-treeline")
	private := filepath.Join(home, ".treeline-dev", "gtl")
	root := filepath.Join(home, ".treeline-dev", "workspaces", "fixture")
	for _, dir := range []string{normal, private, root} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	normalConfig := `{"port":{"increment":10,"reservations":{"@treeline-dev-qa/slot-01":42000,"@treeline-dev-qa/slot-02":42010,"@treeline-dev-qa/slot-03":42020,"@treeline-dev-qa/slot-04":42030}}}`
	privateConfig := `{"port":{"base":42000,"increment":10,"reservations":{"fixture":42000}},"router":{"mode":"disabled"}}`
	for path, data := range map[string]string{
		filepath.Join(normal, "config.json"):   normalConfig,
		filepath.Join(normal, "registry.json"): `{"version":4,"allocations":[],"edges":[],"release_operations":[]}`,
		filepath.Join(private, "config.json"):  privateConfig,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: fixture\ncommands:\n  start: fixture-http-server\n"), 0600); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "add", config.ProjectConfigFile)
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	commit = exec.Command("git", "-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "fixture")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	t.Setenv("HOME", home)
	t.Setenv("GTL_HOME", private)
	t.Setenv("TREELINE_MANAGED", "app")
	t.Setenv("TREELINE_CHANNEL", "dev")
	reg := registry.New("")
	allocation := registry.Allocation{"worktree": root, "project": "fixture", "branch": "main", "main_worktree": true, "port": 42000, "ports": []int{42000}}
	target, err := registry.CaptureReleaseTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	commandFreshEnrollment(t, reg, target, allocation)
	stored, err := reg.FindForRelease(root)
	if err != nil || stored == nil {
		t.Fatal("managed allocation unavailable", err)
	}
	if binding, err := registry.AllocationRuntimeBinding(stored); err != nil || binding.Kind != "never_started" {
		t.Fatalf("runtime binding = %+v, %v; allocation=%#v", binding, err, stored)
	}
	return root, reg, stored
}

func TestManagedDevTrackedRuntimeValidatesAgainAtChildStart(t *testing.T) {
	root, reg, _ := managedDevRuntimeFixture(t)
	oldProbe := managedDevPortAvailable
	managedDevPortAvailable = func(port int) error {
		if port != 42000 {
			t.Fatalf("probed port %d", port)
		}
		return nil
	}
	t.Cleanup(func() { managedDevPortAvailable = oldProbe })
	dir, err := os.MkdirTemp("/tmp", "gtl-managed-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store := runtimeowner.New(filepath.Join(dir, "outcomes.json"))
	effects := runtimeLaunchEffects{
		store: func(string) *runtimeowner.Store { return store },
		observe: func(int) (runtimeowner.Owner, error) {
			return runtimeowner.ObserveOwner(os.Getpid())
		},
		hook: func(context.Context, string, string) checkedHookOutcome { return hookSettledOK },
		run: func(sv *supervisor.Supervisor) error {
			if sv.BeforeChildStart == nil {
				t.Fatal("final child admission missing")
			}
			return sv.BeforeChildStart(sv.Port)
		},
	}
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err := runTrackedStartWith(context.Background(), root, locks, "", false, effects); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.FindForRelease(root); err != nil {
		t.Fatal(err)
	}
}

func TestManagedDevLinkedAllocationMayOmitMainWorktreeFlag(t *testing.T) {
	root, _, allocation := managedDevRuntimeFixture(t)
	if output, err := exec.Command("git", "-C", root, "checkout", "-b", "qa-one").CombinedOutput(); err != nil {
		t.Fatalf("fixture checkout: %v %s", err, output)
	}
	privateConfig := filepath.Join(os.Getenv("GTL_HOME"), "config.json")
	if err := os.WriteFile(privateConfig, []byte(`{"port":{"increment":10,"reservations":{"fixture":42000,"fixture/qa-one":42010}},"router":{"mode":"disabled"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	delete(allocation, "main_worktree")
	allocation["branch"] = "qa-one"
	allocation["port"], allocation["ports"] = float64(42010), []any{float64(42010)}
	if err := validateManagedDevRuntime(root, allocation, false); err != nil {
		t.Fatalf("ordinary non-main allocation refused: %v", err)
	}
}

func TestManagedDevTrackedRuntimeDriftRefusesBeforeHooks(t *testing.T) {
	root, reg, allocation := managedDevRuntimeFixture(t)
	allocation["port"], allocation["ports"] = 43000, []int{43000}
	if err := reg.Allocate(allocation); err != nil {
		t.Fatal(err)
	}
	hooks := 0
	effects := runtimeLaunchEffects{
		store:   func(string) *runtimeowner.Store { return runtimeowner.New(filepath.Join(t.TempDir(), "outcomes.json")) },
		observe: func(int) (runtimeowner.Owner, error) { return runtimeowner.Owner{}, fmt.Errorf("must not observe") },
		run:     func(*supervisor.Supervisor) error { t.Fatal("started drifted runtime"); return nil },
		hook:    func(context.Context, string, string) checkedHookOutcome { hooks++; return hookSettledOK },
	}
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err := runTrackedStartWith(context.Background(), root, locks, "", false, effects); err == nil {
		t.Fatal("drifted allocation admitted")
	}
	if hooks != 0 {
		t.Fatalf("ran %d hooks before refusal", hooks)
	}
}

func TestManagedDevTrackedRuntimeProjectMismatchRefusesBeforeOwner(t *testing.T) {
	root, _, _ := managedDevRuntimeFixture(t)
	if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: changed\ncommands:\n  start: fixture-http-server\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hooks, owners := 0, 0
	effects := runtimeLaunchEffects{
		store:   func(string) *runtimeowner.Store { return runtimeowner.New(filepath.Join(t.TempDir(), "outcomes.json")) },
		observe: func(int) (runtimeowner.Owner, error) { owners++; return runtimeowner.Owner{}, nil },
		run:     func(*supervisor.Supervisor) error { t.Fatal("started mismatched runtime"); return nil },
		hook:    func(context.Context, string, string) checkedHookOutcome { hooks++; return hookSettledOK },
	}
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err := runTrackedStartWith(context.Background(), root, locks, "", false, effects); err == nil {
		t.Fatal("mismatched project admitted")
	}
	if hooks != 0 || owners != 0 {
		t.Fatalf("effects before refusal: hooks=%d owners=%d", hooks, owners)
	}
}

func TestManagedDevReleaseRetainsPolicyProofAfterWorktreeRemoval(t *testing.T) {
	root, _, allocation := managedDevRuntimeFixture(t)
	target, err := registry.CaptureReleaseTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	op := registry.ReleaseOperation{
		ReleaseRequest: registry.ReleaseRequest{Target: target, Allocation: registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(allocation, "allocation_generation")}, HooksDigest: (releaseHookPlan{}).digest()},
		Snapshot:       &registry.CleanupSnapshot{MainWorktree: true, Project: "fixture", Branch: "main", Ports: []int{42000}},
		Phases:         []registry.PhaseRecord{{Name: registry.PhaseWorktree, State: "completed"}},
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedDevRelease(op); err != nil {
		t.Fatalf("post-removal recovery refused retained proof: %v", err)
	}
	op.Snapshot.Ports = []int{43000}
	if err := validateManagedDevRelease(op); err == nil {
		t.Fatal("post-removal recovery admitted drifted snapshot")
	}
}

func TestManagedDevUnallocatedReleaseRetainsTargetProofAfterRemoval(t *testing.T) {
	root, _, _ := managedDevRuntimeFixture(t)
	target, err := registry.CaptureReleaseTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	op := registry.ReleaseOperation{
		ReleaseRequest: registry.ReleaseRequest{Target: target, Allocation: registry.AllocationRef{Kind: "unallocated"}, HooksDigest: (releaseHookPlan{}).digest()},
		Phases:         []registry.PhaseRecord{{Name: registry.PhaseWorktree, State: "pending"}},
	}
	if err := validateManagedDevRelease(op); err != nil {
		t.Fatalf("contained unallocated target refused: %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	op.Phases[0].State = "completed"
	if err := validateManagedDevRelease(op); err != nil {
		t.Fatalf("unallocated post-removal recovery refused: %v", err)
	}
}

func TestManagedDevPostRemovalReleaseRejectsOutsideAndUnsafeProofs(t *testing.T) {
	root, reg, allocation := managedDevRuntimeFixture(t)
	base := registry.ReleaseOperation{
		ReleaseRequest: registry.ReleaseRequest{Target: registry.TargetIdentity{Path: root}, Allocation: registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(allocation, "allocation_generation")}, HooksDigest: (releaseHookPlan{}).digest()},
		Snapshot:       &registry.CleanupSnapshot{MainWorktree: true, Project: "fixture", Branch: "main", Ports: []int{42000}},
		Phases:         []registry.PhaseRecord{{Name: registry.PhaseWorktree, State: "completed"}},
	}
	t.Run("outside absent target", func(t *testing.T) {
		op := base
		op.Target.Path = filepath.Join(filepath.Dir(filepath.Dir(root)), "outside", "missing")
		if err := validateManagedDevRelease(op); err == nil {
			t.Fatal("outside absent target admitted")
		}
	})
	t.Run("outside retained common", func(t *testing.T) {
		op := base
		op.Target.Common = &registry.CommonIdentity{Path: filepath.Join(filepath.Dir(filepath.Dir(root)), "outside", ".git")}
		if err := validateManagedDevRelease(op); err == nil {
			t.Fatal("outside retained Git path admitted")
		}
	})
	t.Run("retained hooks", func(t *testing.T) {
		op := base
		op.HooksDigest = fmt.Sprintf("%064x", 1)
		if err := validateManagedDevRelease(op); err == nil {
			t.Fatal("retained hook plan admitted")
		}
	})
	t.Run("retained cleanup resources", func(t *testing.T) {
		for name, mutate := range map[string]func(*registry.CleanupSnapshot){
			"database": func(s *registry.CleanupSnapshot) { s.Databases = []string{"unsafe"} },
			"adapter":  func(s *registry.CleanupSnapshot) { s.DatabaseAdapter = "postgresql" },
			"redis":    func(s *registry.CleanupSnapshot) { value := 1; s.RedisDB = &value },
		} {
			t.Run(name, func(t *testing.T) {
				op := base
				copy := *base.Snapshot
				op.Snapshot = &copy
				mutate(op.Snapshot)
				if err := validateManagedDevRelease(op); err == nil {
					t.Fatal("retained unsafe resource admitted")
				}
			})
		}
	})
	t.Run("live redis prefix", func(t *testing.T) {
		if err := reg.UpdateField(root, "redis_prefix", "unsafe"); err != nil {
			t.Fatal(err)
		}
		if err := validateManagedDevRelease(base); err == nil {
			t.Fatal("live Redis prefix admitted")
		}
	})
}

func TestManagedDevRuntimeActionRefusesUntrackedLegacyPath(t *testing.T) {
	root, _, _ := managedDevRuntimeFixture(t)
	untracked := filepath.Join(filepath.Dir(root), "untracked")
	if err := os.Mkdir(untracked, 0700); err != nil {
		t.Fatal(err)
	}
	effects := runtimeActionEffects{
		store:    func(string) *runtimeowner.Store { t.Fatal("legacy action reached store"); return nil },
		verify:   func(runtimeowner.Owner) bool { t.Fatal("legacy action verified owner"); return false },
		status:   func(string, string) (string, error) { t.Fatal("legacy action queried status"); return "", nil },
		command:  func(string, string) error { t.Fatal("legacy runtime command executed"); return nil },
		shutdown: func(string, string, string, time.Duration) error { t.Fatal("legacy shutdown executed"); return nil },
	}
	handled, err := runTrackedRuntimeActionWith(context.Background(), untracked, "restart", effects)
	if !handled || err == nil {
		t.Fatalf("untracked managed dev action handled=%v err=%v", handled, err)
	}
}
