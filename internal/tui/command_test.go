package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func commandFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gtl-helper")
	const script = `#!/bin/sh
mode=$1
shift
case "$mode" in
inspect)
  printf '%s\n' "$PWD" "$@" "$NO_COLOR" "$GTL_NO_UPDATE_NOTIFY" "$GTL_NO_STALE_WARN"
  cat
  ;;
fail)
  printf '\033[31msetup failed\033[0m\007\033]0;unsafe title\007\n' >&2
  exit 7
  ;;
large)
  head -c 196608 /dev/zero | tr '\000' x
  printf 'tail marker'
  ;;
cancel)
  trap 'printf "cleanup completed" >&2; exit 0' TERM
  (trap '' TERM; printf ready > "$1.child"; exec sleep 30) >/dev/null 2>&1 &
  printf '%s' "$!" > "$2"
  while [ ! -f "$1.child" ]; do sleep 0.01; done
  printf ready > "$1"
  while :; do sleep 0.01; done
  ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunCLISelectedDirectoryAndArguments(t *testing.T) {
	t.Setenv("NO_COLOR", "0")
	t.Setenv("GTL_NO_UPDATE_NOTIFY", "0")
	dir := t.TempDir()
	wantArgs := []string{"feature/a branch", "$(touch unexpected)", "; echo bad", "--literal"}
	output, err := runCLIExecutable(context.Background(), commandFixture(t), dir, append([]string{"inspect"}, wantArgs...)...)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{wantDir}, wantArgs...)
	want = append(want, "1", "1", "1")
	if got := strings.Split(strings.TrimSuffix(output, "\n"), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected child invocation: %q, want %q", got, want)
	}
}

func TestRunCLIErrorOutput(t *testing.T) {
	output, err := runCLIExecutable(context.Background(), commandFixture(t), t.TempDir(), "fail")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("expected exit 7, got %v", err)
	}
	if output != "setup failed\n" {
		t.Fatalf("expected sanitized failure output, got %q", output)
	}
}

func TestRunCLIBoundedOutput(t *testing.T) {
	output, err := runCLIExecutable(context.Background(), commandFixture(t), t.TempDir(), "large")
	if err != nil {
		t.Fatal(err)
	}
	const notice = "[Earlier output omitted]\n"
	if len(output) != commandOutputLimit+len(notice) || !strings.HasPrefix(output, notice) || !strings.HasSuffix(output, "tail marker") {
		t.Fatalf("incorrect output tail: length %d", len(output))
	}
}

func TestRunCLIGracefulCancellationCleansDescendants(t *testing.T) {
	executable := commandFixture(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	childPIDFile := filepath.Join(dir, "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		output string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, err := runCLIExecutable(ctx, executable, dir, "cancel", ready, childPIDFile)
		done <- result{output, err}
	}()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
waitReady:
	for {
		select {
		case <-tick.C:
			if _, err := os.Stat(ready); err == nil {
				break waitReady
			}
		case r := <-done:
			t.Fatalf("helper exited before ready: %+v", r)
		case <-deadline:
			t.Fatal("helper did not become ready")
		}
	}
	data, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	cancel()
	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) || !strings.Contains(r.output, "cleanup completed") {
			t.Fatalf("expected graceful cleanup and cancellation, got %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	deadline = time.After(3 * time.Second)
	for {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		// An orphan may await reaping by init after SIGKILL, but cannot run anymore.
		state, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(childPID)).Output()
		if strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
			return
		}
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatal("SIGTERM-ignoring descendant survived cancellation")
		}
	}
}

func TestRunCLIRejectsEmptyDirectory(t *testing.T) {
	if _, err := runCLI(context.Background(), "", "doctor"); err == nil {
		t.Fatal("expected missing worktree path error")
	}
}
