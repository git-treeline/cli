//go:build e2e

// Package e2e drives the actual built gtl binary through the full worktree
// lifecycle (setup → start → status → list → stop → release) against a
// throwaway git repo and an isolated GTL_HOME. It asserts real observable
// state — registry entries, a listening port, a live supervisor socket — at
// each step, covering the seams the layer unit tests can't.
//
// Gated behind the `e2e` build tag so `go test ./...` never runs it: it is
// slower and needs a Go toolchain (to build the port-binder helper) plus the
// ability to bind a local TCP port.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// socketPath mirrors internal/supervisor.SocketPath: a short, deterministic
// /tmp path derived from the first 8 bytes of the SHA-256 of the worktree
// path. Reproduced here (not imported) so the test asserts against the same
// on-disk artifact the binary creates, independent of internal packages.
func socketPath(worktreePath string) string {
	if resolved, err := filepath.EvalSymlinks(worktreePath); err == nil {
		worktreePath = resolved
	}
	h := sha256.Sum256([]byte(worktreePath))
	return fmt.Sprintf("/tmp/gtl-%d/gtl-%x.sock", os.Geteuid(), h[:8])
}

func supervisorPIDPath(socket string) string {
	return strings.TrimSuffix(socket, ".sock") + ".pid"
}

func childPIDPath(socket string) string {
	return strings.TrimSuffix(socket, ".sock") + ".child.pid"
}

func supervisorPID(t *testing.T, socket string) string {
	t.Helper()
	data, err := os.ReadFile(supervisorPIDPath(socket))
	if err != nil {
		t.Fatalf("reading supervisor PID: %v", err)
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		t.Fatal("supervisor PID file was empty")
	}
	return pid
}

func hooksStatePath(socket string) string {
	return strings.TrimSuffix(socket, ".sock") + ".hooks"
}

// gtlEnv builds the hermetic environment shared by every subprocess call:
// an isolated GTL_HOME (registry + config live here, nothing touches the
// developer's real state) and a temp HOME. GTL_NO_STALE_WARN silences the
// router-version banner so stderr stays clean for diagnostics.
func gtlEnv(gtlHome, home string) []string {
	env := os.Environ()
	// Drop any inherited overrides so the test is self-contained.
	filtered := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "GTL_HOME=") ||
			strings.HasPrefix(kv, "HOME=") ||
			strings.HasPrefix(kv, "GTL_NO_STALE_WARN=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return append(filtered,
		"GTL_HOME="+gtlHome,
		"HOME="+home,
		"GTL_NO_STALE_WARN=1",
	)
}

