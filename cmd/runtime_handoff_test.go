package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

func runtimeHandoffFixture(t *testing.T, commands []string) (*runtimeowner.Store, runtimeHandoff, runtimeowner.Owner) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gtl-runtime-handoff-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	binding := runtimeowner.Binding{
		Target:     registry.TargetIdentity{Path: t.TempDir(), Directory: registry.DirectoryIdentity{Dev: "1", Ino: "2"}},
		Allocation: registry.AllocationRef{Kind: "allocated", Generation: uuid.NewString()},
		Generation: uuid.NewString(), ConfigDigest: strings.Repeat("a", 64),
	}
	h, err := captureRuntimeHandoff(binding, []startHookEntry{{Name: "fixture", Hook: config.StartHook{PostStop: commands}}}, 4321)
	if err != nil {
		t.Fatal(err)
	}
	store := runtimeowner.New(filepath.Join(dir, "runtime.json"))
	if _, err := store.Prepare(context.Background(), h.Binding); err != nil {
		t.Fatal(err)
	}
	return store, h, runtimeowner.Owner{BootID: uuid.NewString(), PID: 99999999, BirthID: "123:456"}
}

func TestRuntimeHandoffCapturesExactRawPlanAndPort(t *testing.T) {
	_, original, _ := runtimeHandoffFixture(t, nil)
	selected := []startHookEntry{
		{Name: "first", Hook: config.StartHook{PostStop: []string{"first-a {port}", "first-b $SECRET"}}},
		{Name: "second", Hook: config.StartHook{PostStop: []string{"second-a {port_2}", "second-b"}}},
	}
	h, err := captureRuntimeHandoff(original.Binding, selected, 5432)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"second-a {port_2}", "second-b", "first-a {port}", "first-b $SECRET"}
	if !reflect.DeepEqual(h.PostStop, want) {
		t.Fatalf("plan = %q", h.PostStop)
	}
	selected[1].Hook.PostStop[0] = "changed config"
	if !reflect.DeepEqual(h.PostStop, want) {
		t.Fatal("captured plan changed with source")
	}
	raw, err := encodeRuntimeHandoff(h)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("5433")) || !bytes.Contains(raw, []byte("{port_2}")) {
		t.Fatal("persisted expanded command")
	}
	decoded, err := decodeRuntimeHandoff(raw)
	if err != nil || !reflect.DeepEqual(decoded, h) {
		t.Fatal("handoff round trip failed", err)
	}
	changedPort, err := captureRuntimeHandoff(original.Binding, selected, 5433)
	if err != nil || changedPort.Binding.PostStopDigest == h.Binding.PostStopDigest {
		t.Fatal("port or command not bound")
	}
	empty, err := captureRuntimeHandoff(original.Binding, nil, 5432)
	if err != nil || empty.PostStop == nil {
		t.Fatal("explicit empty plan missing")
	}
	emptyRaw, _ := encodeRuntimeHandoff(empty)
	if !bytes.Contains(emptyRaw, []byte(`"post_stop":[]`)) {
		t.Fatal("empty selection not represented")
	}
	changedPort, err = captureRuntimeHandoff(original.Binding, nil, 5433)
	if err != nil || changedPort.Binding.PostStopDigest == empty.Binding.PostStopDigest {
		t.Fatal("empty plan port not bound")
	}
}

