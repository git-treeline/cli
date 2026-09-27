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
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func executePublicRelease(t *testing.T, command *cobra.Command, input string, args ...string) (releaseStatus, error) {
	t.Helper()
	var out bytes.Buffer
	root := &cobra.Command{Use: "fixture", SilenceErrors: true, SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			t.Fatal("release inherited root configuration hooks")
			return nil
		},
		PersistentPostRunE: func(*cobra.Command, []string) error { t.Fatal("release inherited root update hooks"); return nil },
	}
	root.AddCommand(command)
	root.SetIn(strings.NewReader(input))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{command.Name()}, args...))
	err := root.Execute()
	var status releaseStatus
	if decodeErr := json.Unmarshal(out.Bytes(), &status); decodeErr != nil {
		t.Fatalf("invalid release response %q: %v", out.String(), decodeErr)
	}
	if status.Schema != 1 {
		t.Fatalf("unexpected schema %d", status.Schema)
	}
	return status, err
}

func publicRequestJSON(t *testing.T, request registry.ReleaseRequest) string {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPublicReleaseStartLostReplyAndResumeAreConnected(t *testing.T) {
	executor, intent := releaseExecutorFixture(t)
	effects, factories := 0, 0
	executor.RunPhase = func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		effects++
		retained, err := executor.Registry.GetRelease(op.OperationID)
		if err != nil || !reflect.DeepEqual(retained, &op) {
			t.Fatal("public dispatch preceded exact retained intent", err)
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	factory := func(reg *registry.Registry) (*releaseExecutor, error) {
		factories++
		executor.Registry = reg
		return executor, nil
	}
	input := publicRequestJSON(t, intent)
	first, err := executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), input)
	if err != nil || first.Outcome != "completed" || first.Operation == nil || effects != 5 || factories != 1 {
		t.Fatalf("start=%+v err=%v effects=%d factories=%d", first, err, effects, factories)
	}
	// Replies and configuration may disappear; the committed receipt is enough.
	if err := os.WriteFile(filepath.Join(intent.Target.Path, ".treeline.yml"), []byte("invalid current config: [credential-do-not-read"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, resume := range []bool{false, true} {
		var next releaseStatus
		var err error
		if resume {
			next, err = executePublicRelease(t, newReleaseResumeCommand(executor.Registry.Path, factory), "unread secret stdin", intent.OperationID)
		} else {
			next, err = executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), input)
		}
		if err != nil || !reflect.DeepEqual(first, next) || effects != 5 || factories != 1 {
			t.Fatalf("replay=%+v err=%v effects=%d factories=%d", next, err, effects, factories)
		}
	}
	changed := intent
	changed.HooksDigest = strings.Repeat("a", 64)
	conflict, err := executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), publicRequestJSON(t, changed))
	if err == nil || err.Error() != "binding_conflict" || conflict.Outcome != "binding_conflict" || !reflect.DeepEqual(conflict.Operation, first.Operation) || effects != 5 || factories != 1 {
		t.Fatalf("changed intent=%+v err=%v", conflict, err)
	}
}

func TestPublicReleaseResumeUsesOnlyRetainedIntent(t *testing.T) {
	executor, intent := releaseExecutorFixture(t)
	effects := 0
	executor.RunPhase = func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		effects++
		if !reflect.DeepEqual(op.ReleaseRequest, intent) {
			t.Fatal("resume replaced retained bindings")
		}
		return registry.PhaseResult{State: "blocked", Code: "config_changed"}
	}
	factory := func(reg *registry.Registry) (*releaseExecutor, error) { executor.Registry = reg; return executor, nil }
	first, err := executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), publicRequestJSON(t, intent))
	if err == nil || first.Outcome != "blocked" || first.Operation == nil || effects != 1 {
		t.Fatalf("blocked=%+v err=%v", first, err)
	}
	executor.RunPhase = func(_ context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
		effects++
		if !reflect.DeepEqual(op.ReleaseRequest, intent) {
			t.Fatal("resume replaced retained bindings")
		}
		return registry.PhaseResult{State: "completed", Code: "ok"}
	}
	completed, err := executePublicRelease(t, newReleaseResumeCommand(executor.Registry.Path, factory), `{"hooks_digest":"caller-must-not-supply-intent"}`, intent.OperationID)
	if err != nil || completed.Outcome != "completed" || completed.Operation == nil || effects != 6 {
		t.Fatalf("resume=%+v err=%v effects=%d", completed, err, effects)
	}
	latest, err := executor.Registry.GetRelease(intent.OperationID)
	if err != nil || !reflect.DeepEqual(completed.Operation, latest) {
		t.Fatal("response not exact retained evidence", err)
	}
}

