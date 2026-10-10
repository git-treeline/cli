package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/github"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
)

func creationFixture(t *testing.T) (string, string) {
	t.Helper()
	commandLifecycleEnvironment(t)
	oldBase, oldPath := newBase, newPath
	oldStart, oldOpen, oldDry, oldForce, oldNoSetup, oldStrict := newStart, newOpen, newDryRun, newForce, newNoSetup, newStrict
	oldClaimPath, oldClaimNoSetup := claimPath, claimNoSetup
	oldReviewPath, oldReviewStart, oldReviewOpen, oldReviewNoSetup, oldLookup := reviewPath, reviewStart, reviewOpen, reviewNoSetup, lookupReviewPR
	newBase, newStart, newOpen, newDryRun, newForce, newNoSetup, newStrict = "main", false, false, false, true, true, false
	claimNoSetup, reviewNoSetup, reviewStart, reviewOpen = true, true, false, false
	lookupReviewPR = func(int) (*github.PRInfo, error) { return &github.PRInfo{HeadRefName: "feature"}, nil }
	t.Cleanup(func() {
		newBase, newPath = oldBase, oldPath
		newStart, newOpen, newDryRun, newForce, newNoSetup, newStrict = oldStart, oldOpen, oldDry, oldForce, oldNoSetup, oldStrict
		claimPath, claimNoSetup = oldClaimPath, oldClaimNoSetup
		reviewPath, reviewStart, reviewOpen, reviewNoSetup, lookupReviewPR = oldReviewPath, oldReviewStart, oldReviewOpen, oldReviewNoSetup, oldLookup
	})
	root, err := registry.CanonicalLifecycleTarget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "repo")
	if err := os.Mkdir(main, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(main, config.ProjectConfigFile), []byte(lifecycleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "add", config.ProjectConfigFile)
	runGit(t, main, "commit", "-m", "fixture")
	t.Chdir(main)
	destination := filepath.Join(main, "trees", "feature")
	newPath, claimPath, reviewPath = destination, destination, destination
	return main, destination
}

