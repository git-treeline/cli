package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

func runtimeBootstrapFixture(t *testing.T) (*runtimeowner.Store, runtimeHandoff, runtimeowner.Owner, detachedSupervisorConfig, registry.Allocation) {
	t.Helper()
	store, h, owner := runtimeHandoffFixture(t, nil)
	raw, err := encodeRuntimeHandoff(h)
	if err != nil {
		t.Fatal(err)
	}
	runtime := detachedSupervisorConfig{CommandTemplate: "must not execute", Dir: h.Binding.Target.Path, SocketPath: supervisor.SocketPath(h.Binding.Target.Path), Port: h.Port, Env: map[string]string{"FIXTURE": "private"}, RuntimeHandoff: raw}
	allocation := registry.Allocation{"worktree": h.Binding.Target.Path, "allocation_generation": h.Binding.Allocation.Generation, "runtime_binding": registry.RuntimeBinding{Kind: "supervisor", Generation: h.Binding.Generation}}
	return store, h, owner, runtime, allocation
}

func TestRuntimeBootstrapRoundTripAndLegacyAbsence(t *testing.T) {
	_, h, _, runtime, _ := runtimeBootstrapFixture(t)
	raw, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	decoded, got, err := decodeDetachedSupervisorConfig(raw)
	if err != nil || got == nil || !reflect.DeepEqual(*got, h) || !reflect.DeepEqual(decoded, runtime) {
		t.Fatal("tracked bootstrap changed", err)
	}
	runtime.RuntimeHandoff = nil
	raw, _ = json.Marshal(runtime)
	decoded, got, err = decodeDetachedSupervisorConfig(raw)
	if err != nil || got != nil || !reflect.DeepEqual(decoded, runtime) {
		t.Fatal("legacy bootstrap gained tracked capability", err)
	}
}

func TestRuntimeBootstrapRejectsMalformedTrackedEnvelope(t *testing.T) {
	_, _, _, runtime, _ := runtimeBootstrapFixture(t)
	raw, _ := json.Marshal(runtime)
	for _, invalid := range [][]byte{
		append(append([]byte{}, raw...), '\n'),
		bytes.Replace(raw, []byte(`"port":4321`), []byte(`"port":4321,"port":4321`), 1),
		bytes.Replace(raw, []byte(`"command_template":`), []byte(`"unknown":0,"command_template":`), 1),
		bytes.Replace(raw, []byte(`"runtime_handoff":`), []byte(`"runtime_handoff":null,"runtime_handoff":`), 1),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":2`), 1),
	} {
		if _, _, err := decodeDetachedSupervisorConfig(invalid); err == nil {
			t.Fatal("noncanonical tracked bootstrap accepted")
		}
	}
	runtime.RuntimeHandoff = json.RawMessage(`null`)
	raw, _ = json.Marshal(runtime)
	if _, _, err := decodeDetachedSupervisorConfig(raw); err == nil {
		t.Fatal("explicit null handoff downgraded to legacy")
	}
}

