package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
)

type releaseDatabaseFake struct {
	database.Adapter
	drops []string
	drop  func(string) error
}

func (f *releaseDatabaseFake) Drop(name string) error {
	f.drops = append(f.drops, name)
	if f.drop != nil {
		return f.drop(name)
	}
	return nil
}
func (f *releaseDatabaseFake) ListDatabases() ([]string, error) {
	return []string{"app", "app_test", "app_test_1"}, nil
}

func releaseDatabaseFixture(t *testing.T) (releaseDatabasesAdapter, registry.ReleaseOperation, *releaseDatabaseFake) {
	t.Helper()
	executor, intent := releaseExecutorFixture(t)
	reg := executor.Registry
	if err := reg.Allocate(registry.Allocation{"worktree": intent.Target.Path, "project": "lifecycle_fixture", "database": "app", "databases": []string{"app", "app_test"}, "database_adapter": "postgresql"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.PrepareRelease(intent); err != nil {
		t.Fatal(err)
	}
	var op registry.ReleaseOperation
	for _, phase := range []registry.ReleasePhase{registry.PhasePreHook, registry.PhaseSupervisor, registry.PhaseDatabases} {
		current, err := reg.GetRelease(intent.OperationID)
		if err != nil || current == nil {
			t.Fatal(err)
		}
		token, err := reg.BeginReleasePhase(intent.OperationID, phase, current.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if phase != registry.PhaseDatabases {
			if _, err := reg.RecordReleasePhase(token, registry.PhaseResult{State: "completed", Code: "ok"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	current, err := reg.GetRelease(intent.OperationID)
	if err != nil || current == nil {
		t.Fatal(err)
	}
	op = *current
	revision, err := format.DatabaseCleanupConfigRevision(intent.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	fake := &releaseDatabaseFake{}
	return releaseDatabasesAdapter{Registry: reg, ConfigRevision: revision, NewAdapter: func(string, []string) (database.Adapter, error) { return fake, nil }}, op, fake
}

func TestReleaseDatabasesAdapterUsesRetainedSnapshotAndStrictSurvivors(t *testing.T) {
	adapter, op, fake := releaseDatabaseFixture(t)
	other := commandLifecycleRoot(t)
	if err := adapter.Registry.Allocate(registry.Allocation{"worktree": other, "database": "app_test_1"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(op.Target.Path, ".treeline.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if result := adapter.Run(context.Background(), op); result != (registry.PhaseResult{State: "completed", Code: "ok"}) {
		t.Fatal(result)
	}
	if !reflect.DeepEqual(fake.drops, []string{"app", "app_test"}) {
		t.Fatal("snapshot/shared protection lost", fake.drops)
	}
	after, err := os.ReadFile(filepath.Join(op.Target.Path, ".treeline.yml"))
	if err != nil || string(before) != string(after) {
		t.Fatal("config migrated", err)
	}
	current, err := adapter.Registry.GetRelease(op.OperationID)
	if err != nil || !reflect.DeepEqual(*current, op) {
		t.Fatal("adapter mutated registry evidence", err)
	}
}

func TestReleaseDatabasesAdapterRefusesUnboundInputs(t *testing.T) {
	for _, kind := range []string{"no revision", "wrong revision", "no policy", "no allocation", "unstarted", "changed snapshot", "malformed survivor", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			adapter, op, fake := releaseDatabaseFixture(t)
			ctx := context.Background()
			switch kind {
			case "no revision":
				adapter.ConfigRevision = ""
			case "wrong revision":
				adapter.ConfigRevision = strings.Repeat("0", 64)
			case "no policy":
				op.Policy.DropDatabases = false
			case "no allocation":
				op.Allocation = registry.AllocationRef{Kind: "unallocated"}
			case "unstarted":
				for index := range op.Phases {
					if op.Phases[index].Name == registry.PhaseDatabases {
						op.Phases[index].State = "pending"
					}
				}
			case "changed snapshot":
				op.Snapshot.Databases = []string{"not_retained"}
			case "malformed survivor":
				other := commandLifecycleRoot(t)
				if err := adapter.Registry.Allocate(registry.Allocation{"worktree": other, "databases": []any{"app_test_1", float64(7)}}); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if result := adapter.Run(ctx, op); result.State != "blocked" || len(fake.drops) != 0 {
				t.Fatalf("refusal: %+v %v", result, fake.drops)
			}
		})
	}
}

func TestReleaseDatabasesAdapterPreservesPostDispatchUncertainty(t *testing.T) {
	for _, kind := range []string{"drop failure", "survivor changed", "config changed", "registry corrupt"} {
		t.Run(kind, func(t *testing.T) {
			adapter, op, fake := releaseDatabaseFixture(t)
			other := commandLifecycleRoot(t)
			fake.drop = func(string) error {
				switch kind {
				case "drop failure":
					return errors.New("private credential diagnostic")
				case "survivor changed":
					return adapter.Registry.Allocate(registry.Allocation{"worktree": other, "database": "app_test"})
				case "config changed":
					return os.WriteFile(filepath.Join(op.Target.Path, ".treeline.yml"), []byte("project: changed\n"), 0600)
				case "registry corrupt":
					return os.WriteFile(adapter.Registry.Path, []byte("{"), 0600)
				}
				return nil
			}
			result := adapter.Run(context.Background(), op)
			if result != (registry.PhaseResult{State: "uncertain", Code: "unknown"}) || len(fake.drops) != 1 {
				t.Fatalf("partial result: %+v %v", result, fake.drops)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "private") {
				t.Fatal("diagnostic leaked")
			}
		})
	}
}