func TestManagedDevCreationSelectionBeforeEffects(t *testing.T) {
	for _, variant := range []string{"contained", "unmapped branch", "outside target", "setup hook", "drifted pool"} {
		t.Run(variant, func(t *testing.T) {
			commandLifecycleEnvironment(t)
			f := appDevFixture(t)
			t.Setenv("HOME", f.home)
			t.Setenv("GTL_HOME", f.state)
			t.Setenv("TREELINE_MANAGED", "app")
			t.Setenv("TREELINE_CHANNEL", "dev")
			privateAppDevState(t, f, `{"version":2,"allocations":[]}`)
			stable := filepath.Join(f.home, "Library", "Application Support", "git-treeline")
			writeIntrospectionFile(t, filepath.Join(stable, "config.json"), `{"port":{"base":3002,"increment":10,"reservations":{"@treeline-dev-qa/slot-01":42000,"@treeline-dev-qa/slot-02":42010,"@treeline-dev-qa/slot-03":42020,"@treeline-dev-qa/slot-04":42030}}}`)
			writeIntrospectionFile(t, filepath.Join(stable, "registry.json"), `{"version":2,"allocations":[]}`)
			writeIntrospectionFile(t, filepath.Join(f.state, "config.json"), `{"port":{"increment":10,"reservations":{"fixture":42000,"fixture/qa-one":42010}},"router":{"mode":"disabled"}}`)
			if err := os.Chmod(filepath.Join(f.state, "config.json"), 0600); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(f.home, ".treeline-dev", "workspaces", "repo")
			if err := os.MkdirAll(source, 0700); err != nil {
				t.Fatal(err)
			}
			runGit(t, source, "init", "-b", "main")
			project := "project: fixture\nport_count: 1\nenv: {PORT: '{port}'}\n"
			if variant == "setup hook" {
				project += "hooks: {pre_setup: 'touch escaped'}\n"
			}
			writeIntrospectionFile(t, filepath.Join(source, config.ProjectConfigFile), project)
			runGit(t, source, "add", config.ProjectConfigFile)
			runGit(t, source, "commit", "-m", "fixture")
			branch := "qa-one"
			target := filepath.Join(filepath.Dir(source), "repo-qa-one")
			if variant == "unmapped branch" {
				branch = "unmapped"
			}
			if variant == "outside target" {
				target = filepath.Join(f.home, "ordinary-worktree")
			}
			if variant == "drifted pool" {
				writeIntrospectionFile(t, filepath.Join(stable, "config.json"), `{"port":{"increment":10}}`)
			}
			before := snapshotIntrospection(t, f.base)
			selection, err := discoverCreation(source, branch, false, func(string, *config.ProjectConfig, *config.UserConfig) string { return target })
			if variant == "contained" {
				if err != nil || selection.source != source || selection.target != target {
					t.Fatalf("contained selection refused: %+v %v", selection, err)
				}
			} else if err == nil {
				t.Fatalf("unsafe selection accepted: %+v", selection)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}

func runCreationCommand(name string) error {
	switch name {
	case "new":
		return newCmd.RunE(newCmd, []string{"feature"})
	case "claim":
		return claimCmd.RunE(claimCmd, []string{"feature"})
	default:
		return reviewCmd.RunE(reviewCmd, []string{"42"})
	}
}

func assertCreationRefused(t *testing.T, main, destination string, reg *registry.Registry, before []byte) {
	t.Helper()
	if after, err := os.ReadFile(filepath.Join(main, config.ProjectConfigFile)); err != nil || string(after) != lifecycleConfig {
		t.Fatalf("refused command migrated source config: %v", err)
	}
	for _, path := range []string{filepath.Join(main, ".gitignore"), filepath.Join(main, ".git", "FETCH_HEAD"), filepath.Join(destination, "setup-hook-ran"), filepath.Join(destination, "setup-command-ran")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refused command wrote %s: %v", path, err)
		}
	}
	if after, err := os.ReadFile(reg.Path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refused command rewrote registry: %v", err)
	}
}

func TestCreationLifecycleRefusesBeforeEffects(t *testing.T) {
	for _, command := range []string{"new", "claim", "review"} {
		for _, reason := range []string{"source reservation", "existing reservation", "destination reservation", "corrupt registry", "busy"} {
			t.Run(command+"/"+reason, func(t *testing.T) {
				main, destination := creationFixture(t)
				reg := registry.New("")
				commandLifecycleAllocate(t, reg, main)
				actual := destination
				want := "reserved"
				switch reason {
				case "source reservation":
					commandLifecycleReserve(t, reg, main, filepath.Join(main, ".git"))
				case "existing reservation":
					actual = filepath.Join(filepath.Dir(main), "actual-existing")
					runGit(t, main, "worktree", "add", "-b", "feature", actual)
					commandLifecycleAllocate(t, reg, actual)
					commandLifecycleReserve(t, reg, actual, filepath.Join(main, ".git"))
				case "destination reservation":
					actual = commandLifecycleRoot(t)
					newPath, claimPath, reviewPath = actual, actual, actual
					commandLifecycleAllocate(t, reg, actual)
					commandLifecycleReserve(t, reg, actual, "")
				case "corrupt registry", "busy":
					if err := os.WriteFile(reg.Path, []byte(`{broken`), 0600); err != nil {
						t.Fatal(err)
					}
					want = "registry"
				}
				calls := 0
				acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
					calls++
					if len(paths) != 2 || !slices.Contains(paths, main) || !slices.Contains(paths, actual) {
						t.Fatalf("wrong target set %v; actual %s", paths, actual)
					}
					if reason == "busy" {
						return nil, errors.New("fixture busy")
					}
					return &registry.LifecycleLocks{}, nil
				}
				if reason == "busy" {
					want = "fixture busy"
				}
				before, _ := os.ReadFile(reg.Path)
				if err := runCreationCommand(command); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %s refusal: %v", reason, err)
				}
				if calls != 1 {
					t.Fatalf("nested or missing acquisition: %d", calls)
				}
				assertCreationRefused(t, main, actual, reg, before)
				if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("predicted destination was created: %v", err)
				}
			})
		}
	}
}

