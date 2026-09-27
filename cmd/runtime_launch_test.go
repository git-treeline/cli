package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

func TestTrackedStartConnectsFreshAllocationHooksOwnerAndCompletion(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	root := intent.Target.Path
	if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte("project: lifecycle_fixture\nenv_file: .env\nenv:\n  REMOVED: old\n  CHANGED: old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.SyncRuntimeEnv(root, config.LoadUserConfig("")); err != nil {
		t.Fatal(err)
	}
	text := "project: lifecycle_fixture\nenv_file: .env\nenv:\n  CHANGED: new\ncommands:\n  start: fixture-server\nhooks:\n  worker:\n    pre_start: ['before-{port}']\n    post_stop: ['after-{port}']\n"
	if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Registry.Allocate(registry.Allocation{"worktree": root, "project": "lifecycle_fixture", "port": 12345}); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "gtl-launch-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store := runtimeowner.New(filepath.Join(dir, "outcomes.json"))
	var order []string
	effects := runtimeLaunchEffects{
		store: func(string) *runtimeowner.Store { return store },
		observe: func(int) (runtimeowner.Owner, error) {
			order = append(order, "observe")
			return runtimeowner.Owner{BootID: uuid.NewString(), PID: 42, BirthID: "123:4"}, nil
		},
		hook: func(_ context.Context, dir, command string) checkedHookOutcome {
			if dir != root {
				t.Fatal("wrong hook target")
			}
			order = append(order, command)
			return hookSettledOK
		},
		run: func(sv *supervisor.Supervisor) error {
			order = append(order, "run")
			record, err := store.Get(context.Background(), sv.RuntimeGeneration)
			if err != nil || record.State != runtimeowner.Owned {
				t.Fatal(record, err)
			}
			if sv.Command != "fixture-server" || sv.Port != 12345 {
				t.Fatal("startup settings lost")
			}
			data, err := os.ReadFile(filepath.Join(root, ".env"))
			if err != nil || strings.Contains(string(data), "REMOVED=") || !strings.Contains(string(data), `CHANGED="new"`) || sv.Env["CHANGED"] != "new" {
				t.Fatal("managed environment was not synchronized before launch", err)
			}
			sv.Started()
			locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root})
			if err != nil {
				t.Fatal("foreground retained lifecycle lock", err)
			}
			_ = locks.Close()
			if err := sv.ShutdownRequested(uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if err := sv.ShutdownComplete(); err != nil {
				t.Fatal(err)
			}
			done, err := store.Get(context.Background(), sv.RuntimeGeneration)
			if err != nil || done.State != runtimeowner.Completed {
				t.Fatal(done, err)
			}
			return nil
		},
	}
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer locks.Close()
	if err := runTrackedStartWith(context.Background(), root, locks, "worker", false, effects); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"before-12345", "observe", "run", "after-12345"}) {
		t.Fatal(order)
	}

}

func TestTrackedStartRefusesTargetOrConfigurationChangedByHook(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "configuration", true: "directory"}[replacement], func(t *testing.T) {
			_, intent := releaseExecutorFixture(t)
			root := intent.Target.Path
			original := []byte("project: lifecycle_fixture\ncommands:\n  start: fixture-server\nhooks:\n  worker:\n    pre_start: ['fixture-hook']\n")
			if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), original, 0600); err != nil {
				t.Fatal(err)
			}
			dir, err := os.MkdirTemp("/tmp", "gtl-launch-change-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			store := runtimeowner.New(filepath.Join(dir, "outcomes.json"))
			effects := runtimeLaunchEffects{
				store: func(string) *runtimeowner.Store { return store },
				observe: func(int) (runtimeowner.Owner, error) {
					t.Fatal("observed changed target")
					return runtimeowner.Owner{}, nil
				},
				run: func(*supervisor.Supervisor) error { t.Fatal("launched changed target"); return nil },
				hook: func(context.Context, string, string) checkedHookOutcome {
					if replacement {
						old := root + "-original"
						if err := os.Rename(root, old); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = os.RemoveAll(old) })
						if err := os.Mkdir(root, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), original, 0600); err != nil {
							t.Fatal(err)
						}
					} else if err := os.WriteFile(filepath.Join(root, ".treeline.yml"), append(original, []byte("env:\n  CHANGED: new\n")...), 0600); err != nil {
						t.Fatal(err)
					}
					return hookSettledOK
				},
			}
			locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{root})
			if err != nil {
				t.Fatal(err)
			}
			defer locks.Close()
			if err := runTrackedStartWith(context.Background(), root, locks, "worker", false, effects); err == nil {
				t.Fatal("changed launch binding accepted")
			}
			if _, err := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
				t.Fatal("changed target environment was written")
			}
		})
	}
}

func TestTrackedStartRefusesLegacyAndReservedTargetsBeforeEffects(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "reserved", true: "legacy"}[legacy], func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			if err := os.WriteFile(filepath.Join(intent.Target.Path, ".treeline.yml"), []byte("project: lifecycle_fixture\ncommands:\n  start: fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if legacy {
				if _, err := e.Registry.Release(intent.Target.Path); err != nil {
					t.Fatal(err)
				}
				if err := e.Registry.Allocate(registry.Allocation{"worktree": intent.Target.Path, "port": 12345}); err != nil {
					t.Fatal(err)
				}
			} else {
				commandLifecycleReserve(t, e.Registry, intent.Target.Path, "")
			}
			dir, err := os.MkdirTemp("/tmp", "gtl-launch-refusal-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			effects := runtimeLaunchEffects{store: func(string) *runtimeowner.Store { return runtimeowner.New(filepath.Join(dir, "outcomes.json")) }, observe: func(int) (runtimeowner.Owner, error) {
				t.Fatal("observed unadmitted process")
				return runtimeowner.Owner{}, nil
			}, run: func(*supervisor.Supervisor) error { t.Fatal("unadmitted launch"); return nil }, hook: func(context.Context, string, string) checkedHookOutcome {
				t.Fatal("unadmitted hook")
				return hookSettledOK
			}}
			if err := runTrackedStartWith(context.Background(), intent.Target.Path, nil, "", false, effects); err == nil {
				t.Fatal("unadmitted start accepted")
			}
		})
	}
}
