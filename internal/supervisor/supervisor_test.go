package supervisor

import (
	"net"
	"os"
	"path/filepath"
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
