package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func executeReleaseInspect(t *testing.T, path string, args ...string) (registry.ReleaseRequest, []byte, error) {
	t.Helper()
	var output bytes.Buffer
	root := &cobra.Command{Use: "fixture", SilenceErrors: true, SilenceUsage: true,
		PersistentPreRunE:  func(*cobra.Command, []string) error { t.Fatal("inspect ran root configuration hooks"); return nil },
		PersistentPostRunE: func(*cobra.Command, []string) error { t.Fatal("inspect ran root update hooks"); return nil },
	}
	root.AddCommand(newReleaseInspectCommand(path))
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(append([]string{"inspect"}, args...))
	err := root.Execute()
	var intent registry.ReleaseRequest
	if err == nil {
		if decodeErr := json.Unmarshal(output.Bytes(), &intent); decodeErr != nil {
			t.Fatalf("invalid inspect JSON: %s: %v", output.String(), decodeErr)
		}
	} else if output.Len() != 0 {
		t.Fatalf("refused inspection wrote diagnostics/request: %q", output.String())
	}
	return intent, output.Bytes(), err
}

func TestReleaseInspectCapturesDeterministicSecretFreeIntentWithoutWrites(t *testing.T) {
	e, existing := releaseExecutorFixture(t)
	configPath := filepath.Join(existing.Target.Path, config.ProjectConfigFile)
	content := []byte("project: lifecycle_fixture\nenv:\n  TOKEN: never-emit-credential\nhooks:\n  pre_release: ['echo never-emit-hook-body']\n  post_release: []\n")
	if err := os.WriteFile(configPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Registry.UpdateField(existing.Target.Path, "private_note", "never-emit-allocation-metadata"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(e.Registry.Path)
	if err != nil {
		t.Fatal(err)
	}
	namesBefore, err := os.ReadDir(filepath.Dir(e.Registry.Path))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	first, raw, err := executeReleaseInspect(t, e.Registry.Path, existing.Target.Path, "--operation-id", id, "--drop-db")
	if err != nil {
		t.Fatal(err)
	}
	second, again, err := executeReleaseInspect(t, e.Registry.Path, existing.Target.Path, "--operation-id", id, "--drop-db")
	if err != nil || !reflect.DeepEqual(first, second) || !bytes.Equal(raw, again) {
		t.Fatal("unchanged inspection was not deterministic", err)
	}
	plan, err := loadReleaseHookPlan(existing.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != id || first.Target != existing.Target || first.Allocation != existing.Allocation || first.RuntimeBinding == nil || *first.RuntimeBinding != *existing.RuntimeBinding || first.HooksDigest != plan.digest() || first.DatabaseConfigRevision != commandDatabaseRevision(t, existing.Target.Path) {
		t.Fatalf("wrong captured bindings: %+v", first)
	}
	if first.Policy != (registry.ReleasePolicy{DropDatabases: true}) {
		t.Fatalf("inspection silently forced policy: %+v", first.Policy)
	}
	for _, secret := range []string{"never-emit-credential", "never-emit-hook-body", "never-emit-allocation-metadata"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("inspection leaked configuration/allocation contents")
		}
	}
	after, _ := os.ReadFile(e.Registry.Path)
	afterConfig, _ := os.ReadFile(configPath)
	namesAfter, _ := os.ReadDir(filepath.Dir(e.Registry.Path))
	if !bytes.Equal(before, after) || !bytes.Equal(content, afterConfig) || !reflect.DeepEqual(namesBefore, namesAfter) {
		t.Fatal("inspection wrote registry, config, or registry-directory files")
	}
	noDB, _, err := executeReleaseInspect(t, e.Registry.Path, existing.Target.Path, "--operation-id", uuid.NewString(), "--ignore-hook-failure")
	if err != nil || noDB.DatabaseConfigRevision != "" || noDB.Policy != (registry.ReleasePolicy{IgnoreHookFailure: true}) {
		t.Fatalf("explicit policy changed: %+v %v", noDB, err)
	}
}

func TestReleaseInspectRefusesUnknownLegacyCorruptionAndBadConfig(t *testing.T) {
	for _, fixture := range []string{"legacy", "unknown", "corrupt", "bad_config"} {
		t.Run(fixture, func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			switch fixture {
			case "legacy":
				raw, _ := json.Marshal(map[string]any{"version": 2, "allocations": []registry.Allocation{{"worktree": intent.Target.Path, "credential": "never-emit-credential"}}})
				if err := os.WriteFile(e.Registry.Path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				raw, err := os.ReadFile(e.Registry.Path)
				if err != nil {
					t.Fatal(err)
				}
				var data registry.RegistryData
				if err := json.Unmarshal(raw, &data); err != nil {
					t.Fatal(err)
				}
				data.Allocations[0]["runtime_binding"] = registry.RuntimeBinding{Kind: "unknown"}
				raw, _ = json.Marshal(data)
				if err := os.WriteFile(e.Registry.Path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(e.Registry.Path, []byte(`{"credential":"never-emit-credential",`), 0600); err != nil {
					t.Fatal(err)
				}
			case "bad_config":
				if err := os.WriteFile(filepath.Join(intent.Target.Path, config.ProjectConfigFile), []byte("hooks: {pre_release: [true]}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(e.Registry.Path)
			_, _, err := executeReleaseInspect(t, e.Registry.Path, intent.Target.Path, "--operation-id", uuid.NewString())
			want := "runtime_binding_unavailable"
			if fixture == "corrupt" {
				want = "registry_unavailable"
			}
			if fixture == "bad_config" {
				want = "config_unavailable"
			}
			if err == nil || err.Error() != want {
				t.Fatalf("want %s, got %v", want, err)
			}
			after, _ := os.ReadFile(e.Registry.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("refused inspection rewrote evidence")
			}
		})
	}
}

func TestReleaseInspectReservedAndExistingOperationRequireResume(t *testing.T) {
	e, initial := releaseExecutorFixture(t)
	intent, _, err := executeReleaseInspect(t, e.Registry.Path, initial.Target.Path, "--operation-id", initial.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Registry.PrepareRelease(intent); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(e.Registry.Path)
	for _, id := range []string{initial.OperationID, uuid.NewString()} {
		_, _, err := executeReleaseInspect(t, e.Registry.Path, initial.Target.Path, "--operation-id", id)
		want := "reservation_refused"
		if id == initial.OperationID {
			want = "operation_exists_use_resume"
		}
		if err == nil || err.Error() != want {
			t.Fatalf("want %s, got %v", want, err)
		}
	}
	after, _ := os.ReadFile(e.Registry.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("inspect resumed or altered retained operation")
	}
}

func TestReleaseInspectUnallocatedLinkedRemovalAndPrimaryRefusal(t *testing.T) {
	main, linked := releaseSafetyFixture(t)
	reg := registry.New("")
	id := uuid.NewString()
	intent, _, err := executeReleaseInspect(t, reg.Path, linked, "--operation-id", id, "--remove-worktree", "--force-dirty")
	if err != nil {
		t.Fatal(err)
	}
	if intent.Allocation != (registry.AllocationRef{Kind: "unallocated"}) || intent.RuntimeBinding != nil || intent.Target.Common == nil || intent.HooksDigest != (releaseHookPlan{}).digest() || intent.DatabaseConfigRevision != "" || intent.Policy != (registry.ReleasePolicy{RemoveWorktree: true, ForceDirty: true}) {
		t.Fatalf("unallocated inspection invented cleanup: %+v", intent)
	}
	for _, extra := range [][]string{{}, {"--remove-worktree", "--drop-db"}, {"--remove-worktree", "--ignore-hook-failure"}} {
		args := append([]string{linked, "--operation-id", uuid.NewString()}, extra...)
		if _, _, err := executeReleaseInspect(t, reg.Path, args...); err == nil || err.Error() != "invalid_request" {
			t.Fatalf("invalid unallocated policy accepted: %v", err)
		}
	}
	if _, _, err := executeReleaseInspect(t, reg.Path, main, "--operation-id", uuid.NewString(), "--remove-worktree"); err == nil || err.Error() != "removal_refused" {
		t.Fatalf("primary removal accepted: %v", err)
	}
	if _, err := os.Stat(reg.Path); !os.IsNotExist(err) {
		t.Fatal("unallocated inspection created registry", err)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatal("inspection removed worktree", err)
	}
}

func TestReleaseInspectInvalidArgumentsAreBounded(t *testing.T) {
	e, intent := releaseExecutorFixture(t)
	for _, args := range [][]string{
		{}, {intent.Target.Path}, {intent.Target.Path, "--operation-id", "never-emit-secret"},
		{intent.Target.Path, "--operation-id", strings.ToUpper(intent.OperationID)},
		{intent.Target.Path, "--operation-id", uuid.NewString(), "--force-dirty"},
		{intent.Target.Path, "--operation-id", uuid.NewString(), "--secret-unknown-flag"},
		{intent.Target.Path, "--operation-id", uuid.NewString(), "extra-secret"},
	} {
		_, _, err := executeReleaseInspect(t, e.Registry.Path, args...)
		if err == nil || (err.Error() != "invalid_request" && err.Error() != "invalid_operation_id") {
			t.Fatalf("unbounded argument refusal: %v", err)
		}
	}
}

func TestReleaseInspectRequestFeedsRegisteredStructuredStart(t *testing.T) {
	registered := false
	for _, command := range releaseCmd.Commands() {
		if command.Name() == "inspect" {
			registered = true
		}
	}
	if !registered {
		t.Fatal("release inspect is not registered")
	}
	executor, initial := releaseExecutorFixture(t)
	intent, raw, err := executeReleaseInspect(t, executor.Registry.Path, initial.Target.Path, "--operation-id", initial.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	executor.RunPhase = func(_ context.Context, operation registry.ReleaseOperation, _ registry.ReleasePhase) registry.PhaseResult {
		calls++
		if !reflect.DeepEqual(operation.ReleaseRequest, intent) {
			t.Fatal("structured start changed CLI-derived intent")
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	factory := func(reg *registry.Registry) (*releaseExecutor, error) { executor.Registry = reg; return executor, nil }
	completed, err := executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), string(raw))
	if err != nil || completed.Operation == nil || completed.Outcome != "completed" || calls != 4 {
		t.Fatalf("start=%+v err=%v calls=%d", completed, err, calls)
	}
	before, _ := os.ReadFile(executor.Registry.Path)
	if _, _, err := executeReleaseInspect(t, executor.Registry.Path, intent.Target.Path, "--operation-id", intent.OperationID); err == nil || err.Error() != "operation_exists_use_resume" {
		t.Fatalf("inspection reused completed UUID: %v", err)
	}
	after, _ := os.ReadFile(executor.Registry.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("completed UUID inspection mutated retained result")
	}
}
