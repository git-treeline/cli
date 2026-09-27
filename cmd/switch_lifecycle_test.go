package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/github"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
)

func switchLifecycleFixture(t *testing.T) (string, string) {
	t.Helper()
	main, _ := creationFixture(t)
	oldSetup, oldRestart, oldLookup, oldConfigure := switchRunSetup, switchRestart, lookupSwitchPR, configureSwitchSupervisor
	switchRunSetup, switchRestart = false, false
	lookupSwitchPR = func(int) (*github.PRInfo, error) { t.Fatal("unexpected PR lookup"); return nil, nil }
	configureSwitchSupervisor = func(string, string, map[string]string, int) (string, error) {
		t.Fatal("unexpected restart")
		return "", nil
	}
	t.Cleanup(func() {
		switchRunSetup, switchRestart, lookupSwitchPR, configureSwitchSupervisor = oldSetup, oldRestart, oldLookup, oldConfigure
	})
	linked := filepath.Join(filepath.Dir(main), "linked space\n")
	runGit(t, main, "worktree", "add", "-b", "current", linked)
	runGit(t, main, "branch", "feature")
	runGit(t, main, "remote", "add", "origin", main)
	child := filepath.Join(linked, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	return main, linked
}

func TestSwitchLifecycleRefusesBeforeEffects(t *testing.T) {
	for _, reason := range []string{"target reservation", "main reservation", "corrupt registry", "busy"} {
		t.Run(reason, func(t *testing.T) {
			main, linked := switchLifecycleFixture(t)
			reg := registry.New("")
			commandLifecycleAllocate(t, reg, linked)
			want := "reserved"
			switch reason {
			case "target reservation":
				commandLifecycleReserve(t, reg, linked, filepath.Join(main, ".git"))
			case "main reservation":
				commandLifecycleAllocate(t, reg, main)
				commandLifecycleReserve(t, reg, main, filepath.Join(main, ".git"))
			case "corrupt registry":
				if err := os.WriteFile(reg.Path, []byte(`{broken`), 0600); err != nil {
					t.Fatal(err)
				}
				want = "registry"
			case "busy":
				want = "fixture busy"
			}
			calls := 0
			acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
				calls++
				if len(paths) != 2 || !slices.Contains(paths, main) || !slices.Contains(paths, linked) {
					t.Fatalf("wrong lock paths: %q", paths)
				}
				if reason == "busy" {
					return nil, errors.New("fixture busy")
				}
				return &registry.LifecycleLocks{}, nil
			}
			before, _ := os.ReadFile(reg.Path)
			switchRunSetup, switchRestart = true, true
			if err := switchCmd.RunE(switchCmd, []string{"feature"}); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("missing refusal %s: %v", want, err)
			}
			if calls != 1 {
				t.Fatalf("acquisitions: %d", calls)
			}
			commandLifecycleUnchanged(t, reg, before, main, linked)
			if branch := runGit(t, linked, "branch", "--show-current"); branch != "current" {
				t.Fatalf("checkout happened: %s", branch)
			}
			if _, err := os.Stat(filepath.Join(main, ".git", "FETCH_HEAD")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("fetch happened: %v", err)
			}
		})
	}
}

func TestSwitchLifecycleStrictDiscovery(t *testing.T) {
	for _, location := range []string{"main", "main subdirectory", "non Git", "broken Git", "changed under lock"} {
		t.Run(location, func(t *testing.T) {
			main, linked := switchLifecycleFixture(t)
			reg := registry.New("")
			commandLifecycleAllocate(t, reg, linked)
			before, _ := os.ReadFile(reg.Path)
			switch location {
			case "main":
				t.Chdir(main)
			case "main subdirectory":
				child := filepath.Join(main, "child")
				if err := os.Mkdir(child, 0700); err != nil {
					t.Fatal(err)
				}
				t.Chdir(child)
			case "non Git":
				t.Chdir(t.TempDir())
			case "broken Git":
				if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			acquireCommandLifecycleLocks = func(context.Context, []string) (*registry.LifecycleLocks, error) {
				calls++
				if location != "changed under lock" {
					t.Fatal("invalid location acquired locks")
				}
				if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
				return &registry.LifecycleLocks{}, nil
			}
			arg := "42"
			if location == "changed under lock" {
				arg = "feature"
			}
			if err := switchCmd.RunE(switchCmd, []string{arg}); err == nil {
				t.Fatal("invalid discovery accepted")
			}
			if location == "changed under lock" && calls != 1 {
				t.Fatalf("acquisitions: %d", calls)
			}
			commandLifecycleUnchanged(t, reg, before, main, linked)
			if _, err := os.Stat(filepath.Join(main, ".git", "FETCH_HEAD")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("fetch happened: %v", err)
			}
		})
	}
}

func TestSwitchLifecycleBranchPRSetupAndRestart(t *testing.T) {
	for _, arg := range []string{"feature", "42", "#42"} {
		t.Run(arg, func(t *testing.T) {
			main, linked := switchLifecycleFixture(t)
			acquisitions, lookups, restarts := 0, 0, 0
			acquireCommandLifecycleLocks = func(_ context.Context, paths []string) (*registry.LifecycleLocks, error) {
				acquisitions++
				if len(paths) != 2 || !slices.Contains(paths, main) || !slices.Contains(paths, linked) {
					t.Fatalf("wrong lock paths: %q", paths)
				}
				return &registry.LifecycleLocks{}, nil
			}
			lookupSwitchPR = func(number int) (*github.PRInfo, error) {
				lookups++
				if number != 42 {
					t.Fatalf("wrong PR: %d", number)
				}
				return &github.PRInfo{HeadRefName: "feature"}, nil
			}
			switchRunSetup, switchRestart = arg == "#42", true
			configureSwitchSupervisor = func(socket, action string, env map[string]string, port int) (string, error) {
				restarts++
				if acquisitions != 1 || socket != supervisor.SocketPath(linked) || action != "restart" || env["PORT"] == "" || port <= 0 {
					t.Fatalf("wrong restart context: %s %s %v %d", socket, action, env, port)
				}
				if runGit(t, linked, "branch", "--show-current") != "feature" {
					t.Fatal("restart preceded checkout")
				}
				if arg == "42" {
					return "", errors.New("fixture stopped server")
				}
				return "ok", nil
			}
			hook := "#!/bin/sh\ntouch unrelated-hook-ran\n[ \"$GTL_DEFER_SETUP\" = 1 ] || exit 1\n"
			if err := os.WriteFile(filepath.Join(main, ".git", "hooks", "post-checkout"), []byte(hook), 0700); err != nil {
				t.Fatal(err)
			}
			if err := switchCmd.RunE(switchCmd, []string{arg}); err != nil {
				t.Fatal(err)
			}
			if acquisitions != 1 || restarts != 1 {
				t.Fatalf("nested/missing guard or restart: %d %d", acquisitions, restarts)
			}
			wantLookups := 1
			if arg == "feature" {
				wantLookups = 0
			}
			if lookups != wantLookups {
				t.Fatalf("lookups: %d", lookups)
			}
			if _, err := os.Stat(filepath.Join(linked, "unrelated-hook-ran")); err != nil {
				t.Fatal("unrelated hook suppressed", err)
			}
			_, setupErr := os.Stat(filepath.Join(linked, "setup-command-ran"))
			if switchRunSetup && setupErr != nil || !switchRunSetup && !errors.Is(setupErr, os.ErrNotExist) {
				t.Fatalf("setup flag behavior changed: %v", setupErr)
			}
		})
	}
}