func TestPublicReleaseRejectsNoncanonicalRequestsBeforeFactory(t *testing.T) {
	executor, intent := releaseExecutorFixture(t)
	valid := publicRequestJSON(t, intent)
	var fields map[string]any
	if err := json.Unmarshal([]byte(valid), &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "hooks_digest")
	missing, _ := json.Marshal(fields)
	fixtures := map[string]string{
		"empty": "", "too_large": strings.Repeat(" ", maxReleaseRequestBytes+1),
		"unknown":          strings.TrimSuffix(valid, "}") + `,"credential":"never-emit-this"}`,
		"duplicate":        strings.TrimSuffix(valid, "}") + fmt.Sprintf(`,"operation_id":%q}`, intent.OperationID),
		"nested_duplicate": strings.Replace(valid, `"force_dirty":false`, `"force_dirty":false,"force_dirty":false`, 1),
		"trailing":         valid + ` {"credential":"never-emit-this"}`,
		"missing":          string(missing), "null": `null`, "array": `[]`,
		"wrong_case":    strings.Replace(valid, `"operation_id"`, `"Operation_id"`, 1),
		"optional_null": strings.Replace(valid, `"runtime_binding":{"kind":"never_started"}`, `"runtime_binding":null`, 1),
	}
	factory := func(*registry.Registry) (*releaseExecutor, error) {
		t.Fatal("invalid JSON created executor")
		return nil, nil
	}
	before, _ := os.ReadFile(executor.Registry.Path)
	for name, input := range fixtures {
		t.Run(name, func(t *testing.T) {
			status, err := executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), input)
			if err == nil || err.Error() != "invalid_request" || status.Outcome != "invalid_request" || status.Operation != nil || status.OperationID != "" {
				t.Fatalf("status=%+v err=%v", status, err)
			}
			raw, _ := json.Marshal(status)
			if bytes.Contains(raw, []byte("never-emit-this")) {
				t.Fatal("decoder diagnostic leaked")
			}
			after, _ := os.ReadFile(executor.Registry.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid input changed registry")
			}
		})
	}
}

func TestPublicReleaseRefusalsAreBoundedAndDoNotEnroll(t *testing.T) {
	executor, intent := releaseExecutorFixture(t)
	for _, id := range []string{"secret-invalid-uuid", strings.ToUpper(intent.OperationID), uuid.NewString()} {
		factory := func(*registry.Registry) (*releaseExecutor, error) {
			t.Fatal("missing/invalid resume constructed executor")
			return nil, nil
		}
		status, err := executePublicRelease(t, newReleaseResumeCommand(executor.Registry.Path, factory), "", id)
		want := "not_found"
		if !publicReleaseUUID(id) {
			want = "invalid_operation_id"
		}
		if err == nil || err.Error() != want || status.Outcome != want || status.Operation != nil {
			t.Fatalf("status=%+v err=%v", status, err)
		}
	}
	factory := func(*registry.Registry) (*releaseExecutor, error) {
		return nil, errors.New("secret factory diagnostics")
	}
	status, err := executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), publicRequestJSON(t, intent))
	if err == nil || err.Error() != "executor_unavailable" || status.Outcome != "executor_unavailable" || status.Operation != nil {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if retained, err := executor.Registry.GetRelease(intent.OperationID); err != nil || retained != nil {
		t.Fatal("factory failure prepared operation", err)
	}
	if got := publicReleaseErrorCode(&releaseExecutionError{Code: "secret-unbounded-error"}); got != "execution_failed" {
		t.Fatal("unknown diagnostic exposed", got)
	}
}

func TestPublicReleaseUncertainResumeNeverReplaysEffects(t *testing.T) {
	executor, intent := releaseExecutorFixture(t)
	calls := 0
	executor.RunPhase = func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult {
		calls++
		return registry.PhaseResult{State: "uncertain", Code: "unknown"}
	}
	factory := func(reg *registry.Registry) (*releaseExecutor, error) { executor.Registry = reg; return executor, nil }
	first, err := executePublicRelease(t, newReleaseStartCommand(executor.Registry.Path, factory), publicRequestJSON(t, intent))
	if err == nil || first.Outcome != "uncertain" || calls != 1 {
		t.Fatalf("start=%+v err=%v", first, err)
	}
	again, err := executePublicRelease(t, newReleaseResumeCommand(executor.Registry.Path, factory), "", intent.OperationID)
	if err == nil || again.Outcome != "uncertain" || !reflect.DeepEqual(first.Operation, again.Operation) || calls != 1 {
		t.Fatalf("resume=%+v err=%v calls=%d", again, err, calls)
	}
}

func TestPublicReleaseArgumentErrorsStayBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	for _, args := range [][]string{{"secret-extra-argument"}, {"--secret-unknown-flag"}} {
		status, err := executePublicRelease(t, newReleaseStartCommand(path, nil), "", args...)
		if err == nil || err.Error() != "invalid_request" || status.Outcome != "invalid_request" || status.OperationID != "" {
			t.Fatalf("argument refusal=%+v err=%v", status, err)
		}
	}
}

func TestPublicReleaseCompletedSchemaThreeHistoryNeedsNoExecutor(t *testing.T) {
	executor, intent := releaseExecutorFixture(t)
	if _, err := executor.Start(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(executor.Registry.Path)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	data["version"] = 3
	record := data["release_operations"].([]any)[0].(map[string]any)
	delete(record, "runtime_binding")
	delete(record, "database_config_revision")
	raw, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executor.Registry.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	factory := func(*registry.Registry) (*releaseExecutor, error) {
		t.Fatal("completed historical result created executor")
		return nil, nil
	}
	status, err := executePublicRelease(t, newReleaseResumeCommand(executor.Registry.Path, factory), "", intent.OperationID)
	if err != nil || status.Outcome != "completed" || status.Operation == nil || status.Operation.RuntimeBinding != nil || status.Operation.DatabaseConfigRevision != "" {
		t.Fatalf("history=%+v err=%v", status, err)
	}
	after, _ := os.ReadFile(executor.Registry.Path)
	if !bytes.Equal(raw, after) {
		t.Fatal("historical query migrated or rewrote retained evidence")
	}
}