func TestCreationLifecycleRechecksDestinationUnderLock(t *testing.T) {
	for _, command := range []string{"new", "claim", "review"} {
		for _, changed := range []string{"branch target", "configured path"} {
			t.Run(command+"/"+changed, func(t *testing.T) {
				main, destination := creationFixture(t)
				reg := registry.New("")
				commandLifecycleAllocate(t, reg, main)
				before, _ := os.ReadFile(reg.Path)
				calls := 0
				acquireCommandLifecycleLocks = func(context.Context, []string) (*registry.LifecycleLocks, error) {
					calls++
					if changed == "branch target" {
						runGit(t, main, "worktree", "add", "-b", "feature", filepath.Join(filepath.Dir(main), "new-existing"))
					} else {
						newPath, claimPath, reviewPath = destination+"-changed", destination+"-changed", destination+"-changed"
					}
					return &registry.LifecycleLocks{}, nil
				}
				if err := runCreationCommand(command); err == nil || !strings.Contains(err.Error(), "destination changed") {
					t.Fatalf("changed destination accepted: %v", err)
				}
				if calls != 1 {
					t.Fatal("changed destination caused reacquisition")
				}
				assertCreationRefused(t, main, destination, reg, before)
			})
		}
	}
}

func TestCreationLifecycleReviewLinkedSubdirectory(t *testing.T) {
	main, _ := creationFixture(t)
	linked := filepath.Join(filepath.Dir(main), "linked")
	runGit(t, main, "worktree", "add", "-b", "current", linked)
	child := filepath.Join(linked, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	reg := registry.New("")
	commandLifecycleAllocate(t, reg, linked)
	commandLifecycleReserve(t, reg, linked, filepath.Join(main, ".git"))
	acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
		if !slices.Contains(paths, main) || !slices.Contains(paths, linked) || slices.Contains(paths, child) {
			t.Fatalf("review locked subdirectory instead of tree: %v", paths)
		}
		return &registry.LifecycleLocks{}, nil
	}
	if err := runCreationCommand("review"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("in-place review ignored reservation: %v", err)
	}
	if branch := runGit(t, linked, "branch", "--show-current"); branch != "current" {
		t.Fatalf("refused review checked out %s", branch)
	}
	if after, _ := os.ReadFile(filepath.Join(linked, config.ProjectConfigFile)); string(after) != lifecycleConfig {
		t.Fatal("refused review migrated target config")
	}
}

func TestCreationLifecycleReviewPreservesWhitespaceTarget(t *testing.T) {
	main, _ := creationFixture(t)
	linked := filepath.Join(filepath.Dir(main), "linked \n")
	runGit(t, main, "worktree", "add", "-b", "current", linked)
	child := filepath.Join(linked, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	calls := 0
	acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
		calls++
		if !slices.Contains(paths, linked) || !slices.Contains(paths, main) {
			t.Fatalf("admission changed target bytes: %q", paths)
		}
		return nil, errors.New("fixture busy")
	}
	if err := runCreationCommand("review"); err == nil || !strings.Contains(err.Error(), "fixture busy") || calls != 1 {
		t.Fatalf("whitespace target admission: %v", err)
	}
}

func TestCreationLifecycleGitFailureNeverGuessesTarget(t *testing.T) {
	for _, command := range []string{"new", "claim", "review"} {
		t.Run(command, func(t *testing.T) {
			main, _ := creationFixture(t)
			linked := filepath.Join(filepath.Dir(main), "linked")
			runGit(t, main, "worktree", "add", "-b", "current", linked)
			child := filepath.Join(linked, "child")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: missing-admin-directory\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(child)
			acquireCommandLifecycleLocks = func(context.Context, []string) (*registry.LifecycleLocks, error) {
				t.Fatal("failed Git discovery acquired guessed target")
				return nil, nil
			}
			if err := runCreationCommand(command); err == nil {
				t.Fatal("broken Git identity accepted")
			}
			if after, _ := os.ReadFile(filepath.Join(linked, config.ProjectConfigFile)); string(after) != lifecycleConfig {
				t.Fatal("failed Git discovery migrated config")
			}
		})
	}
}

