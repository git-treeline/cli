package cmd

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
)

func TestRunRefreshDryRunPreservesLegacyConfigRegistryAndEnv(t *testing.T) {
	worktree, reg := refreshRuntimeFixture(t, "project: app\nports_needed: 2\nenv:\n  PORT: '{port}'\n")
	envPath := filepath.Join(worktree, ".env.local")
	if err := os.WriteFile(envPath, []byte("KEEP=unchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(worktree, ".treeline.yml")
	configBefore := readRefreshFile(t, configPath)
	registryBefore := readRefreshFile(t, reg.Path)
	envBefore := readRefreshFile(t, envPath)

	oldDryRun, oldForce := refreshDryRun, refreshForce
	refreshDryRun, refreshForce = true, false
	t.Cleanup(func() { refreshDryRun, refreshForce = oldDryRun, oldForce })

	stdout, _, err := captureStdIO(t, runRefresh)
	if err != nil {
		t.Fatalf("runRefresh dry-run: %v", err)
	}
	if !strings.Contains(stdout, "Dry run — no changes made.") {
		t.Errorf("expected dry-run receipt, got:\n%s", stdout)
	}
	if got := readRefreshFile(t, configPath); string(got) != string(configBefore) {
		t.Errorf("legacy config changed during dry-run:\nwant %q\n got %q", configBefore, got)
	}
	if got := readRefreshFile(t, reg.Path); string(got) != string(registryBefore) {
		t.Errorf("registry changed during dry-run:\nwant %q\n got %q", registryBefore, got)
	}
	if got := readRefreshFile(t, envPath); string(got) != string(envBefore) {
		t.Errorf("env changed during dry-run:\nwant %q\n got %q", envBefore, got)
	}
}

func TestRunRefreshReportsSupervisorConfigureFailure(t *testing.T) {
	worktree, _ := refreshRuntimeFixture(t, "project: app\nport_count: 2\nenv:\n  PORT: '{port}'\n")
	requests := startRefreshSupervisor(t, worktree, "running", "error: rejected for test")

	oldDryRun, oldForce := refreshDryRun, refreshForce
	refreshDryRun, refreshForce = false, true
	t.Cleanup(func() { refreshDryRun, refreshForce = oldDryRun, oldForce })

	stdout, stderr, err := captureStdIO(t, runRefresh)
	if err == nil {
		var received []string
		for len(requests) > 0 {
			received = append(received, <-requests)
		}
		t.Fatalf("runRefresh succeeded despite supervisor configure failure; requests=%q\nstdout:\n%s\nstderr:\n%s", received, stdout, stderr)
	}
	if !strings.Contains(err.Error(), "1 worktree") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "Done. 0 succeeded, 1 failed.") {
		t.Errorf("expected failed summary, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "supervisor returned") {
		t.Errorf("expected configure failure on stderr, got:\n%s", stderr)
	}
	if got := <-requests; got != "status" {
		t.Errorf("first supervisor request = %q, want status", got)
	}
	if got := <-requests; !strings.HasPrefix(got, "configure-action:") {
		t.Errorf("second supervisor request = %q, want configure-action", got)
	}
}

func refreshRuntimeFixture(t *testing.T, projectConfig string) (string, *registry.Registry) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GTL_HOME", filepath.Join(root, "gtl-home"))
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".treeline.yml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := registry.New("")
	if err := reg.Allocate(registry.Allocation{
		"worktree": worktree,
		"project":  "app",
		"branch":   "main",
		"port":     float64(43100),
		"ports":    []any{float64(43100)},
	}); err != nil {
		t.Fatal(err)
	}
	return worktree, reg
}

func startRefreshSupervisor(t *testing.T, worktree string, replies ...string) <-chan string {
	t.Helper()
	socket := supervisor.SocketPath(worktree)
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	})

	requests := make(chan string, len(replies))
	go func() {
		defer close(requests)
		for _, reply := range replies {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			request, _ := io.ReadAll(conn)
			requests <- string(request)
			_, _ = conn.Write([]byte(reply))
			_ = conn.Close()
		}
	}()
	return requests
}

func readRefreshFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