func TestRuntimeBootstrapRequiresExactCurrentBinding(t *testing.T) {
	for _, failure := range []string{"none", "directory", "socket", "port", "allocation", "path", "generation", "legacy", "unknown", "config"} {
		t.Run(failure, func(t *testing.T) {
			_, h, _, runtime, allocation := runtimeBootstrapFixture(t)
			revision := h.Binding.ConfigDigest
			switch failure {
			case "directory":
				runtime.Dir += "/other"
			case "socket":
				runtime.SocketPath += ".other"
			case "port":
				runtime.Port++
			case "allocation":
				allocation["allocation_generation"] = uuid.NewString()
			case "path":
				allocation["worktree"] = h.Binding.Target.Path + "/other"
			case "generation":
				allocation["runtime_binding"] = registry.RuntimeBinding{Kind: "supervisor", Generation: uuid.NewString()}
			case "legacy":
				delete(allocation, "runtime_binding")
			case "unknown":
				allocation["runtime_binding"] = registry.RuntimeBinding{Kind: "unknown"}
			case "config":
				revision = "changed"
			}
			err := validateRuntimeBootstrap(runtime, h, allocation, revision)
			if failure == "none" && err != nil || failure != "none" && err == nil {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func TestRuntimeBootstrapReadinessRequiresBirthReceiptAndTypedStatus(t *testing.T) {
	for _, failure := range []string{"none", "no snapshot", "prepared", "birth", "boot", "probe", "status", "status error", "socket", "stopping", "receipt changed", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			store, h, owner, runtime, _ := runtimeBootstrapFixture(t)
			if failure != "prepared" {
				if _, err := store.RegisterOwner(context.Background(), h.Binding, owner); err != nil {
					t.Fatal(err)
				}
			}
			detached := &detachedSupervisor{socket: runtime.SocketPath, ownerPID: owner.PID, owner: &owner}
			if failure == "no snapshot" {
				detached.owner = nil
			}
			if failure == "socket" {
				detached.socket += ".other"
			}
			if failure == "stopping" {
				if _, _, err := store.BeginStop(context.Background(), h.Binding, owner, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			observe := func(pid int) (runtimeowner.Owner, error) {
				if pid != owner.PID {
					t.Fatal("wrong PID observed")
				}
				actual := owner
				if failure == "birth" {
					actual.BirthID = "999:1"
				}
				if failure == "boot" {
					actual.BootID = uuid.NewString()
				}
				if failure == "probe" {
					return actual, errors.New("unverified")
				}
				return actual, nil
			}
			status := func(socket, generation string) (string, error) {
				if socket != runtime.SocketPath || generation != h.Binding.Generation {
					t.Fatal("wrong status identity")
				}
				if failure == "status" {
					return "running", nil
				}
				if failure == "status error" {
					return "owned", errors.New("lost response")
				}
				if failure == "receipt changed" {
					if _, _, err := store.BeginStop(context.Background(), h.Binding, owner, uuid.NewString()); err != nil {
						t.Fatal(err)
					}
				}
				return "owned", nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancelled" {
				cancel()
			}
			if ready := trackedDetachedReady(ctx, detached, h, store, observe, status); ready != (failure == "none") {
				t.Fatalf("ready = %v", ready)
			}
		})
	}
}

func TestRuntimeBootstrapCleanupNeedsExactOwnershipAndDurableCompletion(t *testing.T) {
	for _, outcome := range []string{"settled", "lost reply", "ack only", "prepared", "uncertain", "probe mismatch", "no snapshot", "stopping", "completed", "socket"} {
		t.Run(outcome, func(t *testing.T) {
			store, h, owner, runtime, _ := runtimeBootstrapFixture(t)
			detached := &detachedSupervisor{socket: runtime.SocketPath, ownerPID: owner.PID, owner: &owner}
			if outcome == "no snapshot" {
				detached.owner = nil
			}
			if outcome == "socket" {
				detached.socket += ".other"
			}
			if outcome != "prepared" {
				if _, err := store.RegisterOwner(context.Background(), h.Binding, owner); err != nil {
					t.Fatal(err)
				}
			}
			existingOperation := uuid.NewString()
			if outcome == "stopping" || outcome == "completed" {
				if _, _, err := store.BeginStop(context.Background(), h.Binding, owner, existingOperation); err != nil {
					t.Fatal(err)
				}
				if outcome == "completed" {
					if _, err := store.Complete(context.Background(), h.Binding, owner, existingOperation); err != nil {
						t.Fatal(err)
					}
				}
			}
			if outcome == "uncertain" {
				if _, err := store.MarkUncertain(context.Background(), h.Binding, &owner, "", "owner_changed"); err != nil {
					t.Fatal(err)
				}
			}
			observe := func(pid int) (runtimeowner.Owner, error) {
				actual := owner
				if outcome == "probe mismatch" {
					actual.BirthID = "999:1"
				}
				return actual, nil
			}
			calls := 0
			shutdown := func(socket, generation, operation string, _ time.Duration) error {
				calls++
				if socket != runtime.SocketPath || generation != h.Binding.Generation {
					t.Fatal("wrong shutdown identity")
				}
				if outcome == "stopping" && operation != existingOperation {
					t.Fatal("stopping retry changed operation")
				}
				if outcome == "ack only" {
					return nil
				}
				if _, _, err := store.BeginStop(context.Background(), h.Binding, owner, operation); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Complete(context.Background(), h.Binding, owner, operation); err != nil {
					t.Fatal(err)
				}
				if outcome == "lost reply" {
					return errors.New("reply lost")
				}
				return nil
			}
			err := cleanupTrackedDetachedSupervisor(context.Background(), detached, h, store, observe, shutdown)
			success := outcome == "settled" || outcome == "lost reply" || outcome == "stopping" || outcome == "completed"
			if (err == nil) != success {
				t.Fatalf("cleanup = %v", err)
			}
			wantCalls := 0
			if outcome == "settled" || outcome == "lost reply" || outcome == "stopping" || outcome == "ack only" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("shutdown dispatched without exact evidence")
			}
		})
	}
}