func TestCreationLifecycleConfiglessInitChangedDestination(t *testing.T) {
	main, destination := creationFixture(t)
	if err := os.Remove(filepath.Join(main, config.ProjectConfigFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "next.config.js"), []byte("export default {}"), 0600); err != nil {
		t.Fatal(err)
	}
	// Read-only discovery uses the directory name; init uses the remote name.
	runGit(t, main, "remote", "add", "origin", filepath.Join(filepath.Dir(main), "different-project.git"))
	newPath = ""
	if err := runCreationCommand("new"); err == nil || !strings.Contains(err.Error(), "destination changed") {
		t.Fatalf("init changed destination without refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(main, config.ProjectConfigFile)); err != nil {
		t.Fatal("guarded source init was not performed")
	}
	for _, path := range []string{destination, filepath.Join(filepath.Dir(main), "repo-feature"), filepath.Join(filepath.Dir(main), "different_project-feature"), filepath.Join(main, ".gitignore")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("changed destination had effects: %s: %v", path, err)
		}
	}
}

func TestCreationLifecycleLocalCreationSuppressesOnlyAutomaticSetup(t *testing.T) {
	for _, command := range []string{"new", "claim", "review"} {
		for _, noSetup := range []bool{false, true} {
			t.Run(command+"/"+map[bool]string{true: "no-setup", false: "parent-setup"}[noSetup], func(t *testing.T) {
				main, destination := creationFixture(t)
				newNoSetup, claimNoSetup, reviewNoSetup = noSetup, noSetup, noSetup
				if command != "new" {
					runGit(t, main, "branch", "feature")
					runGit(t, main, "remote", "add", "origin", main)
				}
				hook := "#!/bin/sh\ntouch unrelated-hook-ran\nif [ \"$GTL_DEFER_SETUP\" != 1 ]; then touch unsafe-auto-setup; exit 1; fi\n"
				hookPath := filepath.Join(main, ".git", "hooks", "post-checkout")
				if err := os.WriteFile(hookPath, []byte(hook), 0700); err != nil {
					t.Fatal(err)
				}
				calls := 0
				acquireCommandLifecycleLocks = func(ctx context.Context, paths []string) (*registry.LifecycleLocks, error) {
					calls++
					if ctx == nil {
						ctx = context.Background()
					}
					return registry.AcquireLifecycleLocks(ctx, paths)
				}
				if err := runCreationCommand(command); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatal("setup recursively acquired execution locks")
				}
				if _, err := os.Stat(filepath.Join(destination, "unrelated-hook-ran")); err != nil {
					t.Fatal("unrelated post-checkout hook was suppressed")
				}
				if _, err := os.Stat(filepath.Join(destination, "unsafe-auto-setup")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("legacy automatic setup was not deferred")
				}
				_, err := os.Stat(filepath.Join(destination, "setup-command-ran"))
				if noSetup && !errors.Is(err, os.ErrNotExist) || !noSetup && err != nil {
					t.Fatalf("parent setup policy ignored: %v", err)
				}
			})
		}
	}
}

