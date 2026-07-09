package supervisor

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisor_StopAndResume(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	marker := filepath.Join(dir, "started")

	cmd := "echo $$ >> " + marker + " && sleep 60"
	sv := New(cmd, dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	resp, err := Send(sock, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if resp != "running" {
		t.Errorf("expected running, got %s", resp)
	}

	// Stop child — supervisor stays alive
	resp, err = Send(sock, "stop")
	if err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	time.Sleep(200 * time.Millisecond)

	resp, err = Send(sock, "status")
	if err != nil {
		t.Fatalf("status after stop failed: %v", err)
	}
	if resp != "stopped" {
		t.Errorf("expected stopped after stop, got %s", resp)
	}

	// Resume via start
	resp, err = Send(sock, "start")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok from start, got %s", resp)
	}

	time.Sleep(500 * time.Millisecond)

	resp, err = Send(sock, "status")
	if err != nil {
		t.Fatalf("status after resume failed: %v", err)
	}
	if resp != "running" {
		t.Errorf("expected running after resume, got %s", resp)
	}

	data, _ := os.ReadFile(marker)
	lines := splitNonEmpty(string(data))
	if len(lines) < 2 {
		t.Errorf("expected at least 2 PIDs (start + resume), got %d", len(lines))
	}

	// Shutdown supervisor entirely
	_, _ = Send(sock, "shutdown")
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor didn't exit after shutdown")
	}
}

func TestSupervisor_Shutdown(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	sv := New("sleep 60", dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	resp, err := Send(sock, "shutdown")
	if err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("supervisor returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor didn't exit after shutdown")
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("expected socket to be cleaned up")
	}
}

