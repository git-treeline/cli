package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/google/uuid"
)

const lifecycleConfig = "project: lifecycle_fixture\ndefault_branch: main\nports_needed: 1\nenv:\n  PORT: '{port}'\nhooks:\n  pre_setup:\n    - touch setup-hook-ran\n  pre_release:\n    - touch release-hook-ran\ncommands:\n  setup:\n    - touch setup-command-ran\n"

func commandLifecycleEnvironment(t *testing.T) {
	t.Helper()
	releaseSafetyEnvironment(t)
	t.Setenv("GTL_HEADLESS", "1")
	t.Setenv("GTL_DEFER_SETUP", "")
	oldDry, oldMain, oldRegistry := setupDryRun, setupMainRepo, setup.RegistryPath
	setupDryRun, setupMainRepo, setup.RegistryPath = false, "", ""
	releaseRemoveWorktree, releaseForce = false, true
	t.Cleanup(func() {
		setupDryRun, setupMainRepo, setup.RegistryPath = oldDry, oldMain, oldRegistry
	})
}

func commandLifecycleRoot(t *testing.T) string {
	t.Helper()
	root, err := registry.CanonicalLifecycleTarget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(lifecycleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func commandLifecycleAllocate(t *testing.T, reg *registry.Registry, root string) {
	t.Helper()
	target := registry.TargetIdentity{Path: root, Directory: commandDirectoryIdentity(t, root)}
	command := exec.Command("git", "-c", "core.fsmonitor=false", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if raw, err := command.Output(); err == nil {
		common, err := filepath.EvalSymlinks(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		target.Common = &registry.CommonIdentity{Path: common, DirectoryIdentity: commandDirectoryIdentity(t, common)}
	}
	commandFreshEnrollment(t, reg, target, registry.Allocation{"worktree": root, "project": "lifecycle_fixture"})
}

func commandDirectoryIdentity(t *testing.T, path string) registry.DirectoryIdentity {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	return registry.DirectoryIdentity{Dev: strconv.FormatUint(uint64(stat.Dev), 10), Ino: strconv.FormatUint(uint64(stat.Ino), 10)}
}

func commandLifecycleReserve(t *testing.T, reg *registry.Registry, root, common string) string {
	t.Helper()
	alloc, err := reg.FindForRelease(root)
	if err != nil || alloc == nil {
		t.Fatalf("allocation: %v", err)
	}
	target := registry.TargetIdentity{Path: root, Directory: commandDirectoryIdentity(t, root)}
	if common != "" {
		target.Common = &registry.CommonIdentity{Path: common, DirectoryIdentity: commandDirectoryIdentity(t, common)}
	}
	id := uuid.NewString()
	_, _, err = reg.PrepareRelease(registry.ReleaseRequest{
		OperationID: id, Target: target,
		Allocation:  registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(alloc, "allocation_generation")},
		HooksDigest: strings.Repeat("0", 64), RuntimeBinding: &registry.RuntimeBinding{Kind: "never_started"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func commandLifecycleUnchanged(t *testing.T, reg *registry.Registry, before []byte, roots ...string) {
	t.Helper()
	if after, err := os.ReadFile(reg.Path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refusal changed registry: %v", err)
	}
	for _, root := range roots {
		if after, err := os.ReadFile(filepath.Join(root, config.ProjectConfigFile)); err != nil || string(after) != lifecycleConfig {
			t.Fatalf("refusal migrated config: %v: %s", err, after)
		}
		for _, name := range []string{"setup-hook-ran", "setup-command-ran", "release-hook-ran", ".env.local"} {
			if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refusal wrote %s: %v", name, err)
			}
		}
	}
}

func TestLifecycleSetupRefusesBeforeConfigAndHooks(t *testing.T) {
	for _, refusal := range []string{"target reservation", "source reservation", "corrupt registry", "busy execution"} {
		t.Run(refusal, func(t *testing.T) {
			commandLifecycleEnvironment(t)
			target, source := commandLifecycleRoot(t), commandLifecycleRoot(t)
			setupMainRepo = source
			reg := registry.New("")
			commandLifecycleAllocate(t, reg, target)
			commandLifecycleAllocate(t, reg, source)
			want := "reserved"
			switch refusal {
			case "target reservation":
				commandLifecycleReserve(t, reg, target, "")
			case "source reservation":
				commandLifecycleReserve(t, reg, source, "")
			case "corrupt registry", "busy execution":
				if err := os.WriteFile(reg.Path, []byte(`{broken`), 0600); err != nil {
					t.Fatal(err)
				}
				want = "registry"
			}
			calls := 0
			acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
				calls++
				if len(paths) != 2 || !slices.Contains(paths, source) || !slices.Contains(paths, target) {
					t.Fatalf("setup lock domain omitted source/target: %v", paths)
				}
				if refusal == "busy execution" {
					return nil, errors.New("fixture execution busy")
				}
				return &registry.LifecycleLocks{}, nil
			}
			if refusal == "busy execution" {
				want = "execution busy"
			}
			before, _ := os.ReadFile(reg.Path)
			if err := setupCmd.RunE(setupCmd, []string{target}); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("setup refusal: %v", err)
			}
			if calls != 1 {
				t.Fatalf("acquisition count: %d", calls)
			}
			commandLifecycleUnchanged(t, reg, before, target, source)
		})
	}
}

func TestLifecycleSetupDiscoversAndChecksPrimarySource(t *testing.T) {
	commandLifecycleEnvironment(t)
	source, target := releaseSafetyFixture(t)
	for _, root := range []string{source, target} {
		if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(lifecycleConfig), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, source)
	commandLifecycleAllocate(t, reg, target)
	commandLifecycleReserve(t, reg, source, filepath.Join(source, ".git"))
	calls := 0
	acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
		calls++
		if !slices.Contains(paths, source) || !slices.Contains(paths, target) {
			t.Fatalf("wrong automatic source: %v", paths)
		}
		return &registry.LifecycleLocks{}, nil
	}
	before, _ := os.ReadFile(reg.Path)
	if err := setupCmd.RunE(setupCmd, []string{target}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("primary reservation ignored: %v", err)
	}
	if calls != 1 {
		t.Fatal("missing acquisition")
	}
	commandLifecycleUnchanged(t, reg, before, target, source)
}

func TestLifecycleSetupAcceptedAndDryRunBoundaries(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(strconv.FormatBool(dry), func(t *testing.T) {
			commandLifecycleEnvironment(t)
			target := commandLifecycleRoot(t)
			setupMainRepo, setupDryRun = target, dry
			calls := 0
			acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
				calls++
				if dry {
					t.Fatal("dry-run acquired execution lock")
				}
				return &registry.LifecycleLocks{}, nil
			}
			if err := setupCmd.RunE(setupCmd, []string{target}); err != nil {
				t.Fatal(err)
			}
			if dry {
				if after, _ := os.ReadFile(filepath.Join(target, config.ProjectConfigFile)); string(after) != lifecycleConfig {
					t.Fatal("dry-run migrated config")
				}
				if _, err := os.Stat(registry.New("").Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("dry-run wrote registry")
				}
			}
			for _, name := range []string{"setup-hook-ran", "setup-command-ran", ".env.local"} {
				_, err := os.Stat(filepath.Join(target, name))
				if dry && !errors.Is(err, os.ErrNotExist) || !dry && err != nil {
					t.Fatalf("effect %s with dry=%v: %v", name, dry, err)
				}
			}
			if !dry && calls != 1 {
				t.Fatal("setup didn't acquire once")
			}
		})
	}
}

func TestLifecycleReleaseRefusesBeforeConfigAndHooks(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, refusal := range []string{"reservation", "corrupt registry", "busy execution"} {
			t.Run(strconv.FormatBool(batch)+"/"+refusal, func(t *testing.T) {
				commandLifecycleEnvironment(t)
				a, b := commandLifecycleRoot(t), commandLifecycleRoot(t)
				reg := registry.New("")
				commandLifecycleAllocate(t, reg, a)
				commandLifecycleAllocate(t, reg, b)
				if refusal == "reservation" {
					reserved := a
					if batch {
						reserved = b
					}
					commandLifecycleReserve(t, reg, reserved, "")
				}
				allocs, err := reg.ReleasePreflight()
				if err != nil {
					t.Fatal(err)
				}
				want := "reserved"
				if refusal != "reservation" {
					if err := os.WriteFile(reg.Path, []byte(`{broken`), 0600); err != nil {
						t.Fatal(err)
					}
					want = "registry"
				}
				calls := 0
				acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
					calls++
					if !slices.Contains(paths, a) || batch && !slices.Contains(paths, b) {
						t.Fatalf("release lock domain: %v", paths)
					}
					if refusal == "busy execution" {
						return nil, errors.New("fixture execution busy")
					}
					return &registry.LifecycleLocks{}, nil
				}
				if refusal == "busy execution" {
					want = "execution busy"
				}
				before, _ := os.ReadFile(reg.Path)
				if batch {
					var count int
					count, err = releaseAndTeardown(reg, allocs, false, false, true)
					if count != 0 {
						t.Fatalf("refusal released %d allocations", count)
					}
				} else {
					err = runReleaseSingle([]string{a})
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("release refusal: %v", err)
				}
				if calls != 1 {
					t.Fatalf("acquisition count: %d", calls)
				}
				commandLifecycleUnchanged(t, reg, before, a, b)
			})
		}
	}
}

