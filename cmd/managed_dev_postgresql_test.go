package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

const managedDevPostgreSQLConfig = "project: host_migration_qa\ncommands: {start: fixture-http-server}\ndatabase: {adapter: postgresql, template: treeline_dev_host_migration_qa_template, name: '{template}_{worktree}', host: 127.0.0.1, port: 5432, user: qa}\nenv: {DATABASE_NAME: '{database}'}\n"

func managedDevPostgreSQLFixture(t *testing.T, main bool) (string, *registry.Registry, registry.Allocation) {
	t.Helper()
	root, reg, a := managedDevSQLiteFixture(t, main)
	private := filepath.Join(os.Getenv("GTL_HOME"), "config.json")
	data, err := os.ReadFile(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, []byte(strings.ReplaceAll(string(data), "fixture", "host_migration_qa")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(managedDevPostgreSQLConfig), 0600); err != nil {
		t.Fatal(err)
	}
	names := allocator.ExpectedDatabases(config.LoadProjectConfigReadOnlyQuiet(root), filepath.Base(root), main)
	a["project"], a["database_adapter"], a["database"], a["databases"] = "host_migration_qa", "postgresql", names[0], names
	if err := reg.Allocate(a); err != nil {
		t.Fatal(err)
	}
	a, err = reg.FindForRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, reg, a
}

func managedDevPostgreSQLReleaseFixture(t *testing.T, main bool) (releaseDatabasesAdapter, registry.ReleaseOperation, *releaseDatabaseFake) {
	t.Helper()
	root, reg, allocation := managedDevPostgreSQLFixture(t, main)
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

func TestManagedDevPostgreSQLRuntimeBinding(t *testing.T) {
	for _, change := range []string{"none", "remote", "template", "allocation", "ambient"} {
		t.Run(change, func(t *testing.T) {
			root, reg, a := managedDevPostgreSQLFixture(t, false)
			if err := validateManagedDevRuntime(root, a, false); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "remote", "template":
				from, to := "127.0.0.1", "remote.invalid"
				if change == "template" {
					from, to = "treeline_dev_host_migration_qa_template", "production"
				}
				if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(strings.ReplaceAll(managedDevPostgreSQLConfig, from, to)), 0600); err != nil {
					t.Fatal(err)
				}
			case "allocation":
				managedDevSQLiteDriftAllocation(t, reg, root)
			case "ambient":
				t.Setenv("PGHOSTADDR", "192.0.2.10")
			}
			previous := managedDevPortAvailable
			probes := 0
			managedDevPortAvailable = func(int) error { probes++; return nil }
			t.Cleanup(func() { managedDevPortAvailable = previous })
			sv := supervisor.New("fixture", root, "unused")
			installManagedDevRuntimeAdmission(sv, root)
			err := sv.BeforeChildStart(42010)
			if change == "none" {
				if err != nil || probes != 1 {
					t.Fatal(err, probes)
				}
			} else if err == nil || probes != 0 {
				t.Fatal("unsafe child admitted", err, probes)
			}
		})
	}
}

func TestManagedDevPostgreSQLReleaseSelectedOnly(t *testing.T) {
	for _, main := range []bool{false, true} {
		t.Run(map[bool]string{true: "main", false: "linked"}[main], func(t *testing.T) {
			adapter, op, fake := managedDevPostgreSQLReleaseFixture(t, main)
			adapter.NewAdapter = func(name string, args []string) (database.Adapter, error) {
				if name != "postgresql" || !reflect.DeepEqual(args, []string{"-h", "127.0.0.1", "-p", "5432", "-U", "qa"}) {
					t.Fatal("connection lost", name, args)
				}
				return fake, nil
			}
			if result := adapter.Run(context.Background(), op); result.State != "completed" {
				t.Fatal(result)
			}
			want := []string{"treeline_dev_host_migration_qa_template_qa_one"}
			if main {
				want = nil
			}
			if !reflect.DeepEqual(fake.drops, want) {
				t.Fatal("wrong cleanup targets", fake.drops)
			}
		})
	}
}

func TestManagedDevPostgreSQLReleaseDrift(t *testing.T) {
	for _, change := range []string{"remote", "port", "template", "snapshot", "allocation", "late allocation", "ambient"} {
		t.Run(change, func(t *testing.T) {
			adapter, op, fake := managedDevPostgreSQLReleaseFixture(t, false)
			switch change {
			case "remote", "port", "template":
				from, to := "127.0.0.1", "remote.invalid"
				if change == "port" {
					from, to = "5432", "5433"
				}
				if change == "template" {
					from, to = "treeline_dev_host_migration_qa_template", "production"
				}
				if err := os.WriteFile(filepath.Join(op.Target.Path, config.ProjectConfigFile), []byte(strings.ReplaceAll(managedDevPostgreSQLConfig, from, to)), 0600); err != nil {
					t.Fatal(err)
				}
			case "snapshot":
				op.Snapshot.Databases = []string{"treeline_dev_host_migration_qa_template_qa_two"}
			case "allocation":
				managedDevSQLiteDriftAllocation(t, adapter.Registry, op.Target.Path)
			case "ambient":
				t.Setenv("PGSERVICE", "production")
			}
			adapter.NewAdapter = func(string, []string) (database.Adapter, error) {
				if change != "late allocation" {
					t.Fatal("unsafe adapter reached")
				}
				managedDevSQLiteDriftAllocation(t, adapter.Registry, op.Target.Path)
				return fake, nil
			}
			if result := adapter.Run(context.Background(), op); result.State != "blocked" || len(fake.drops) != 0 {
				t.Fatal("unsafe drop", result, fake.drops)
			}
		})
	}
}
