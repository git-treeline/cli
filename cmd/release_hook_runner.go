package cmd

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

const checkedHookTimeout = 5 * time.Minute
const checkedHookPollInterval = 25 * time.Millisecond

type ownedHookProcess struct {
	group int
	wait  func() error
}

type checkedHookEffects struct {
	start       func(string, string) (ownedHookProcess, error)
	groupExists func(int) error
}

// RunCheckedHook certifies the ordinary process group created for this hook.
// Commands that deliberately escape that group are outside this boundary.
func RunCheckedHook(ctx context.Context, dir, command string) checkedHookOutcome {
	return runCheckedHook(ctx, dir, command, checkedHookEffects{
		start: func(dir, command string) (ownedHookProcess, error) {
			child := exec.Command("sh", "-c", command)
			child.Dir = dir
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := child.Start(); err != nil {
				return ownedHookProcess{}, err
			}
			return ownedHookProcess{group: child.Process.Pid, wait: child.Wait}, nil
		},
		groupExists: func(group int) error { return syscall.Kill(-group, 0) },
	}, checkedHookTimeout, checkedHookPollInterval)
}

func runCheckedHook(ctx context.Context, dir, command string, effects checkedHookEffects, timeout, poll time.Duration) checkedHookOutcome {
	if ctx == nil || effects.start == nil || effects.groupExists == nil || timeout <= 0 || poll <= 0 {
		return hookNotStarted
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if ctx.Err() != nil {
		return hookNotStarted
	}
	process, err := effects.start(dir, command)
	if err != nil {
		return hookNotStarted
	}
	if process.wait == nil {
		return hookUncertain
	}
	// Wait owns the launched child, not a later process with the same PID. Keep
	// reaping after cancellation, without signaling a potentially unknown group.
	done := make(chan error, 1)
	go func() { done <- process.wait() }()
	if process.group <= 0 {
		return hookUncertain
	}
	var waitErr error
	select {
	case <-ctx.Done():
		return hookUncertain
	case waitErr = <-done:
	}
	if ctx.Err() != nil {
		return hookUncertain
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			return hookUncertain
		}
	}
	for {
		if ctx.Err() != nil {
			return hookUncertain
		}
		err := effects.groupExists(process.group)
		if ctx.Err() != nil {
			return hookUncertain
		}
		if errors.Is(err, syscall.ESRCH) {
			if waitErr != nil {
				return hookSettledFailed
			}
			return hookSettledOK
		}
		// A reused PGID can only delay certification; it never grants permission
		// to signal. EPERM and all other observation errors remain ambiguous.
		if err != nil {
			return hookUncertain
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return hookUncertain
		case <-timer.C:
		}
	}
}
