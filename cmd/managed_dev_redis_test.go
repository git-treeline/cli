package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
)

func TestManagedDevRedisRuntimeAndRelease(t *testing.T) {
	for _, mode := range []string{"valid", "sibling", "removed opt-in"} {
		t.Run(mode, func(t *testing.T) {
			root, reg, _ := managedDevPostgreSQLFixture(t, false)
			body := strings.Replace(managedDevPostgreSQLConfig, "env: {DATABASE_NAME: '{database}'}", "env: {REDIS_URL: '{redis_url}', REDIS_PREFIX: '{redis_prefix}'}", 1)
			if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := reg.UpdateField(root, "redis_prefix", "host_migration_qa:"+filepath.Base(root)); err != nil {
				t.Fatal(err)
			}
			a, err := reg.FindForRelease(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateManagedDevRuntime(root, a, false); err != nil {
				t.Fatal(err)
			}
			target, err := registry.CaptureReleaseTarget(root)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := registry.AllocationRuntimeBinding(a)
			if err != nil {
				t.Fatal(err)
			}
			op, _, err := reg.PrepareRelease(registry.ReleaseRequest{OperationID: uuid.NewString(), Target: target,
				Allocation:     registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(a, "allocation_generation")},
				RuntimeBinding: &binding, HooksDigest: (releaseHookPlan{}).digest()})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := json.Marshal(op.Snapshot)
			if err != nil || strings.Contains(string(snapshot), "redis_prefix") || op.Snapshot.RedisDB != nil {
				t.Fatal("changed snapshot contract", err)
			}
			if mode == "sibling" {
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
						allocation["redis_prefix"] = "host_migration_qa:sibling"
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
			if mode == "removed opt-in" {
				if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(managedDevPostgreSQLConfig), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a, err = reg.FindForRelease(root)
			if err != nil {
				t.Fatal(err)
			}
			runtimeErr, releaseErr := validateManagedDevRuntime(root, a, false), validateManagedDevRelease(op)
			if mode != "valid" {
				if runtimeErr == nil || releaseErr == nil {
					t.Fatal("Redis drift admitted", runtimeErr, releaseErr)
				}
				return
			}
			if runtimeErr != nil || releaseErr != nil {
				t.Fatal(runtimeErr, releaseErr)
			}
			for _, phase := range []registry.ReleasePhase{registry.PhasePreHook, registry.PhaseSupervisor, registry.PhaseAllocation} {
				token, err := reg.BeginReleasePhase(op.OperationID, phase, op.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if phase == registry.PhaseAllocation {
					op, err = reg.RetireReleaseAllocation(token)
				} else {
					op, err = reg.RecordReleasePhase(token, registry.PhaseResult{State: "completed", Code: "ok"})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := validateManagedDevRelease(op); err != nil {
				t.Fatal("prefix-free snapshot cannot finish after retirement", err)
			}
			if current, err := reg.FindForRelease(root); err != nil || current != nil {
				t.Fatal("allocation not retired", current, err)
			}
		})
	}
}