func TestSupervisor_Restart(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	marker := filepath.Join(dir, "started")

	// Command creates a marker file with PID, then sleeps
	cmd := "echo $$ >> " + marker + " && sleep 60"
	sv := New(cmd, dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	resp, err := Send(sock, "restart")
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	// Wait for second start
	time.Sleep(500 * time.Millisecond)

	data, _ := os.ReadFile(marker)
	lines := splitNonEmpty(string(data))
	if len(lines) < 2 {
		t.Errorf("expected at least 2 PIDs (start + restart), got %d: %q", len(lines), string(data))
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_UpdateEnv(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	envOut := filepath.Join(dir, "env.out")

	cmd := "env > " + envOut + " && sleep 60"
	sv := New(cmd, dir, sock)
	sv.Env = map[string]string{"GTL_TEST": "original"}
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, envOut, 2*time.Second)

	data, _ := os.ReadFile(envOut)
	if !strings.Contains(string(data), "GTL_TEST=original") {
		t.Fatalf("expected GTL_TEST=original in initial env, got:\n%s", data)
	}

	// Update env via socket
	resp, err := Send(sock, "update-env:GTL_TEST=updated\x00GTL_NEW=added")
	if err != nil {
		t.Fatalf("update-env failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	// Restart so the child picks up the new env
	resp, err = Send(sock, "restart")
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok from restart, got %s", resp)
	}

	time.Sleep(500 * time.Millisecond)

	data, _ = os.ReadFile(envOut)
	if !strings.Contains(string(data), "GTL_TEST=updated") {
		t.Errorf("expected GTL_TEST=updated after restart, got:\n%s", data)
	}
	if !strings.Contains(string(data), "GTL_NEW=added") {
		t.Errorf("expected GTL_NEW=added after restart, got:\n%s", data)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_GetCommand(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	cmd := "sleep 60"
	sv := New(cmd, dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	resp, err := Send(sock, "get-command")
	if err != nil {
		t.Fatalf("get-command failed: %v", err)
	}
	if resp != cmd {
		t.Errorf("get-command returned %q, want %q", resp, cmd)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_UpdateEnvEmpty(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	sv := New("sleep 60", dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	resp, err := Send(sock, "update-env:")
	if err != nil {
		t.Fatalf("update-env with empty payload failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_StatusWhenStopped(t *testing.T) {
	_, err := Send("/nonexistent/test.sock", "status")
	if err == nil {
		t.Error("expected error connecting to nonexistent socket")
	}
}

// TestSupervisor_ChildPidFileLifecycle verifies the child pgid sidecar is
// written while a child runs and removed once it stops, so a force-kill can
// reap the child process group.
func TestSupervisor_ChildPidFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	childPidPath := ChildPidPath(sock)

	sv := New("sleep 60", dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, childPidPath, 2*time.Second)

	data, err := os.ReadFile(childPidPath)
	if err != nil {
		t.Fatalf("reading child pid file: %v", err)
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pgid <= 1 {
		t.Fatalf("expected a valid pgid in %s, got %q", childPidPath, string(data))
	}
	// The persisted value must be a real process group leader — that's what
	// makes syscall.Kill(-pgid, ...) reap the whole group.
	if got, err := syscall.Getpgid(pgid); err != nil || got != pgid {
		t.Fatalf("persisted value %d is not a group leader (getpgid=%d err=%v)", pgid, got, err)
	}

	// Stopping the child must remove the sidecar.
	if _, err := Send(sock, "stop"); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	waitForFileGone(t, childPidPath, 2*time.Second)

	_, _ = Send(sock, "shutdown")
	<-errCh
}

// TestSupervisor_StartDuringStopSpawnsNoExtraChild reproduces the stop/start
// race: while restart is inside stopChildLocked (s.mu released, waiting for the
// old child to exit), a competing 'start' arrives. Only one child — the one
// restart starts after the stop completes — must end up tracked and running.
func TestSupervisor_StartDuringStopSpawnsNoExtraChild(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	marker := filepath.Join(dir, "started")

	// On SIGTERM, linger ~1s before exiting so the stop window stays open long
	// enough for a racing 'start' to land inside it.
	cmd := "trap 'sleep 1; exit 0' TERM; echo $$ >> " + marker + "; sleep 60"
	sv := New(cmd, dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	// Kick off a restart; it enters the stop window immediately (s.mu released
	// before the SIGTERM linger). Fire a competing start mid-window.
	go func() { _, _ = Send(sock, "restart") }()
	time.Sleep(300 * time.Millisecond)
	if _, err := Send(sock, "start"); err != nil {
		t.Fatalf("racing start failed: %v", err)
	}

	// Let the SIGTERM linger elapse and restart start its fresh child.
	time.Sleep(2 * time.Second)

	resp, err := Send(sock, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if resp != "running" {
		t.Errorf("expected running after restart, got %s", resp)
	}

	// Exactly two starts total: the initial child and restart's fresh child.
	// A third line means the racing start spawned an untracked competitor.
	data, _ := os.ReadFile(marker)
	if lines := splitNonEmpty(string(data)); len(lines) != 2 {
		t.Errorf("expected 2 child starts (initial + restart), got %d: %q", len(lines), string(data))
	}

	_, _ = Send(sock, "shutdown")
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("supervisor didn't exit after shutdown")
	}
}

// tmpSocket returns a short /tmp socket path (macOS caps unix socket paths at
// ~104 bytes, which t.TempDir() paths exceed) and registers its cleanup.
func tmpSocket(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("/tmp", "gtl-test-*.sock")
	if err != nil {
		t.Fatalf("create temp sock: %v", err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() {
		_ = os.Remove(sock)
		_ = os.Remove(ChildPidPath(sock))
		_ = os.Remove(PidPath(sock))
	})
	return sock
}

func waitForFileGone(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s still present after %s", path, timeout)
}

func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("socket %s not created within %s", path, timeout)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s not created within %s", path, timeout)
}

func TestSupervisor_SIGHUPShutdown(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	sv := New("sleep 60", dir, sock)
	sv.Log = func(f string, a ...any) {}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	// SIGHUP should trigger graceful shutdown identical to SIGINT/SIGTERM.
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGHUP)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("supervisor returned error on SIGHUP: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor didn't exit after SIGHUP")
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("expected socket to be cleaned up after SIGHUP")
	}
}

// TestSupervisor_WriteDeadlineUnblocksLock verifies that a hung client
// (connects, sends a command, never reads the response) doesn't hold the
// supervisor's mutex indefinitely. A subsequent "status" query must succeed
// once the write deadline fires on the stuck connection.
func TestSupervisor_WriteDeadlineUnblocksLock(t *testing.T) {
	dir := t.TempDir()
	// macOS caps unix socket paths at ~104 bytes; t.TempDir() paths exceed that.
	f, err := os.CreateTemp("/tmp", "gtl-test-*.sock")
	if err != nil {
		t.Fatalf("create temp sock: %v", err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })

	sv := New("sleep 60", dir, sock)
	sv.Log = func(f string, a ...any) {}
	sv.ConnWriteDeadline = 200 * time.Millisecond // fast deadline for the test

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)

	// Connect and send "restart" but never read the response — simulates a
	// client that disappears mid-command (e.g. gtl stop timing out).
	hung, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = hung.Close() }()
	_, _ = hung.Write([]byte("restart"))
	// Deliberately not reading the response.

	// Wait for the write deadline to fire and the handleConn goroutine to exit.
	time.Sleep(400 * time.Millisecond)

	// The mutex must be free now — status should respond immediately.
	resp, err := SendWithTimeout(sock, "status", 2*time.Second)
	if err != nil {
		t.Fatalf("status after hung client: %v", err)
	}
	if resp != "running" && resp != "stopped" {
		t.Errorf("unexpected status response: %q", resp)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func splitNonEmpty(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}
