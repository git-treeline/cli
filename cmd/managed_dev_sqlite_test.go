package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

const managedDevSQLiteConfig = "project: fixture\ncommands:\n  start: fixture-http-server\ndatabase:\n  adapter: sqlite\n  template: db/seed.sqlite\n  name: '{project}_{worktree}'\n"

func managedDevSQLiteFixture(t *testing.T, main bool) (string, *registry.Registry, registry.Allocation) {
	t.Helper()
	root, reg, allocation := managedDevRuntimeFixture(t)
	if !main {
		linked := filepath.Join(filepath.Dir(root), "qa-one")
		if output, err := exec.Command("git", "-C", root, "worktree", "add", "-q", "-b", "qa-one", linked).CombinedOutput(); err != nil {
			t.Fatalf("fixture worktree: %v %s", err, output)
		}
		root = linked
		if err := os.WriteFile(filepath.Join(os.Getenv("GTL_HOME"), "config.json"), []byte(`{"port":{"increment":10,"reservations":{"fixture":42000,"fixture/qa-one":42010}},"router":{"mode":"disabled"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		allocation = registry.Allocation{"worktree": root, "project": "fixture", "branch": "qa-one", "main_worktree": false, "port": 42010, "ports": []int{42010}}
	}
	if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(managedDevSQLiteConfig), 0600); err != nil {
		t.Fatal(err)
	}
	name := "fixture_qa_one"
	if main {
		name = "db/seed.sqlite"
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte("fixture database"), 0600); err != nil {
		t.Fatal(err)
	}
	allocation["database_adapter"], allocation["database"], allocation["databases"] = "sqlite", name, []string{name}
	if main {
		if err := reg.Allocate(allocation); err != nil {
			t.Fatal(err)
		}
	} else {
		target, err := registry.CaptureReleaseTarget(root)
		if err != nil {
			t.Fatal(err)
		}
		commandFreshEnrollment(t, reg, target, allocation)
	}
	allocation, err := reg.FindForRelease(root)
	if err != nil || allocation == nil {
		t.Fatal("SQLite allocation unavailable", err)
	}
	return root, reg, allocation
}

func managedDevSQLiteReleaseFixture(t *testing.T, main bool) (releaseDatabasesAdapter, registry.ReleaseOperation, *releaseDatabaseFake) {
	t.Helper()
	root, reg, allocation := managedDevSQLiteFixture(t, main)
	target, err := registry.CaptureReleaseTarget(root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := registry.AllocationRuntimeBinding(allocation)
	if err != nil {
		t.Fatal(err)
	}
	request := registry.ReleaseRequest{
		OperationID: uuid.NewString(), Target: target,
		Allocation:     registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(allocation, "allocation_generation")},
		RuntimeBinding: &binding, HooksDigest: (releaseHookPlan{}).digest(),
		DatabaseConfigRevision: commandDatabaseRevision(t, root), Policy: registry.ReleasePolicy{DropDatabases: true},
	}
	op, _, err := reg.PrepareRelease(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []registry.ReleasePhase{registry.PhasePreHook, registry.PhaseSupervisor, registry.PhaseDatabases} {
		token, err := reg.BeginReleasePhase(op.OperationID, phase, op.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if phase != registry.PhaseDatabases {
			if _, err := reg.RecordReleasePhase(token, registry.PhaseResult{State: "completed", Code: "ok"}); err != nil {
				t.Fatal(err)
			}
		}
		current, err := reg.GetRelease(op.OperationID)
		if err != nil || current == nil {
			t.Fatal(err)
		}
		op = *current
	}
	fake := &releaseDatabaseFake{}
	return releaseDatabasesAdapter{Registry: reg, NewAdapter: func(string, []string) (database.Adapter, error) { return fake, nil }}, op, fake
}

func TestManagedDevSQLiteRuntimeRechecksBeforeChild(t *testing.T) {
	for _, change := range []string{"none", "config", "allocation", "primary", "symlink", "hardlink-wal", "symlink-shm", "hardlink-journal"} {
		t.Run(change, func(t *testing.T) {
			root, reg, allocation := managedDevSQLiteFixture(t, false)
			if err := validateManagedDevRuntime(root, allocation, false); err != nil {
				t.Fatal("valid SQLite runtime refused", err)
			}
			probes := 0
			previous := managedDevPortAvailable
			managedDevPortAvailable = func(int) error { probes++; return nil }
			t.Cleanup(func() { managedDevPortAvailable = previous })
			sv := supervisor.New("fixture", root, "unused")
			installManagedDevRuntimeAdmission(sv, root)
			name := registry.GetString(allocation, "database")
			switch change {
			case "config":
				if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(strings.ReplaceAll(managedDevSQLiteConfig, "{project}_{worktree}", "changed")), 0600); err != nil {
					t.Fatal(err)
				}
			case "allocation":
				allocation["database"], allocation["databases"] = "changed", []string{"changed"}
				if err := reg.Allocate(allocation); err != nil {
					t.Fatal(err)
				}
			case "primary":
				if err := reg.UpdateField(root, "database", "changed"); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink-wal", "symlink-shm", "hardlink-journal":
				managedDevUnsafeSQLiteFile(t, root, name, change)
			}
			err := sv.BeforeChildStart(42010)
			if change == "none" {
				if err != nil || probes != 1 {
					t.Fatalf("valid child admission: %v probes=%d", err, probes)
				}
			} else if err == nil || probes != 0 {
				t.Fatalf("unsafe child admitted: %v probes=%d", err, probes)
			}
		})
	}
}

func managedDevUnsafeSQLiteFile(t *testing.T, root, name, kind string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if _, companion, ok := strings.Cut(kind, "-"); ok {
		suffix = "-" + companion
	}
	target := filepath.Join(root, name) + suffix
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	link := os.Symlink
	if strings.HasPrefix(kind, "hardlink") {
		link = os.Link
	}
	if err := link(outside, target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
			t.Fatalf("unrelated SQLite target changed: %v", err)
		}
	})
}

func TestManagedDevSQLiteReleaseSnapshotAndMainTemplate(t *testing.T) {
	for _, main := range []bool{false, true} {
		t.Run(map[bool]string{false: "linked clone", true: "main template"}[main], func(t *testing.T) {
			adapter, op, fake := managedDevSQLiteReleaseFixture(t, main)
			if err := validateManagedDevRelease(op); err != nil {
				t.Fatal("valid SQLite release refused", err)
			}
			if result := adapter.Run(context.Background(), op); result != (registry.PhaseResult{State: "completed", Code: "ok"}) {
				t.Fatal(result)
			}
			if main {
				if len(fake.drops) != 0 {
					t.Fatal("main template was dropped", fake.drops)
				}
			} else if !reflect.DeepEqual(fake.drops, []string{filepath.Join(op.Target.Path, "fixture_qa_one")}) {
				t.Fatal("unexpected cleanup targets", fake.drops)
			}
		})
	}
}

func TestManagedDevSQLiteReleaseRefusesDriftBeforeAdapter(t *testing.T) {
	for _, change := range []string{"config", "allocation", "snapshot", "main flag", "symlink", "hardlink", "symlink-wal", "hardlink-shm", "symlink-journal", "journal"} {
		t.Run(change, func(t *testing.T) {
			adapter, op, fake := managedDevSQLiteReleaseFixture(t, false)
			root, name := op.Target.Path, op.Snapshot.Databases[0]
			switch change {
			case "config":
				if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(strings.ReplaceAll(managedDevSQLiteConfig, "{project}_{worktree}", "changed")), 0600); err != nil {
					t.Fatal(err)
				}
			case "allocation":
				managedDevSQLiteDriftAllocation(t, adapter.Registry, root)
			case "snapshot":
				op.Snapshot.Databases = []string{"changed"}
			case "main flag":
				op.Snapshot.MainWorktree = true
			case "journal":
				if err := os.WriteFile(filepath.Join(root, name)+"-journal", []byte("fixture journal"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				managedDevUnsafeSQLiteFile(t, root, name, change)
			}
			adapter.NewAdapter = func(string, []string) (database.Adapter, error) {
				t.Fatal("unsafe release reached adapter construction")
				return fake, nil
			}
			if err := validateManagedDevRelease(op); err == nil {
				t.Fatal("unsafe snapshot admitted")
			}
			if result := adapter.Run(context.Background(), op); result.State != "blocked" || len(fake.drops) != 0 {
				t.Fatalf("unsafe cleanup: %+v %v", result, fake.drops)
			}
		})
	}
}

func managedDevSQLiteDriftAllocation(t *testing.T, reg *registry.Registry, root string) {
	t.Helper()
	data, err := os.ReadFile(reg.Path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, raw := range document["allocations"].([]any) {
		allocation := raw.(map[string]any)
		if allocation["worktree"] == root {
			allocation["database"], allocation["databases"] = "changed", []string{"changed"}
		}
	}
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reg.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManagedDevSQLiteReleaseRechecksBeforeDrop(t *testing.T) {
	for _, change := range []string{"hardlink-wal", "symlink-shm", "journal", "allocation"} {
		t.Run(change, func(t *testing.T) {
			adapter, op, fake := managedDevSQLiteReleaseFixture(t, false)
			adapter.NewAdapter = func(string, []string) (database.Adapter, error) {
				switch change {
				case "allocation":
					managedDevSQLiteDriftAllocation(t, adapter.Registry, op.Target.Path)
				case "journal":
					if err := os.WriteFile(filepath.Join(op.Target.Path, op.Snapshot.Databases[0])+"-journal", []byte("fixture journal"), 0600); err != nil {
						t.Fatal(err)
					}
				default:
					managedDevUnsafeSQLiteFile(t, op.Target.Path, op.Snapshot.Databases[0], change)
				}
				return fake, nil
			}
			if result := adapter.Run(context.Background(), op); result.State != "blocked" || len(fake.drops) != 0 {
				t.Fatalf("late unsafe cleanup: %+v %v", result, fake.drops)
			}
		})
	}
}

func TestManagedDevSQLiteReleaseAfterRemovalRequiresSettledCleanup(t *testing.T) {
	adapter, op, fake := managedDevSQLiteReleaseFixture(t, false)
	if err := os.RemoveAll(op.Target.Path); err != nil {
		t.Fatal(err)
	}
	op.Phases = append(op.Phases, registry.PhaseRecord{Name: registry.PhaseWorktree, State: "completed"})
	if err := validateManagedDevRelease(op); err == nil {
		t.Fatal("removed target admitted unfinished database cleanup")
	}
	if result := adapter.Run(context.Background(), op); result.State != "blocked" || len(fake.drops) != 0 {
		t.Fatal("removed target replayed cleanup", result, fake.drops)
	}
	for index := range op.Phases {
		if op.Phases[index].Name == registry.PhaseDatabases {
			op.Phases[index].State = "completed"
		}
	}
	if err := validateManagedDevRelease(op); err != nil {
		t.Fatal("settled cleanup re-read removed configuration", err)
	}
	op.Policy.DropDatabases = false
	op.Phases = []registry.PhaseRecord{{Name: registry.PhaseWorktree, State: "completed"}, {Name: registry.PhaseAllocation, State: "pending"}}
	if err := validateManagedDevRelease(op); err != nil {
		t.Fatal("policy-skipped cleanup re-read removed configuration", err)
	}
}
