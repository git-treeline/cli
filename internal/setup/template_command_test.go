package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunTemplateMigration_Command(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input"), []byte("checkout"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	err := runTemplateMigration(context.Background(), dir,
		`printf '%s:' "$MIGRATION_TEST"; cat input; printf ':stderr' >&2`,
		append(os.Environ(), "MIGRATION_TEST=environment"), &log)
	if err != nil {
		t.Fatal(err)
	}
	if got := log.String(); got != "environment:checkout:stderr" {
		t.Fatalf("migration output = %q", got)
	}
}

func TestRunTemplateMigration_NonzeroExit(t *testing.T) {
	err := runTemplateMigration(context.Background(), t.TempDir(), "exit 7", os.Environ(), io.Discard)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("migration error = %v; want exit status 7", err)
	}
}

func TestRunTemplateMigration_CanceledBeforeStart(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runTemplateMigration(ctx, dir, "touch started", os.Environ(), io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("migration error = %v; want context cancellation", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled migration started: %v", err)
	}
}

func TestRunTemplateMigration_AllowsTermCleanup(t *testing.T) {
	dir := t.TempDir()
	cancel, done := startTemplateMigrationTest(t, dir, `
trap 'printf terminated > terminated; exit 0' TERM
printf '%s' "$$" > ready
while :; do sleep 0.05; done
`)
	waitTemplateMigrationPID(t, filepath.Join(dir, "ready"))
	cancel()
	if err := waitTemplateMigrationResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("migration error = %v; want context cancellation", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "terminated")); err != nil {
		t.Fatalf("migration did not finish its TERM trap: %v", err)
	}
}

func TestRunTemplateMigration_KillsChildAfterParentExits(t *testing.T) {
	dir := t.TempDir()
	cancel, done := startTemplateMigrationTest(t, dir, `
trap 'printf terminated > parent-terminated; exit 0' TERM
sh -c 'trap "" TERM; printf "%s" "$$" > child; while :; do sleep 1; done' &
printf '%s' "$$" > ready
wait
`)
	parent := waitTemplateMigrationPID(t, filepath.Join(dir, "ready"))
	child := waitTemplateMigrationPID(t, filepath.Join(dir, "child"))
	t.Cleanup(func() {
		if templateMigrationProcessRunning(child) {
			_ = syscall.Kill(-parent, syscall.SIGKILL)
		}
	})
	cancel()
	if err := waitTemplateMigrationResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("migration error = %v; want context cancellation", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "parent-terminated")); err != nil {
		t.Fatalf("parent did not exit through its TERM trap: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for templateMigrationProcessRunning(child) {
		if time.Now().After(deadline) {
			t.Fatal("TERM-resistant child still running after migration returned")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startTemplateMigrationTest(t *testing.T, dir, command string) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- runTemplateMigration(ctx, dir, command, os.Environ(), io.Discard)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			if data, err := os.ReadFile(filepath.Join(dir, "ready")); err == nil {
				if pid, err := strconv.Atoi(string(data)); err == nil && pid > 0 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Error("migration did not finish after forced cleanup")
			}
			t.Error("migration did not stop after cancellation")
		}
	})
	return cancel, done
}

func waitTemplateMigrationResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not return after cancellation")
		return nil
	}
}

func waitTemplateMigrationPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("migration did not write PID to %s", path)
	return 0
}

func templateMigrationProcessRunning(pid int) bool {
	if syscall.Kill(pid, 0) == syscall.ESRCH {
		return false
	}
	// Orphaned children can remain zombies until the system reaps them.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	return err == nil && !strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
