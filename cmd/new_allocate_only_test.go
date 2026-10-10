package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
)

func allocationOnlyFixture(t *testing.T) (string, string) {
	t.Helper()
	source, target := creationFixture(t)
	acquireCommandLifecycleLocks = func(ctx context.Context, paths []string) (*registry.LifecycleLocks, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		return registry.AcquireLifecycleLocks(ctx, paths)
	}
	prior := newAllocateOnly
	priorCompletion := setupCompleteAllocation
	setupCompleteAllocation = ""
	t.Cleanup(func() { setupCompleteAllocation = priorCompletion })
	newAllocateOnly, newNoSetup = true, false
	t.Cleanup(func() { newAllocateOnly = prior })
	return source, target
}

func TestNewAllocateOnlyThenSetupRetainsFreshRuntime(t *testing.T) {
	source, target := allocationOnlyFixture(t)
	configText := lifecycleConfig + "env_file: .env.local\ncopy_files: [copy-fixture.txt]\ncommands:\n  setup: [touch setup-command-ran]\n  start: touch forbidden-server-start\n"
	// Replace rather than duplicate the fixture's commands mapping.
	configText = strings.Replace(configText, "commands:\n  setup:\n    - touch setup-command-ran\n", "", 1)
	writeFile(t, filepath.Join(source, config.ProjectConfigFile), configText)
	writeFile(t, filepath.Join(source, "copy-fixture.txt"), "synthetic copy")
	runGit(t, source, "add", config.ProjectConfigFile)
	runGit(t, source, "commit", "-m", "allocation-only fixture")
	if err := newCmd.RunE(newCmd, []string{"feature"}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New("")
	before, err := reg.FindForRelease(target)
	if err != nil || before == nil {
		t.Fatalf("allocation unavailable: %v", err)
	}
	generation := registry.GetString(before, "allocation_generation")
	binding, err := registry.AllocationRuntimeBinding(before)
	if err != nil || generation == "" || binding.Kind != "never_started" {
		t.Fatalf("fresh ownership missing: %+v %v", binding, err)
	}
	for _, name := range []string{".env.local", "copy-fixture.txt", "setup-hook-ran", "setup-command-ran", "forbidden-server-start"} {
		if _, err := os.Stat(filepath.Join(target, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("allocation ran effect %s: %v", name, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(target, config.ProjectConfigFile)); err != nil || string(content) != configText {
		t.Fatal("allocation migrated config", err)
	}
	if err := setupCmd.RunE(setupCmd, []string{target}); err != nil {
		t.Fatal(err)
	}
	after, err := reg.FindForRelease(target)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := registry.AllocationRuntimeBinding(after)
	if err != nil || retained != binding || registry.GetString(after, "allocation_generation") != generation {
		t.Fatal("setup changed ownership", err)
	}
	tracked, err := hasTrackedRuntime(reg, target)
	if err != nil || !tracked {
		t.Fatalf("runtime guard rejected fresh setup: %v", err)
	}
	for _, name := range []string{".env.local", "copy-fixture.txt", "setup-hook-ran", "setup-command-ran"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatalf("setup omitted %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "forbidden-server-start")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("setup started runtime")
	}
}

func TestNewAllocateOnlyRefusesExistingBeforeEffects(t *testing.T) {
	for _, variant := range []string{"checkout", "directory", "invalid config", "missing config"} {
		t.Run(variant, func(t *testing.T) {
			source, target := allocationOnlyFixture(t)
			switch variant {
			case "checkout":
				runGit(t, source, "worktree", "add", "-b", "feature", target)
			case "directory":
				if err := os.MkdirAll(target, 0700); err != nil {
					t.Fatal(err)
				}
			case "invalid config":
				writeFile(t, filepath.Join(source, config.ProjectConfigFile), "project: invalid-project!\n")
			case "missing config":
				if err := os.Remove(filepath.Join(source, config.ProjectConfigFile)); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(source, config.ProjectConfigFile))
			if err := newCmd.RunE(newCmd, []string{"feature"}); err == nil {
				t.Fatal("unsafe allocation accepted")
			}
			after, _ := os.ReadFile(filepath.Join(source, config.ProjectConfigFile))
			if !bytes.Equal(before, after) {
				t.Fatal("refusal changed source config")
			}
			if _, err := os.Stat(registry.New("").Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refusal allocated resources", err)
			}
			for _, name := range []string{".gitignore", "setup-hook-ran", "setup-command-ran"} {
				if _, err := os.Stat(filepath.Join(source, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("refusal wrote source", name, err)
				}
			}
		})
	}
}

func TestNewAllocateOnlyDryRun(t *testing.T) {
	_, target := allocationOnlyFixture(t)
	newDryRun = true
	var runErr error
	output := captureStdout(t, func() { runErr = newCmd.RunE(newCmd, []string{"feature"}) })
	if runErr != nil || !strings.Contains(output, "Would allocate resource metadata") || strings.Contains(output, "Would run: gtl setup") {
		t.Fatalf("wrong preview: %s %v", output, runErr)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview created target", err)
	}
}

func TestNewAllocateOnlyCompleteSQLiteRetryPreservesDatabase(t *testing.T) {
	source, target := allocationOnlyFixture(t)
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 unavailable")
	}
	query := func(path, sql string) string {
		t.Helper()
		out, err := exec.Command(sqlite, path, sql).CombinedOutput()
		if err != nil {
			t.Fatalf("temporary SQLite fixture: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	cfg := "project: lifecycle_fixture\nport_count: 1\nenv_file: .env.local\nenv: {PORT: '{port}'}\ndatabase:\n  adapter: sqlite\n  template: template.db\n  name: clone_db\ncommands:\n  setup: [test -f setup-allow]\n"
	writeFile(t, filepath.Join(source, config.ProjectConfigFile), cfg)
	runGit(t, source, "add", config.ProjectConfigFile)
	runGit(t, source, "commit", "-m", "sqlite split setup")
	query(filepath.Join(source, "template.db"), "CREATE TABLE proof(value TEXT); INSERT INTO proof VALUES('template');")
	if err := newCmd.RunE(newCmd, []string{"feature"}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New("")
	entry, err := reg.FindForRelease(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(reg.Path)
	if err != nil {
		t.Fatal(err)
	}
	setupCompleteAllocation = registry.GetString(entry, "allocation_generation")
	db := filepath.Join(target, "clone_db")
	if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("allocate-only provisioned database")
	}
	if err := setupCmd.RunE(setupCmd, []string{target}); err == nil {
		t.Fatal("setup fixture must fail after database provision")
	}
	if got := query(db, "SELECT value FROM proof;"); got != "template" {
		t.Fatal("missing initial clone", got)
	}
	after, err := os.ReadFile(reg.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed setup changed allocation", err)
	}
	query(db, "UPDATE proof SET value='retained';")
	// A retry must skip sync as well as cloning an existing destination. This
	// fixture has no remote, so an accidental template sync would fail.
	cfg = strings.Replace(cfg, "  adapter: sqlite", "  adapter: sqlite\n  sync_on_create: true", 1)
	cfg = strings.Replace(cfg, "  setup: [test -f setup-allow]", "  setup: [test -f setup-allow]\n  migrate: false", 1)
	writeFile(t, filepath.Join(target, config.ProjectConfigFile), cfg)
	writeFile(t, filepath.Join(target, "setup-allow"), "allow")
	if err := setupCmd.RunE(setupCmd, []string{target}); err != nil {
		t.Fatal(err)
	}
	if got := query(db, "SELECT value FROM proof;"); got != "retained" {
		t.Fatal("retry replaced database", got)
	}
	after, err = os.ReadFile(reg.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("completion changed allocation", err)
	}
	if tracked, err := hasTrackedRuntime(reg, target); err != nil || !tracked {
		t.Fatal("completion lost tracked runtime", err)
	}
}

func TestSetupCompleteAllocationRefusesChangedOwnershipAndResources(t *testing.T) {
	for _, variant := range []string{"generation", "unknown", "supervisor", "missing", "project", "ports", "database"} {
		t.Run(variant, func(t *testing.T) {
			_, target := allocationOnlyFixture(t)
			if err := newCmd.RunE(newCmd, []string{"feature"}); err != nil {
				t.Fatal(err)
			}
			reg := registry.New("")
			raw, err := os.ReadFile(reg.Path)
			if err != nil {
				t.Fatal(err)
			}
			var data registry.RegistryData
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			entry := data.Allocations[0]
			setupCompleteAllocation = registry.GetString(entry, "allocation_generation")
			switch variant {
			case "generation":
				setupCompleteAllocation = "00000000-0000-4000-8000-000000000001"
			case "unknown":
				entry["runtime_binding"] = registry.RuntimeBinding{Kind: "unknown"}
			case "supervisor":
				entry["runtime_binding"] = registry.RuntimeBinding{Kind: "supervisor", Generation: "00000000-0000-4000-8000-000000000002"}
			case "missing":
				data.Allocations = nil
			case "project", "ports", "database":
				configPath := filepath.Join(target, config.ProjectConfigFile)
				contents, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				changed := string(contents)
				if variant == "project" {
					changed = strings.Replace(changed, "lifecycle_fixture", "changed_project", 1)
				}
				if variant == "ports" {
					changed = strings.Replace(changed, "ports_needed: 1", "port_count: 2", 1)
				}
				if variant == "database" {
					changed += "database: {adapter: sqlite, template: another.db, name: another_db}\n"
				}
				writeFile(t, configPath, changed)
			}
			raw, err = json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(reg.Path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(target, config.ProjectConfigFile))
			if err := setupCmd.RunE(setupCmd, []string{target}); err == nil {
				t.Fatal("unsafe completion accepted")
			}
			after, _ := os.ReadFile(reg.Path)
			if !bytes.Equal(raw, after) {
				t.Fatal("refused completion mutated allocation")
			}
			after, _ = os.ReadFile(filepath.Join(target, config.ProjectConfigFile))
			if !bytes.Equal(before, after) {
				t.Fatal("refused completion migrated config")
			}
			for _, name := range []string{".env.local", "setup-hook-ran", "setup-command-ran"} {
				if _, err := os.Stat(filepath.Join(target, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("refused completion ran effects", name)
				}
			}
		})
	}
}

func TestNewAllocateOnlyKeepsManagedDevPolicy(t *testing.T) {
	allocationOnlyFixture(t)
	source, reg, _ := managedDevRuntimeFixture(t)
	t.Chdir(source)
	newPath = filepath.Join(filepath.Dir(source), "unmapped-target")
	before, err := os.ReadFile(reg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := newCmd.RunE(newCmd, []string{"unmapped"}); err == nil {
		t.Fatal("allocation-only bypassed managed dev branch mapping")
	}
	after, err := os.ReadFile(reg.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("policy refusal changed allocations", err)
	}
	if _, err := os.Stat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("policy refusal created destination", err)
	}
}

func TestNewAllocateOnlyFlagExclusions(t *testing.T) {
	for _, other := range []string{"no-setup", "start", "open", "strict"} {
		t.Run(other, func(t *testing.T) {
			allocationOnlyFixture(t)
			first, second := newCmd.Flags().Lookup("allocate-only"), newCmd.Flags().Lookup(other)
			oldFirst, oldSecond := first.Changed, second.Changed
			first.Changed, second.Changed = true, true
			t.Cleanup(func() { first.Changed, second.Changed = oldFirst, oldSecond })
			if err := newCmd.ValidateFlagGroups(); err == nil {
				t.Fatal("conflicting allocation-only flag accepted", other)
			}
		})
	}
}