// runGtl invokes the binary synchronously in dir and returns combined output.
func runGtl(t *testing.T, bin, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// portListening reports whether something is accepting connections on port.
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// waitFor polls cond until it is true or the deadline elapses.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}

func TestWorktreeLifecycle(t *testing.T) {
	// --- Preconditions: skip cleanly if the host lacks required tools. ---
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available — skipping e2e lifecycle test")
	}

	// --- Build the binary under test. ---
	binDir := t.TempDir()
	gtl := filepath.Join(binDir, "gtl")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", gtl, "github.com/git-treeline/cli")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gtl binary failed: %v\n%s", err, out)
	}

	// Build a tiny, stdlib-only port binder to use as the worktree's start
	// command. A compiled helper (instead of `python3 -m http.server`) keeps
	// the test deterministic across CI runners, where python availability and
	// startup speed vary — the binder binds 127.0.0.1:<port> instantly and
	// exits on SIGTERM so `gtl stop`/`release` observe the port freeing.
	binderDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binderDir, "go.mod"), []byte("module portbinder\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("writing binder go.mod: %v", err)
	}
	binderSrc := `package main

import (
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+os.Args[1])
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = ln.Close() }()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	<-sigs
}
`
	if err := os.WriteFile(filepath.Join(binderDir, "main.go"), []byte(binderSrc), 0o644); err != nil {
		t.Fatalf("writing binder source: %v", err)
	}
	binder := filepath.Join(binderDir, "portbinder")
	binderBuild := exec.CommandContext(buildCtx, "go", "build", "-o", binder, ".")
	binderBuild.Dir = binderDir
	binderBuild.Env = os.Environ()
	if out, err := binderBuild.CombinedOutput(); err != nil {
		t.Fatalf("building port binder failed: %v\n%s", err, out)
	}

	// --- Hermetic state: isolated GTL_HOME + HOME. ---
	gtlHome := t.TempDir()
	home := t.TempDir()
	env := gtlEnv(gtlHome, home)

	// --- Throwaway git repo. Resolve symlinks so the path the CLI records
	// (os.Getwd of the child) matches what we pass back on later calls; macOS
	// temp dirs live under /var -> /private/var. ---
	rawRepo := t.TempDir()
	repo, err := filepath.EvalSymlinks(rawRepo)
	if err != nil {
		t.Fatalf("resolving repo path: %v", err)
	}
	gitInit(t, repo, env)

	// --- Minimal .treeline.yml: a single port, an env file, and a start
	// command that binds the allocated port via the compiled port binder.
	// The release hooks leave marker files so STEP 6 can assert they fired;
	// pre_release deliberately fails after its marker so the same step also
	// proves --force releases anyway instead of aborting on a broken hook. ---
	treeline := "" +
		"project: e2elifecycle\n" +
		"port_count: 1\n" +
		"env_file: .env.local\n" +
		"env:\n" +
		"  PORT: \"{port}\"\n" +
		"  APP_URL: \"http://localhost:{port}\"\n" +
		"commands:\n" +
		"  start: " + binder + " {port}\n" +
		"hooks:\n" +
		"  pre_release:\n" +
		"    - touch pre_release.ran\n" +
		"    - exit 7\n" +
		"  post_release:\n" +
		"    - touch post_release.ran\n"
	if err := os.WriteFile(filepath.Join(repo, ".treeline.yml"), []byte(treeline), 0o644); err != nil {
		t.Fatalf("writing .treeline.yml: %v", err)
	}

	// =====================================================================
	// STEP 1: gtl setup — allocation appears in the registry, env file written.
	// =====================================================================
	out, err := runGtl(t, gtl, repo, env, "setup")
	if err != nil {
		t.Fatalf("gtl setup failed: %v\n%s", err, out)
	}

	registryPath := filepath.Join(gtlHome, "registry.json")
	if _, err := os.Stat(registryPath); err != nil {
		t.Fatalf("registry.json not created at %s after setup: %v", registryPath, err)
	}

	alloc := findAllocation(t, gtl, repo, env)
	port := allocPort(t, alloc)
	if port <= 0 {
		t.Fatalf("setup did not allocate a usable port; alloc=%v", alloc)
	}
	t.Logf("STEP 1 ok: allocated port %d for %s", port, repo)

	// Env file must exist and carry the allocated port.
	envFile := filepath.Join(repo, ".env.local")
	envData, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("env file %s not written by setup: %v", envFile, err)
	}
	if !strings.Contains(string(envData), fmt.Sprintf("PORT=\"%d\"", port)) {
		t.Fatalf("env file missing PORT=%d; contents:\n%s", port, envData)
	}

	// =====================================================================
	// STEP 1b: a fresh `start --await` must leave an independently owned
	// supervisor behind after the command exits. A later CLI invocation can
	// stop it; this is deliberately a compiled-binary assertion because an
	// in-process supervisor goroutine hides the original ownership bug.
	// =====================================================================
	sockPath := socketPath(repo)
	t.Cleanup(func() {
		// Register before the first detached start so an assertion in this block
		// cannot strand a supervisor or its child outside the test process.
		_, _ = runGtl(t, gtl, repo, env, "stop", "--kill")
	})
	out, err = runGtl(t, gtl, repo, env, "start", "--await", "--await-timeout", "10")
	if err != nil {
		t.Fatalf("fresh gtl start --await failed: %v\n%s", err, out)
	}
	if !portListening(port) {
		t.Fatalf("port %d is not listening after gtl start --await exited", port)
	}
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("supervisor socket missing after gtl start --await exited: %v", err)
	}
	awaitPID := supervisorPID(t, sockPath)
	out, err = runGtl(t, gtl, repo, env, "stop")
	if err != nil {
		t.Fatalf("gtl stop after await failed: %v\n%s", err, out)
	}
	if !waitFor(10*time.Second, func() bool { return !portListening(port) }) {
		t.Fatalf("port %d remained listening after stopping await supervisor", port)
	}
	if got := supervisorPID(t, sockPath); got != awaitPID {
		t.Fatalf("plain stop replaced supervisor PID: got %s, want %s", got, awaitPID)
	}
	out, err = runGtl(t, gtl, repo, env, "start", "--await", "--await-timeout", "10")
	if err != nil {
		t.Fatalf("resuming stopped supervisor with --await failed: %v\n%s", err, out)
	}
	if got := supervisorPID(t, sockPath); got != awaitPID {
		t.Fatalf("start --await replaced stopped supervisor PID: got %s, want %s", got, awaitPID)
	}
	if !portListening(port) {
		t.Fatalf("port %d is not listening after resuming supervisor", port)
	}
	out, err = runGtl(t, gtl, repo, env, "stop", "--kill")
	if err != nil {
		t.Fatalf("gtl stop --kill after resumed await failed: %v\n%s", err, out)
	}
	if !waitFor(10*time.Second, func() bool {
		_, err := os.Stat(sockPath)
		return os.IsNotExist(err)
	}) {
		t.Fatalf("supervisor socket %s survived stop --kill after await", sockPath)
	}
	if portListening(port) {
		t.Fatalf("port %d still listening after stop --kill of await supervisor", port)
	}
	t.Logf("STEP 1b ok: start --await exited with a controllable detached supervisor that resumes in place")

	// Two fresh starts must serialize before hooks run. A slow auto hook makes
	// the race reproducible; one invocation may report startup-in-progress or
	// may simply await the winner, but the project hook runs only once.
	concurrentConfig := strings.Replace(treeline, "hooks:\n", "hooks:\n  concurrent_start:\n    auto: true\n    pre_start: sleep 1; echo pre >> concurrent_hooks.log\n    post_stop: echo post >> concurrent_hooks.log\n", 1)
	if err := os.WriteFile(filepath.Join(repo, ".treeline.yml"), []byte(concurrentConfig), 0o644); err != nil {
		t.Fatalf("writing concurrent start config: %v", err)
	}
	type awaitResult struct {
		out string
		err error
	}
	results := make(chan awaitResult, 2)
	gate := make(chan struct{})
	for range 2 {
		go func() {
			<-gate
			out, err := runGtl(t, gtl, repo, env, "start", "--await", "--await-timeout", "10")
			results <- awaitResult{out: out, err: err}
		}()
	}
	close(gate)
	successes := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
			continue
		}
		if result.out == "" {
			t.Fatalf("concurrent start failed without diagnostic: %v", result.err)
		}
	}
	if successes == 0 {
		t.Fatal("both concurrent start --await commands failed")
	}
	if !waitFor(10*time.Second, func() bool { return portListening(port) }) {
		t.Fatal("concurrent start did not leave the server listening")
	}
	out, err = runGtl(t, gtl, repo, env, "stop", "--kill")
	if err != nil {
		t.Fatalf("stopping concurrent start winner: %v\n%s", err, out)
	}
	concurrentHooks := filepath.Join(repo, "concurrent_hooks.log")
	if !waitFor(5*time.Second, func() bool {
		data, err := os.ReadFile(concurrentHooks)
		return err == nil && string(data) == "pre\npost\n"
	}) {
		data, err := os.ReadFile(concurrentHooks)
		t.Fatalf("concurrent start hooks = %q, %v; want one pre/post pair", data, err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".treeline.yml"), []byte(treeline), 0o644); err != nil {
		t.Fatalf("restoring start config after concurrent start: %v", err)
	}
	t.Logf("STEP 1c ok: concurrent fresh start ran one hook pair")

	// A readiness timeout owns and cleans up only the detached process it just
	// created. The socket/PID sidecars and private launch config cannot survive
	// it and block a later foreground start.
	timeoutConfig := strings.Replace(treeline, "  start: "+binder+" {port}", "  start: sleep 60", 1)
	timeoutConfig = strings.Replace(timeoutConfig, "hooks:\n", "hooks:\n  await_cleanup:\n    auto: true\n    pre_start: echo pre >> await_hooks.log\n    post_stop: echo post >> await_hooks.log\n", 1)
	if err := os.WriteFile(filepath.Join(repo, ".treeline.yml"), []byte(timeoutConfig), 0o644); err != nil {
		t.Fatalf("writing timeout config: %v", err)
	}
	out, err = runGtl(t, gtl, repo, env, "start", "--await", "--await-timeout", "1")
	if err == nil {
		t.Fatalf("gtl start --await unexpectedly succeeded without a listening server:\n%s", out)
	}
	for _, path := range []string{sockPath, supervisorPIDPath(sockPath), childPIDPath(sockPath)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("await timeout left runtime artifact %s: %v\n%s", path, err, out)
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(gtlHome, ".supervisor-*.json")); len(leftovers) != 0 {
		t.Fatalf("await timeout left private runtime config: %v", leftovers)
	}
	hooksLog := filepath.Join(repo, "await_hooks.log")
	if data, err := os.ReadFile(hooksLog); err != nil || string(data) != "pre\npost\n" {
		t.Fatalf("await timeout hooks = %q, %v; want one pre/post pair", data, err)
	}

	// Cancellation follows the same ownership rule as timeout: signal only the
	// waiting CLI, then require its newly-created detached supervisor, config,
	// and hook state to be gone before another command can start.
	cancelCmd := exec.Command(gtl, "start", "--await", "--await-timeout", "60")
	cancelCmd.Dir = repo
	cancelCmd.Env = env
	var cancelOutput bytes.Buffer
	cancelCmd.Stdout = &cancelOutput
	cancelCmd.Stderr = &cancelOutput
	if err := cancelCmd.Start(); err != nil {
		t.Fatalf("starting cancellable await command: %v", err)
	}
	if !waitFor(5*time.Second, func() bool {
		_, err := os.Stat(sockPath)
		return err == nil
	}) {
		t.Fatalf("cancellable await never created supervisor socket:\n%s", cancelOutput.String())
	}
	if err := cancelCmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("cancelling await command: %v", err)
	}
	cancelled := make(chan error, 1)
	go func() { cancelled <- cancelCmd.Wait() }()
	select {
	case err := <-cancelled:
		if err == nil {
			t.Fatalf("cancelled await unexpectedly exited 0:\n%s", cancelOutput.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("cancelled await did not exit:\n%s", cancelOutput.String())
	}
	for _, path := range []string{sockPath, supervisorPIDPath(sockPath), childPIDPath(sockPath), hooksStatePath(sockPath), hooksStatePath(sockPath) + ".running"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cancelled await left runtime artifact %s: %v\n%s", path, err, cancelOutput.String())
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(gtlHome, ".supervisor-*.json")); len(leftovers) != 0 {
		t.Fatalf("cancelled await left private runtime config: %v", leftovers)
	}
	if data, err := os.ReadFile(hooksLog); err != nil || string(data) != "pre\npost\npre\npost\n" {
		t.Fatalf("cancelled await hooks = %q, %v; want one additional pre/post pair", data, err)
	}
	for _, path := range []string{hooksStatePath(sockPath), hooksStatePath(sockPath) + ".running"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("await timeout left hook state %s: %v", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".treeline.yml"), []byte(treeline), 0o644); err != nil {
		t.Fatalf("restoring start config: %v", err)
	}
	t.Logf("STEP 1d ok: await timeout cleaned its supervisor and runtime state")

	// Ensure teardown even if a later assertion fails: kill supervisor + free port.
	sockPath = ""
	defer func() {
		// Best-effort supervisor shutdown so no process outlives the test.
		_, _ = runGtl(t, gtl, repo, env, "stop", "--kill")
		if sockPath != "" {
			_ = os.Remove(sockPath)
		}
	}()

	// =====================================================================
	// STEP 2: gtl start (supervised, backgrounded like a terminal would run
	// it) — the port becomes listening and a supervisor socket appears.
	// =====================================================================
	startCtx, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	startCmd := exec.CommandContext(startCtx, gtl, "start")
	startCmd.Dir = repo
	startCmd.Env = env
	startLog, err := os.Create(filepath.Join(binDir, "start.log"))
	if err != nil {
		t.Fatalf("creating start log: %v", err)
	}
	defer startLog.Close()
	startCmd.Stdout = startLog
	startCmd.Stderr = startLog
	if err := startCmd.Start(); err != nil {
		t.Fatalf("launching gtl start: %v", err)
	}
	startExited := make(chan error, 1)
	go func() { startExited <- startCmd.Wait() }()
	defer func() {
		cancelStart()
		select {
		case <-startExited:
		case <-time.After(5 * time.Second):
			if startCmd.Process != nil {
				_ = startCmd.Process.Kill()
			}
		}
	}()

	sockPath = socketPath(repo)
	if !waitFor(30*time.Second, func() bool {
		_, statErr := os.Stat(sockPath)
		return statErr == nil
	}) {
		dumpLog(t, startLog.Name())
		t.Fatalf("supervisor socket %s never appeared after gtl start", sockPath)
	}
	if !waitFor(30*time.Second, func() bool { return portListening(port) }) {
		dumpLog(t, startLog.Name())
		t.Fatalf("port %d never started listening after gtl start", port)
	}
	t.Logf("STEP 2 ok: port %d listening, supervisor socket at %s", port, sockPath)

	// =====================================================================
	// STEP 3: gtl status --json — worktree shows up with its port and a
	// running supervisor.
	// =====================================================================
	out, err = runGtl(t, gtl, repo, env, "status", "--json")
	if err != nil {
		t.Fatalf("gtl status --json failed: %v\n%s", err, out)
	}
	var statusEntries []map[string]any
	if err := json.Unmarshal([]byte(out), &statusEntries); err != nil {
		t.Fatalf("parsing status --json: %v\noutput:\n%s", err, out)
	}
	statusEntry := findByWorktree(statusEntries, repo)
	if statusEntry == nil {
		t.Fatalf("worktree %s not found in status --json:\n%s", repo, out)
	}
	if got := anyInt(statusEntry["port"]); got != port {
		t.Fatalf("status port = %d, want %d", got, port)
	}
	if listening, _ := statusEntry["listening"].(bool); !listening {
		t.Fatalf("status did not report the port as listening: %v", statusEntry["listening"])
	}
	if sup, _ := statusEntry["supervisor"].(string); sup != "running" {
		t.Fatalf("status supervisor = %q, want \"running\"", sup)
	}
	t.Logf("STEP 3 ok: status --json reports port %d listening, supervisor running", port)

	// =====================================================================
	// STEP 4: gtl list --json — the worktree is listed and reported "up".
	// =====================================================================
	out, err = runGtl(t, gtl, repo, env, "list", "--json")
	if err != nil {
		t.Fatalf("gtl list --json failed: %v\n%s", err, out)
	}
	var listEntries []map[string]any
	if err := json.Unmarshal([]byte(out), &listEntries); err != nil {
		t.Fatalf("parsing list --json: %v\noutput:\n%s", err, out)
	}
	listEntry := findByPath(listEntries, repo)
	if listEntry == nil {
		t.Fatalf("worktree %s not found in list --json:\n%s", repo, out)
	}
	if status, _ := listEntry["status"].(string); status != "up" {
		t.Fatalf("list status = %q, want \"up\"", status)
	}
	if !containsInt(anyIntSlice(listEntry["ports"]), port) {
		t.Fatalf("list ports %v missing allocated port %d", listEntry["ports"], port)
	}
	t.Logf("STEP 4 ok: list --json shows worktree up on port %d", port)

	// =====================================================================
	// STEP 4b: gtl doctor --json — the diagnosis engine runs end-to-end and
	// reports this worktree's real runtime state (machine + project sections,
	// allocated port, live supervisor). Guards the doctor wiring the unit
	// tests can't reach: cmd gathering → internal/doctor engine → JSON out.
	// =====================================================================
	out, err = runGtl(t, gtl, repo, env, "doctor", "--json")
	if err != nil {
		t.Fatalf("gtl doctor --json failed: %v\n%s", err, out)
	}
	var diag struct {
		Machine map[string]any `json:"machine"`
		Project struct {
			Allocation struct {
				Ports []any `json:"ports"`
			} `json:"allocation"`
			Runtime struct {
				Supervisor string `json:"supervisor"`
				Listening  bool   `json:"listening"`
			} `json:"runtime"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(out), &diag); err != nil {
		t.Fatalf("parsing doctor --json: %v\noutput:\n%s", err, out)
	}
	if diag.Machine == nil {
		t.Fatalf("doctor --json missing machine section:\n%s", out)
	}
	if !containsInt(anyIntSlice(diag.Project.Allocation.Ports), port) {
		t.Fatalf("doctor --json allocation ports %v missing %d", diag.Project.Allocation.Ports, port)
	}
	if diag.Project.Runtime.Supervisor != "running" {
		t.Fatalf("doctor --json supervisor = %q, want \"running\"", diag.Project.Runtime.Supervisor)
	}
	if !diag.Project.Runtime.Listening {
		t.Fatalf("doctor --json reports port %d not listening while the server is up", port)
	}
	t.Logf("STEP 4b ok: doctor --json reports live allocation on port %d", port)

	// =====================================================================
	// STEP 5: gtl stop — server stops (port no longer listening) but the
	// allocation remains in the registry.
	// =====================================================================
	out, err = runGtl(t, gtl, repo, env, "stop")
	if err != nil {
		t.Fatalf("gtl stop failed: %v\n%s", err, out)
	}
	if !waitFor(15*time.Second, func() bool { return !portListening(port) }) {
		t.Fatalf("port %d still listening after gtl stop", port)
	}
	// Allocation must survive a plain stop.
	if findAllocation(t, gtl, repo, env) == nil {
		t.Fatalf("allocation vanished from registry after gtl stop (should persist)")
	}
	// Supervisor socket should still exist — stop keeps the supervisor alive.
	if _, statErr := os.Stat(sockPath); statErr != nil {
		t.Fatalf("supervisor socket gone after gtl stop (should stay alive for resume): %v", statErr)
	}
	t.Logf("STEP 5 ok: server stopped, allocation + supervisor retained")

	// =====================================================================
	// STEP 6: gtl release --force — allocation gone from the registry AND the
	// supervisor is torn down (Phase 8 teardown regression guard).
	// =====================================================================
	out, err = runGtl(t, gtl, repo, env, "release", "--force")
	if err != nil {
		t.Fatalf("gtl release --force failed: %v\n%s", err, out)
	}
	if findAllocation(t, gtl, repo, env) != nil {
		t.Fatalf("allocation still present in registry after release --force")
	}
	// The supervisor must have been shut down by release teardown: its socket
	// disappears when Run() returns and its deferred cleanup fires.
	if !waitFor(15*time.Second, func() bool {
		_, statErr := os.Stat(sockPath)
		return os.IsNotExist(statErr)
	}) {
		t.Fatalf("supervisor socket %s still present after release --force — teardown did not stop the supervisor", sockPath)
	}
	// And the backgrounded `gtl start` process should exit now that its
	// supervisor was told to shut down.
	select {
	case <-startExited:
	case <-time.After(15 * time.Second):
		t.Fatalf("backgrounded gtl start did not exit after release --force shut its supervisor down")
	}
	// Release hooks: pre_release ran (marker exists) and its deliberate
	// failure did not block the forced release; post_release ran after.
	if _, statErr := os.Stat(filepath.Join(repo, "pre_release.ran")); statErr != nil {
		t.Fatalf("pre_release hook did not run during release --force: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "post_release.ran")); statErr != nil {
		t.Fatalf("post_release hook did not run during release --force: %v", statErr)
	}
	t.Logf("STEP 6 ok: release --force removed allocation, stopped the supervisor, and ran release hooks despite pre_release failure")
}

// --- helpers ---

func gitInit(t *testing.T, repo string, env []string) {
	t.Helper()
	// Isolate git identity so the commit works on a bare CI runner.
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(env,
			"GIT_AUTHOR_NAME=gtl-e2e", "GIT_AUTHOR_EMAIL=e2e@example.com",
			"GIT_COMMITTER_NAME=gtl-e2e", "GIT_COMMITTER_EMAIL=e2e@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("commit", "--allow-empty", "-m", "init")
}

// findAllocation returns the registry allocation for repo as seen through
// `gtl list --json`, or nil if none exists.
func findAllocation(t *testing.T, gtl, repo string, env []string) map[string]any {
	t.Helper()
	out, err := runGtl(t, gtl, repo, env, "list", "--json")
	if err != nil {
		t.Fatalf("gtl list --json (findAllocation) failed: %v\n%s", err, out)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parsing list --json (findAllocation): %v\n%s", err, out)
	}
	return findByPath(entries, repo)
}

func allocPort(t *testing.T, alloc map[string]any) int {
	t.Helper()
	if alloc == nil {
		return 0
	}
	ports := anyIntSlice(alloc["ports"])
	if len(ports) > 0 {
		return ports[0]
	}
	return anyInt(alloc["port"])
}

func findByWorktree(entries []map[string]any, repo string) map[string]any {
	for _, e := range entries {
		if s, _ := e["worktree"].(string); s == repo {
			return e
		}
	}
	return nil
}

func findByPath(entries []map[string]any, repo string) map[string]any {
	for _, e := range entries {
		// list --json uses "path"; status --json uses "worktree".
		if s, _ := e["path"].(string); s == repo {
			return e
		}
		if s, _ := e["worktree"].(string); s == repo {
			return e
		}
	}
	return nil
}

func anyInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func anyIntSlice(v any) []int {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(raw))
	for _, item := range raw {
		out = append(out, anyInt(item))
	}
	return out
}

func containsInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func dumpLog(t *testing.T, path string) {
	t.Helper()
	if data, err := os.ReadFile(path); err == nil {
		t.Logf("--- gtl start log ---\n%s\n--- end log ---", data)
	}
}