func TestCreationLifecycleExistingRegistryCompatibility(t *testing.T) {
	for _, version := range []int{2, 3, 4} {
		for _, command := range []string{"new", "claim", "review"} {
			t.Run(fmt.Sprintf("schema-%d/%s", version, command), func(t *testing.T) {
				main, destination := creationFixture(t)
				t.Setenv("TREELINE_MANAGED", "")
				t.Setenv("TREELINE_CHANNEL", "")
				acquireCommandLifecycleLocks = func(ctx context.Context, paths []string) (*registry.LifecycleLocks, error) {
					if ctx == nil {
						ctx = context.Background()
					}
					return registry.AcquireLifecycleLocks(ctx, paths)
				}
				newNoSetup, claimNoSetup, reviewNoSetup = false, false, false
				if command != "new" {
					runGit(t, main, "branch", "feature")
					runGit(t, main, "remote", "add", "origin", main)
				}
				reg := registry.New("")
				existing := registry.Allocation{
					"worktree": main, "project": "lifecycle_fixture", "branch": "main", "main_worktree": true,
					"port": 42100, "ports": []int{42100}, "database": "retained_database", "redis_db": 7,
				}
				if version >= 3 {
					existing["allocation_generation"] = uuid.NewString()
				}
				if version == 4 {
					existing["runtime_binding"] = registry.RuntimeBinding{Kind: "unknown"}
				}
				raw, err := json.Marshal(registry.RegistryData{Version: version, Allocations: []registry.Allocation{existing}})
				if err != nil {
					t.Fatal(err)
				}
				writeIntrospectionFile(t, reg.Path, string(raw))
				before, err := reg.FindForRelease(main)
				if err != nil {
					t.Fatal(err)
				}
				if err := runCreationCommand(command); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"setup-hook-ran", "setup-command-ran", config.LoadProjectConfigReadOnly(destination).EnvFileTarget()} {
					if _, err := os.Stat(filepath.Join(destination, name)); err != nil {
						t.Fatalf("creation did not complete %s: %v", name, err)
					}
				}
				allocation, err := reg.FindForRelease(destination)
				if err != nil || allocation == nil || slices.Contains(registry.ExtractPorts(allocation), 42100) {
					t.Fatalf("new allocation missing or reuses existing port: %v: %v", allocation, err)
				}
				binding, err := registry.AllocationRuntimeBinding(allocation)
				wantBinding := "unknown"
				if version == 4 {
					wantBinding = "never_started"
				}
				if err != nil || binding.Kind != wantBinding {
					t.Fatalf("runtime binding = %+v, want %s: %v", binding, wantBinding, err)
				}
				if err := runReleaseSingle([]string{destination}); err != nil {
					t.Fatal(err)
				}
				if allocation, err := reg.FindForRelease(destination); err != nil || allocation != nil {
					t.Fatalf("release retained allocation: %v: %v", allocation, err)
				}
				if _, err := os.Stat(filepath.Join(destination, "release-hook-ran")); err != nil {
					t.Fatalf("release hook did not run: %v", err)
				}
				after, err := reg.FindForRelease(main)
				if err != nil {
					t.Fatal(err)
				}
				if version == 2 {
					delete(after, "allocation_generation")
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("existing resources changed: before=%v after=%v", before, after)
				}
				snapshot, err := reg.SnapshotStrict()
				wantVersion := max(version, 3)
				if err != nil || snapshot.Version != wantVersion {
					t.Fatalf("registry version = %d, want %d: %v", snapshot.Version, wantVersion, err)
				}
			})
		}
	}
}

func TestCreationLifecycleFreshEnrollmentUpgradesLegacyWithoutAdoptingRuntime(t *testing.T) {
	for _, version := range []int{2, 3} {
		for _, command := range []string{"host-claim", "allocate-only"} {
			t.Run(fmt.Sprintf("schema-%d/%s", version, command), func(t *testing.T) {
				var main, destination string
				if command == "allocate-only" {
					main, destination = allocationOnlyFixture(t)
				} else {
					main, destination = creationFixture(t)
				}
				t.Setenv("TREELINE_MANAGED", "")
				t.Setenv("TREELINE_CHANNEL", "")
				reg := registry.New("")
				existing := registry.Allocation{"worktree": main, "project": "lifecycle_fixture", "port": 42100, "database": "retained_database", "redis_db": 7}
				if version == 3 {
					existing["allocation_generation"] = uuid.NewString()
				}
				raw, err := json.Marshal(registry.RegistryData{Version: version, Allocations: []registry.Allocation{existing}})
				if err != nil {
					t.Fatal(err)
				}
				writeIntrospectionFile(t, reg.Path, string(raw))
				before, err := reg.FindForRelease(main)
				if err != nil {
					t.Fatal(err)
				}
				if command == "allocate-only" {
					if err := newCmd.RunE(newCmd, []string{"feature"}); err != nil {
						t.Fatal(err)
					}
					if _, err := os.Stat(filepath.Join(destination, "setup-command-ran")); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("allocation-only ran setup: %v", err)
					}
				} else {
					runGit(t, main, "worktree", "add", "-b", "feature", destination)
					locks, err := registry.AcquireLifecycleLocks(context.Background(), []string{main, destination})
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := locks.Close(); err != nil {
							t.Error(err)
						}
					}()
					request := hostClaimRequest{Source: registry.TargetIdentity{Path: main}, Target: hostClaimTarget{Path: destination}}
					outcome, err := hostClaimSetup(claimCmd, request, config.LoadUserConfig(""), locks)
					if err != nil || outcome != "retained" {
						t.Fatalf("host claim failed: outcome=%s err=%v", outcome, err)
					}
					if _, err := os.Stat(filepath.Join(destination, "setup-command-ran")); err != nil {
						t.Fatalf("host claim did not complete setup: %v", err)
					}
				}
				snapshot, err := reg.SnapshotStrict()
				if err != nil || snapshot.Version != 4 || len(snapshot.Allocations) != 2 {
					t.Fatalf("host claim did not enroll fresh target: %+v: %v", snapshot, err)
				}
				fresh, err := reg.FindForRelease(destination)
				if err != nil || fresh == nil || slices.Contains(registry.ExtractPorts(fresh), 42100) {
					t.Fatalf("fresh allocation missing or reused legacy port: %+v: %v", fresh, err)
				}
				binding, err := registry.AllocationRuntimeBinding(fresh)
				if err != nil || binding.Kind != "never_started" {
					t.Fatalf("fresh runtime ownership missing: %+v: %v", binding, err)
				}
				retained, err := reg.FindForRelease(main)
				if err != nil {
					t.Fatal(err)
				}
				binding, err = registry.AllocationRuntimeBinding(retained)
				if err != nil || binding.Kind != "unknown" || registry.GetString(retained, "allocation_generation") == "" {
					t.Fatalf("legacy runtime ownership adopted: %+v: %v", retained, err)
				}
				delete(retained, "runtime_binding")
				if version == 2 {
					delete(retained, "allocation_generation")
				}
				if !reflect.DeepEqual(before, retained) {
					t.Fatalf("legacy allocation changed: before=%v after=%v", before, retained)
				}
			})
		}
	}
}