func TestLifecycleReleaseAcceptedStopsAtFixtureHook(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(strconv.FormatBool(batch), func(t *testing.T) {
			commandLifecycleEnvironment(t)
			target := commandLifecycleRoot(t)
			if err := os.WriteFile(filepath.Join(target, config.ProjectConfigFile), []byte("project: lifecycle_fixture\nhooks:\n  pre_release:\n    - touch release-hook-ran; exit 1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := registry.New("")
			commandLifecycleAllocate(t, reg, target)
			before, _ := os.ReadFile(reg.Path)
			releaseForce = false
			input, err := os.CreateTemp(t.TempDir(), "confirmation")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = input.WriteString("yes\n")
			_, _ = input.Seek(0, 0)
			oldInput := os.Stdin
			os.Stdin = input
			t.Cleanup(func() { os.Stdin = oldInput; _ = input.Close() })
			if batch {
				allocs, err := reg.ReleasePreflight()
				if err != nil {
					t.Fatal(err)
				}
				if count, err := releaseAndTeardown(reg, allocs, false, false, false); count != 0 || err != nil {
					t.Fatalf("known hook failure behavior changed: %d %v", count, err)
				}
			} else if err := runReleaseSingle([]string{target}); err == nil || !strings.Contains(err.Error(), "release aborted") {
				t.Fatalf("fixture hook wasn't reached: %v", err)
			}
			if _, err := os.Stat(filepath.Join(target, "release-hook-ran")); err != nil {
				t.Fatal("admitted release did not reach fixture hook")
			}
			if after, _ := os.ReadFile(reg.Path); !bytes.Equal(before, after) {
				t.Fatal("fixture hook failure did not preserve allocation")
			}
		})
	}
}

func TestLifecycleReleaseDryRunAndEmptyBatchDoNotAcquire(t *testing.T) {
	commandLifecycleEnvironment(t)
	target := commandLifecycleRoot(t)
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, target)
	before, _ := os.ReadFile(reg.Path)
	acquireCommandLifecycleLocks = func(context.Context, []string) (*registry.LifecycleLocks, error) {
		t.Fatal("nonwriting path acquired lifecycle lock")
		return nil, nil
	}
	releaseDryRun = true
	if err := runReleaseSingle([]string{target}); err != nil {
		t.Fatal(err)
	}
	if count, err := releaseAndTeardown(reg, nil, true, true, true); count != 0 || err != nil {
		t.Fatalf("empty batch: %d %v", count, err)
	}
	commandLifecycleUnchanged(t, reg, before, target)
}

func commandFreshEnrollment(t *testing.T, reg *registry.Registry, target registry.TargetIdentity, allocation registry.Allocation) registry.Allocation {
	t.Helper()
	locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{target.Path})
	if err != nil {
		t.Fatal(err)
	}
	defer locks.Close()
	enrolled, err := reg.EnrollFreshAllocation(locks, target, allocation)
	if err != nil {
		t.Fatal(err)
	}
	return enrolled
}
