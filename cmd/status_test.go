package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/process"
)

func TestProbeAll(t *testing.T) {
	tests := []struct {
		name    string
		delays  []time.Duration // per-input probe latency
		budget  time.Duration
		applied []int // input indexes whose result must be applied
		dropped []int // input indexes whose result must never be applied
	}{
		{
			name:    "all fast probes are applied",
			delays:  []time.Duration{0, 0, 0},
			budget:  time.Second,
			applied: []int{0, 1, 2},
		},
		{
			name:    "one stalled probe does not block the others",
			delays:  []time.Duration{0, time.Hour, 0},
			budget:  200 * time.Millisecond,
			applied: []int{0, 2},
			dropped: []int{1},
		},
		{
			name:   "no inputs returns immediately",
			delays: nil,
			budget: time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.budget)
			defer cancel()

			got := make(map[int]bool)
			start := time.Now()
			probeAll(ctx, tt.delays,
				func(ctx context.Context, d time.Duration) bool {
					select {
					case <-time.After(d):
						return true
					case <-ctx.Done():
						return false
					}
				},
				func(i int, ok bool) { got[i] = ok })
			elapsed := time.Since(start)

			if elapsed > tt.budget+500*time.Millisecond {
				t.Fatalf("probeAll took %v, budget was %v", elapsed, tt.budget)
			}
			for _, i := range tt.applied {
				if _, ok := got[i]; !ok {
					t.Errorf("input %d: result not applied", i)
				}
			}
			for _, i := range tt.dropped {
				if _, ok := got[i]; ok {
					t.Errorf("input %d: stalled result applied after deadline", i)
				}
			}
		})
	}
}

// TestStatusHelperProcess is not a real test: TestStatusSIGTERMReapsGit
// re-executes the test binary with this as the entry point, so a real
// `gtl status --json` — cobra wiring and signal handling included — can be
// killed mid-probe.
func TestStatusHelperProcess(t *testing.T) {
	if os.Getenv("GTL_STATUS_HELPER") != "1" {
		t.Skip("helper entry point for TestStatusSIGTERMReapsGit")
	}
	rootCmd.SetArgs([]string{"status", "--json"})
	_ = rootCmd.Execute()
}

// TestStatusSIGTERMReapsGit is the end-to-end regression for the process
// storm: a poller that gives up on gtl status sends SIGTERM, and the hung git
// child must die with the command instead of surviving into the next poll.
func TestStatusSIGTERMReapsGit(t *testing.T) {
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "git.pid")
	// exec replaces the shell so the pid we record is the process that hangs;
	// the rename makes the pid file appear atomically.
	script := "#!/bin/sh\necho $$ > " + pidFile + ".tmp && mv " + pidFile + ".tmp " + pidFile + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	stalled := t.TempDir()
	registryJSON := fmt.Sprintf(`{"allocations":[{"project":"demo","worktree":%q,"branch":"old","port":4000}]}`, stalled)
	if err := os.WriteFile(filepath.Join(home, "registry.json"), []byte(registryJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestStatusHelperProcess$")
	cmd.Env = append(os.Environ(),
		"GTL_STATUS_HELPER=1",
		"GTL_HOME="+home,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	var raw []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		var err error
		if raw, err = os.ReadFile(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake git never ran: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	gitPid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("bad pid %q: %v", raw, err)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("gtl status did not exit after SIGTERM")
	}

	deadline = time.Now().Add(2 * time.Second)
	for process.Alive(gitPid) {
		if time.Now().After(deadline) {
			t.Fatalf("hung git (pid %d) survived SIGTERM to gtl status", gitPid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