func TestCreationLifecyclePreservesReuseAndConfiglessSemantics(t *testing.T) {
	for _, command := range []string{"new", "claim", "review"} {
		t.Run(command, func(t *testing.T) {
			main, destination := creationFixture(t)
			runGit(t, main, "worktree", "add", "-b", "feature", destination)
			runGit(t, main, "remote", "add", "origin", main)
			if err := runCreationCommand(command); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"setup-hook-ran", "setup-command-ran"} {
				_, err := os.Stat(filepath.Join(destination, name))
				if command == "new" && !errors.Is(err, os.ErrNotExist) || command != "new" && err != nil {
					t.Fatalf("existing %s reuse semantics changed: %v", command, err)
				}
			}
			_, err := os.Stat(registry.New("").Path)
			if command == "new" && !errors.Is(err, os.ErrNotExist) || command != "new" && err != nil {
				t.Fatalf("existing %s allocation behavior changed: %v", command, err)
			}
		})
	}
	t.Run("configless new", func(t *testing.T) {
		main, destination := creationFixture(t)
		runGit(t, main, "rm", config.ProjectConfigFile)
		runGit(t, main, "commit", "-m", "configless")
		hook := "#!/bin/sh\ntouch unrelated-hook-ran\n[ \"$GTL_DEFER_SETUP\" = 1 ] || exit 1\n"
		if err := os.WriteFile(filepath.Join(main, ".git", "hooks", "post-checkout"), []byte(hook), 0700); err != nil {
			t.Fatal(err)
		}
		newNoSetup = false
		if err := runCreationCommand("new"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(destination, "unrelated-hook-ran")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(registry.New("").Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("configless create unexpectedly allocated")
		}
	})
}

func TestCreationLifecycleReviewSwitchDefersLegacySetup(t *testing.T) {
	main, _ := creationFixture(t)
	linked := filepath.Join(filepath.Dir(main), "linked")
	runGit(t, main, "worktree", "add", "-b", "current", linked)
	runGit(t, main, "branch", "feature")
	runGit(t, main, "remote", "add", "origin", main)
	if err := os.WriteFile(filepath.Join(os.Getenv("GTL_HOME"), "config.json"), []byte(`{"review":{"skip_switch_confirm":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\ntouch unrelated-hook-ran\n[ \"$GTL_DEFER_SETUP\" = 1 ] || exit 1\n"
	if err := os.WriteFile(filepath.Join(main, ".git", "hooks", "post-checkout"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(linked)
	if err := runCreationCommand("review"); err != nil {
		t.Fatal(err)
	}
	if branch := runGit(t, linked, "branch", "--show-current"); branch != "feature" {
		t.Fatalf("review didn't switch: %s", branch)
	}
	if _, err := os.Stat(filepath.Join(linked, "unrelated-hook-ran")); err != nil {
		t.Fatal("review suppressed unrelated hook")
	}
	if _, err := os.Stat(filepath.Join(linked, "setup-command-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("review refresh-only ran setup commands")
	}
}
