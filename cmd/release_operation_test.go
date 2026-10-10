package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func queryReleaseStatus(t *testing.T, path, id string) (releaseStatus, error) {
	t.Helper()
	var out bytes.Buffer
	parent := &cobra.Command{
		Use: "fixture", SilenceErrors: true, SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			t.Fatal("status ran inherited write/update hooks")
			return nil
		},
		PersistentPostRun: func(*cobra.Command, []string) { t.Fatal("status ran inherited update notice") },
	}
	parent.AddCommand(newReleaseStatusCommand(path))
	parent.SetOut(&out)
	parent.SetErr(&out)
	parent.SetArgs([]string{"status", id})
	err := parent.Execute()
	var response releaseStatus
	if decodeErr := json.Unmarshal(out.Bytes(), &response); decodeErr != nil {
		t.Fatalf("invalid status JSON: %v: %s", decodeErr, out.Bytes())
	}
	if response.Schema != 1 {
		t.Fatalf("schema = %d", response.Schema)
	}
	return response, err
}

func TestReleaseStatusQueryDoesNotWrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, fixture := range []struct{ name, raw, outcome string }{
		{"missing", "", "not_found"},
		{"legacy", `{"version":2,"allocations":[]}`, "not_found"},
		{"corrupt", `{"secret":"do-not-emit-this",`, "registry_unavailable"},
		{"future", `{"version":999,"allocations":[]}`, "registry_unavailable"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "registry.json")
			if fixture.raw != "" {
				if err := os.WriteFile(path, []byte(fixture.raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			response, err := queryReleaseStatus(t, path, uuid.NewString())
			if err == nil || response.Outcome != fixture.outcome || response.Operation != nil {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if fixture.raw == "" {
				if len(entries) != 0 {
					t.Fatal("missing query created files")
				}
			} else {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != fixture.raw || len(entries) != 1 {
					t.Fatal("query changed registry or created lock/config files")
				}
			}
		})
	}
	response, err := queryReleaseStatus(t, filepath.Join(t.TempDir(), "registry.json"), "not-a-uuid-secret")
	if err == nil || response.Outcome != "invalid_operation_id" || response.OperationID != "" {
		t.Fatalf("invalid ID response=%+v err=%v", response, err)
	}
}

func TestReleaseStatusRetainsInterruptedAndCompletedEvidence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if strings.HasPrefix(key, "GIT_") {
			t.Setenv(key, "")
			_ = os.Unsetenv(key)
		}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	target := registry.TargetIdentity{Path: root, Directory: registry.DirectoryIdentity{
		Dev: strconv.FormatUint(uint64(stat.Dev), 10), Ino: strconv.FormatUint(stat.Ino, 10),
	}}
	reg := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	a := commandFreshEnrollment(t, reg, target, registry.Allocation{"worktree": root, "project": "fixture", "env": map[string]any{"token": "never-in-receipt"}})
	request := registry.ReleaseRequest{OperationID: uuid.NewString(), Target: target,
		Allocation: registry.AllocationRef{Kind: "allocated", Generation: registry.GetString(a, "allocation_generation")}, HooksDigest: strings.Repeat("0", 64), RuntimeBinding: &registry.RuntimeBinding{Kind: "never_started"}}
	op, _, err := reg.PrepareRelease(request)
	if err != nil {
		t.Fatal(err)
	}
	assertQuery := func(want string) {
		t.Helper()
		before, err := os.ReadFile(reg.Path)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			response, err := queryReleaseStatus(t, reg.Path, request.OperationID)
			if err != nil || response.Outcome != want || response.Operation == nil || response.OperationID != request.OperationID {
				t.Fatalf("want %s got %+v err=%v", want, response, err)
			}
			encoded, _ := json.Marshal(response)
			if bytes.Contains(encoded, []byte("never-in-receipt")) {
				t.Fatal("query exposed allocation environment")
			}
		}
		after, err := os.ReadFile(reg.Path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("query mutated retained evidence")
		}
	}
	assertQuery("prepared")
	for _, phase := range op.Phases {
		current, err := reg.GetRelease(request.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		token, err := reg.BeginReleasePhase(request.OperationID, phase.Name, current.Revision)
		if err != nil {
			t.Fatal(err)
		}
		assertQuery("uncertain")
		if phase.Name == registry.PhaseAllocation {
			_, err = reg.RetireReleaseAllocation(token)
		} else {
			_, err = reg.RecordReleasePhase(token, registry.PhaseResult{State: "completed", Code: "ok"})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	assertQuery("completed")
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	assertQuery("completed")
}
