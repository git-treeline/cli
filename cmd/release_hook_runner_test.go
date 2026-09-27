package cmd

import (
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func hookRunnerEffects(wait func() error, probe func(int) error) checkedHookEffects {
	return checkedHookEffects{
		start:       func(string, string) (ownedHookProcess, error) { return ownedHookProcess{group: 12345, wait: wait}, nil },
		groupExists: probe,
	}
}

func TestCheckedHookRunnerRequiresLeaderAndEmptyGroup(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed"}[failed], func(t *testing.T) {
			var waited atomic.Bool
			probes := 0
			effects := hookRunnerEffects(func() error {
				waited.Store(true)
				if failed {
					return &exec.ExitError{}
				}
				return nil
			}, func(group int) error {
				if group != 12345 || !waited.Load() {
					t.Fatal("group probed before owned child reaped")
				}
				probes++
				if probes == 1 {
					return nil
				}
				return syscall.ESRCH
			})
			got := runCheckedHook(context.Background(), "fixture-dir", "fixture-command", effects, time.Second, time.Millisecond)
			want := hookSettledOK
			if failed {
				want = hookSettledFailed
			}
			if got != want || probes != 2 {
				t.Fatalf("outcome=%s probes=%d", got, probes)
			}
		})
	}
}

func TestCheckedHookRunnerRefusesAmbiguousGroupEvidence(t *testing.T) {
	for _, probeErr := range []error{syscall.EPERM, syscall.EINVAL, errors.New("observation unavailable")} {
		effects := hookRunnerEffects(func() error { return nil }, func(int) error { return probeErr })
		if got := runCheckedHook(context.Background(), "", "", effects, time.Second, time.Millisecond); got != hookUncertain {
			t.Fatalf("%v certified %s", probeErr, got)
		}
	}
	effects := hookRunnerEffects(func() error { return errors.New("wait unavailable") }, func(int) error { t.Fatal("invalid wait result probed group"); return syscall.ESRCH })
	if got := runCheckedHook(context.Background(), "", "", effects, time.Second, time.Millisecond); got != hookUncertain {
		t.Fatalf("wait error certified %s", got)
	}
}

func TestCheckedHookRunnerBeforeStartAndStartFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	effects := checkedHookEffects{start: func(string, string) (ownedHookProcess, error) {
		t.Fatal("canceled context launched")
		return ownedHookProcess{}, nil
	}, groupExists: func(int) error { return syscall.ESRCH }}
	if got := runCheckedHook(ctx, "", "", effects, time.Second, time.Millisecond); got != hookNotStarted {
		t.Fatal(got)
	}
	effects.start = func(dir, command string) (ownedHookProcess, error) {
		if dir != "fixture-dir" || command != "fixture-command" {
			t.Fatal("launch inputs changed")
		}
		return ownedHookProcess{}, errors.New("start failed")
	}
	if got := runCheckedHook(context.Background(), "fixture-dir", "fixture-command", effects, time.Second, time.Millisecond); got != hookNotStarted {
		t.Fatal(got)
	}
}

func TestCheckedHookRunnerCanceledWaitStillReaps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	reaped := make(chan struct{})
	effects := hookRunnerEffects(func() error { <-release; close(reaped); return nil }, func(int) error { t.Fatal("canceled child was certified"); return syscall.ESRCH })
	start := effects.start
	effects.start = func(dir, command string) (ownedHookProcess, error) {
		process, err := start(dir, command)
		cancel()
		return process, err
	}
	if got := runCheckedHook(ctx, "", "", effects, time.Second, time.Millisecond); got != hookUncertain {
		t.Fatal(got)
	}
	close(release)
	select {
	case <-reaped:
	case <-time.After(time.Second):
		t.Fatal("owned child was not reaped after cancellation")
	}
}

func TestCheckedHookRunnerLiveOrReusedGroupTimesOut(t *testing.T) {
	probes := 0
	effects := hookRunnerEffects(func() error { return nil }, func(int) error { probes++; return nil })
	if got := runCheckedHook(context.Background(), "", "", effects, 10*time.Millisecond, time.Millisecond); got != hookUncertain || probes == 0 {
		t.Fatalf("outcome=%s probes=%d", got, probes)
	}
}

func TestCheckedHookRunnerCancellationAtAbsenceCannotCertify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	effects := hookRunnerEffects(func() error { return nil }, func(int) error { cancel(); return syscall.ESRCH })
	if got := runCheckedHook(ctx, "", "", effects, time.Second, time.Millisecond); got != hookUncertain {
		t.Fatal(got)
	}
}
