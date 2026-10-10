package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
)

func releaseHostsFixture(t *testing.T) (releaseHostsAdapter, registry.ReleaseOperation, string, string) {
	t.Helper()
	e, intent := releaseExecutorFixture(t)
	survivor := commandLifecycleRoot(t)
	if err := e.Registry.Allocate(registry.Allocation{"worktree": survivor, "project": "survivor", "branch": "main"}); err != nil {
		t.Fatal(err)
	}
	op, _, err := e.Registry.PrepareRelease(intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range op.Phases {
		current, err := e.Registry.GetRelease(op.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		token, err := e.Registry.BeginReleasePhase(op.OperationID, phase.Name, current.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if phase.Name == registry.PhaseHosts {
			break
		}
		if phase.Name == registry.PhaseAllocation {
			_, err = e.Registry.RetireReleaseAllocation(token)
		} else {
			_, err = e.Registry.RecordReleasePhase(token, registry.PhaseResult{State: "completed", Code: "ok"})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := e.Registry.GetRelease(op.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte(releaseHostsContent("lifecycle-fixture.example.test", "survivor-main.example.test")), 0600); err != nil {
		t.Fatal(err)
	}
	a := releaseHostsAdapter{Registry: e.Registry, Domain: "example.test", Read: func() (service.ManagedHostsSnapshot, error) { return service.ReadManagedHostsStrict(path) }}
	return a, *current, path, survivor
}

func releaseHostsContent(hosts ...string) string {
	s := "127.0.0.1 localhost\n# BEGIN git-treeline" + platform.DevSuffix() + "\n"
	for _, host := range hosts {
		s += "127.0.0.1 " + host + "\n"
	}
	return s + "# END git-treeline" + platform.DevSuffix() + "\n::1 localhost\n"
}

func requireHostsOutcome(t *testing.T, result registry.PhaseResult, state, code string) {
	t.Helper()
	if result.State != state || result.Code != code {
		t.Fatalf("want %s/%s, got %+v", state, code, result)
	}
}

func TestReleaseHostsStrictSurvivorsAndNoConfigMutation(t *testing.T) {
	a, op, path, _ := releaseHostsFixture(t)
	beforeRegistry, _ := os.ReadFile(a.Registry.Path)
	configPath := filepath.Join(op.Target.Path, ".treeline.yml")
	beforeConfig, _ := os.ReadFile(configPath)
	userConfig := platform.ConfigFile()
	if err := os.WriteFile(userConfig, []byte(`{deliberately invalid config`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	requireHostsOutcome(t, a.Run(context.Background(), op), "completed", "ok")
	actual, _ := os.ReadFile(path)
	if string(actual) != releaseHostsContent("survivor-main.example.test") {
		t.Fatalf("survivor/unrelated content lost: %s", actual)
	}
	afterRegistry, _ := os.ReadFile(a.Registry.Path)
	afterConfig, _ := os.ReadFile(configPath)
	afterUser, _ := os.ReadFile(userConfig)
	if !bytes.Equal(beforeRegistry, afterRegistry) || !bytes.Equal(beforeConfig, afterConfig) || string(afterUser) != `{deliberately invalid config` {
		t.Fatal("adapter mutated registry/config")
	}
	a.Write = func(service.ManagedHostsSnapshot, []string) error {
		t.Fatal("settled hosts state rewritten")
		return nil
	}
	requireHostsOutcome(t, a.Run(context.Background(), op), "completed", "ok")
}

func TestReleaseHostsOptInAndReadRefusals(t *testing.T) {
	for _, kind := range []string{"missing", "unmanaged", "unrelated managed", "malformed", "read error", "corrupt registry", "invalid survivor", "invalid domain", "stale phase"} {
		t.Run(kind, func(t *testing.T) {
			a, op, path, survivor := releaseHostsFixture(t)
			want := "blocked"
			code := "failed"
			switch kind {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				want = "completed"
			case "unmanaged":
				if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "completed"
			case "unrelated managed":
				if err := os.WriteFile(path, []byte(releaseHostsContent("unrelated.example.test")), 0600); err != nil {
					t.Fatal(err)
				}
				want = "completed"
			case "malformed":
				if err := os.WriteFile(path, []byte("# BEGIN git-treeline"+platform.DevSuffix()+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "read error":
				a.Read = func() (service.ManagedHostsSnapshot, error) { return service.ManagedHostsSnapshot{}, os.ErrPermission }
			case "corrupt registry":
				if err := os.WriteFile(a.Registry.Path, []byte(`{broken`), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid survivor":
				if err := a.Registry.Allocate(registry.Allocation{"worktree": survivor, "project": 17, "branch": "main"}); err != nil {
					t.Fatal(err)
				}
			case "invalid domain":
				a.Domain = "example.test\n192.0.2.1 other"
				code = "config_changed"
			case "stale phase":
				op.Revision--
			}
			if want == "completed" {
				code = "ok"
			}
			before, _ := os.ReadFile(path)
			a.Write = func(service.ManagedHostsSnapshot, []string) error {
				t.Fatal("no-op/refusal dispatched write")
				return nil
			}
			requireHostsOutcome(t, a.Run(context.Background(), op), want, code)
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("no-op/refusal changed hosts")
			}
		})
	}
}

func TestReleaseHostsExplicitWriteAndVerificationOutcomes(t *testing.T) {
	for _, kind := range []string{"permission", "changed", "busy", "partial", "unknown error", "lying success", "readback failure", "changed survivors"} {
		t.Run(kind, func(t *testing.T) {
			a, op, path, survivor := releaseHostsFixture(t)
			state, code := "uncertain", "unknown"
			writes := 0
			a.Write = func(snapshot service.ManagedHostsSnapshot, desired []string) error {
				writes++
				if !reflect.DeepEqual(desired, []string{"survivor-main.example.test"}) {
					t.Fatalf("wrong desired hosts: %v", desired)
				}
				switch kind {
				case "permission":
					return service.ErrHostsPrivilegeRequired
				case "changed":
					return service.ErrHostsChanged
				case "busy":
					return service.ErrHostsBusy
				case "partial":
					if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
						t.Fatal(err)
					}
					return service.ErrHostsUncertain
				case "unknown error":
					return errors.New("sensitive output must not become receipt")
				case "lying success":
					return nil
				}
				if err := service.SyncManagedHostsNoninteractive(snapshot, desired); err != nil {
					return err
				}
				if kind == "changed survivors" {
					if err := a.Registry.UpdateField(survivor, "branch", "changed"); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			// Readback uses the same captured callback, whose second call can
			// fail independently of a successful write.
			if kind == "readback failure" {
				reads := 0
				a.Read = func() (service.ManagedHostsSnapshot, error) {
					reads++
					if reads > 1 {
						return service.ManagedHostsSnapshot{}, os.ErrPermission
					}
					return service.ReadManagedHostsStrict(path)
				}
			}
			if kind == "permission" {
				state, code = "blocked", "privilege_required"
			}
			if kind == "changed" || kind == "busy" {
				state, code = "blocked", "failed"
			}
			requireHostsOutcome(t, a.Run(context.Background(), op), state, code)
			if writes != 1 {
				t.Fatalf("write count: %d", writes)
			}
		})
	}
}

func TestReleaseHostsPreservesCollidingSurvivorRoute(t *testing.T) {
	a, op, path, survivor := releaseHostsFixture(t)
	if err := a.Registry.UpdateField(survivor, "project", op.Snapshot.Project); err != nil {
		t.Fatal(err)
	}
	if err := a.Registry.UpdateField(survivor, "branch", op.Snapshot.Branch); err != nil {
		t.Fatal(err)
	}
	requireHostsOutcome(t, a.Run(context.Background(), op), "completed", "ok")
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "127.0.0.1 lifecycle-fixture.example.test\n") || strings.Contains(string(content), "survivor-main") {
		t.Fatalf("shared hostname removed: %s", content)
	}
}