func TestRuntimeHandoffStrictBoundsAndFields(t *testing.T) {
	_, h, _ := runtimeHandoffFixture(t, []string{"fixture {port}"})
	raw, _ := encodeRuntimeHandoff(h)
	for _, malformed := range [][]byte{
		append(append([]byte{}, raw...), '\n'),
		append(append([]byte{}, raw...), []byte(`{}`)...),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":2`), 1),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"extra":0`), 1),
		bytes.Replace(raw, []byte(`"port":4321`), []byte(`"port":4322`), 1),
		bytes.Replace(raw, []byte(`"post_stop":["fixture {port}"]`), []byte(`"post_stop":null`), 1),
		bytes.Repeat([]byte(" "), maxRuntimeHandoffBytes+1),
	} {
		if _, err := decodeRuntimeHandoff(malformed); err == nil {
			t.Fatal("malformed handoff accepted")
		}
	}
	for _, commands := range [][]string{nil, {"nul\x00command"}, {string([]byte{255})}, {strings.Repeat("x", maxRuntimeHandoffBytes)}, make([]string, 1025)} {
		bad := h
		bad.PostStop = commands
		bad.Binding.PostStopDigest = bad.postStopDigest()
		if _, err := encodeRuntimeHandoff(bad); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
}

func TestRuntimeHandoffCompletesCapturedPlanOnceWithDurableReceipt(t *testing.T) {
	store, h, owner := runtimeHandoffFixture(t, []string{"first {port}", "second {port_2}"})
	sv := supervisor.New("must not execute", h.Binding.Target.Path, "unused.sock")
	var commands []string
	run := func(_ context.Context, dir, command string) checkedHookOutcome {
		if dir != h.Binding.Target.Path {
			t.Fatal("wrong target")
		}
		receipt, err := store.Query(context.Background(), h.Binding)
		if err != nil || receipt.State != runtimeowner.Stopping {
			t.Fatal("hook ran before durable stopping", err)
		}
		commands = append(commands, command)
		return hookSettledOK
	}
	if err := configureTrackedRuntime(sv, store, h, owner, run); err != nil {
		t.Fatal(err)
	}
	if sv.RuntimeGeneration != h.Binding.Generation {
		t.Fatal("wrong supervisor generation")
	}
	receipt, err := store.Query(context.Background(), h.Binding)
	if err != nil || receipt.State != runtimeowner.Owned || receipt.Owner == nil || *receipt.Owner != owner {
		t.Fatal("owner was not registered")
	}
	if sv.ShutdownComplete() == nil {
		t.Fatal("completion without claimed stop accepted")
	}
	op := uuid.NewString()
	if err := sv.ShutdownRequested(op); err != nil {
		t.Fatal(err)
	}
	h.PostStop[0] = "modified after handoff"
	if err := sv.ShutdownComplete(); err != nil {
		t.Fatal(err)
	}
	if err := sv.ShutdownComplete(); err != nil {
		t.Fatal("cached completion failed", err)
	}
	if !reflect.DeepEqual(commands, []string{"first 4321", "second 4322"}) {
		t.Fatalf("captured execution = %q", commands)
	}
	reopened := runtimeowner.New(store.Path)
	receipt, err = reopened.Query(context.Background(), h.Binding)
	if err != nil || receipt.State != runtimeowner.Completed || receipt.OperationID != op {
		t.Fatal("success not durable", err)
	}
	data, err := os.ReadFile(store.Path)
	if err != nil || bytes.Contains(data, []byte("first")) || bytes.Contains(data, []byte("second")) {
		t.Fatal("receipt contains command body")
	}
	if sv.ShutdownRequested(op) == nil {
		t.Fatal("duplicate claim replayed")
	}
	other := supervisor.New("must not execute", "unused", "unused.sock")
	if configureTrackedRuntime(other, reopened, h, owner, run) == nil {
		t.Fatal("completed owner adopted again")
	}
}

func TestRuntimeHandoffEmptyPlanAndOrdinaryShutdown(t *testing.T) {
	store, h, owner := runtimeHandoffFixture(t, nil)
	sv := supervisor.New("must not execute", "unused", "unused.sock")
	if err := configureTrackedRuntime(sv, store, h, owner, nil); err != nil {
		t.Fatal(err)
	}
	if err := sv.ShutdownRequested(""); err != nil {
		t.Fatal(err)
	}
	if err := sv.ShutdownComplete(); err != nil {
		t.Fatal(err)
	}
	r, err := runtimeowner.New(store.Path).Query(context.Background(), h.Binding)
	if err != nil || r.State != runtimeowner.Completed {
		t.Fatal("empty selected plan did not complete", err)
	}
	if id, err := uuid.Parse(r.OperationID); err != nil || id == uuid.Nil || id.String() != r.OperationID {
		t.Fatal("ordinary shutdown has no operation identity")
	}
}

func TestRuntimeHandoffUncertainHooksNeverReplay(t *testing.T) {
	for _, outcome := range []checkedHookOutcome{hookNotStarted, hookSettledFailed, hookUncertain, "panic", "unrecognized"} {
		t.Run(string(outcome), func(t *testing.T) {
			store, h, owner := runtimeHandoffFixture(t, []string{"first", "must not run"})
			sv := supervisor.New("must not execute", "unused", "unused.sock")
			calls := 0
			run := func(context.Context, string, string) checkedHookOutcome {
				calls++
				if outcome == "panic" {
					panic("fixture panic")
				}
				return outcome
			}
			if err := configureTrackedRuntime(sv, store, h, owner, run); err != nil {
				t.Fatal(err)
			}
			if err := sv.ShutdownRequested(uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if sv.ShutdownComplete() == nil || sv.ShutdownComplete() == nil || calls != 1 {
				t.Fatal("uncertain hook succeeded or replayed")
			}
			r, err := runtimeowner.New(store.Path).Query(context.Background(), h.Binding)
			if err != nil || r.State != runtimeowner.Uncertain || r.Code != "post_stop_failed" {
				t.Fatal("uncertainty not retained", err)
			}
			other := supervisor.New("must not execute", "unused", "unused.sock")
			if configureTrackedRuntime(other, store, h, owner, run) == nil {
				t.Fatal("uncertain runtime adopted")
			}
		})
	}
}

func TestRuntimeHandoffRefusesMismatchedAndInterruptedOwnership(t *testing.T) {
	for _, failure := range []string{"binding", "owner", "runner", "already stopping", "lost claim"} {
		t.Run(failure, func(t *testing.T) {
			store, h, owner := runtimeHandoffFixture(t, []string{"must not run"})
			sv := supervisor.New("must not execute", "unused", "unused.sock")
			run := checkedHookRunner(func(context.Context, string, string) checkedHookOutcome {
				t.Fatal("refused runtime ran hook")
				return hookSettledOK
			})
			op := uuid.NewString()
			if failure == "binding" {
				h.Binding.ConfigDigest = strings.Repeat("b", 64)
			}
			if failure == "runner" {
				run = nil
			}
			if failure == "owner" || failure == "already stopping" {
				if _, err := store.RegisterOwner(context.Background(), h.Binding, owner); err != nil {
					t.Fatal(err)
				}
				if failure == "owner" {
					owner.BirthID = "999:1"
				} else if _, _, err := store.BeginStop(context.Background(), h.Binding, owner, op); err != nil {
					t.Fatal(err)
				}
			}
			err := configureTrackedRuntime(sv, store, h, owner, run)
			if failure != "lost claim" {
				if err == nil {
					t.Fatal("unsafe setup accepted")
				}
				if sv.RuntimeGeneration != "" || sv.ShutdownComplete != nil {
					t.Fatal("failed setup installed callbacks")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, claimed, err := store.BeginStop(context.Background(), h.Binding, owner, op); err != nil || !claimed {
				t.Fatal("fixture claim failed", err)
			}
			if !errors.Is(sv.ShutdownRequested(op), runtimeowner.ErrUncertain) {
				t.Fatal("lost claim permitted replay")
			}
			if sv.ShutdownComplete() == nil {
				t.Fatal("unclaimed completion accepted")
			}
		})
	}
}

func TestRuntimeHandoffMissingFieldRejected(t *testing.T) {
	_, h, _ := runtimeHandoffFixture(t, nil)
	raw, _ := encodeRuntimeHandoff(h)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "post_stop")
	malformed, _ := json.Marshal(fields)
	if _, err := decodeRuntimeHandoff(malformed); err == nil {
		t.Fatal("missing explicit empty plan accepted")
	}
}
