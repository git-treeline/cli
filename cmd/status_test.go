package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/git-treeline/cli/internal/registry"
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
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  \"rev-parse --abbrev-ref HEAD\") echo old; exit ;;\n" +
		"  \"remote get-url origin\") echo https://github.com/acme/web.git; exit ;;\n" +
		"esac\n" +
		"echo $$ > " + pidFile + ".tmp && mv " + pidFile + ".tmp " + pidFile + "\nexec sleep 60\n"
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

func TestStatusJSONRelatedSnapshot(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(fmt.Sprintf("stalled_root=%v", stalled), func(t *testing.T) {
			home, wt, bin := t.TempDir(), t.TempDir(), t.TempDir()
			registryJSON := fmt.Sprintf(`{"version":2,"allocations":[{"worktree":%q,"branch":"main"}],"edges":[{"a":{"repo":"acme/web","branch":"main"},"b":{"repo":"acme/api","branch":"feature"},"createdAt":"2026-10-10T12:00:00Z"}]}`, wt)
			if err := os.WriteFile(filepath.Join(home, "registry.json"), []byte(registryJSON), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wt, ".treeline.yml"), []byte("related_repos:\n  - acme/mobile\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			rootAction := "pwd"
			if stalled {
				rootAction = "echo $$ > \"$GTL_HOME/git.pid\"; exec sleep 60"
			}
			// Replacing the registry during the probe proves relationship output
			// comes from the same snapshot as the allocation being rendered.
			script := `#!/bin/sh
case "$*" in
  "rev-parse --abbrev-ref HEAD") echo main ;;
  "remote get-url origin")
    echo '{"version":2,"allocations":[],"edges":[]}' > "$GTL_HOME/registry.json"
    echo https://github.com/acme/web.git ;;
  "rev-parse --show-toplevel") ` + rootAction + ` ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			pidPath := filepath.Join(home, "git.pid")
			t.Cleanup(func() {
				if raw, err := os.ReadFile(pidPath); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStatusHelperProcess$")
			cmd.WaitDelay = time.Second
			cmd.Env = append(os.Environ(), "GTL_STATUS_HELPER=1", "GTL_HOME="+home,
				"GTL_NO_UPDATE_NOTIFY=1", "GTL_NO_STALE_WARN=1",
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("status failed (deadline: %v): %v\n%s", ctx.Err(), err, out)
			}
			var entries []struct {
				Repo    string         `json:"repo"`
				Related []relatedEntry `json:"related"`
			}
			if err := json.NewDecoder(bytes.NewReader(out)).Decode(&entries); err != nil {
				t.Fatalf("decode status: %v\n%s", err, out)
			}
			if len(entries) != 1 || entries[0].Repo != "acme/web" || len(entries[0].Related) != 2 {
				t.Fatalf("status lost snapshot edges or fallback config: %+v", entries)
			}
			if entries[0].Related[0].Source != "edge" || entries[0].Related[0].Repo != "acme/api" || entries[0].Related[1].Source != "config" || entries[0].Related[1].Repo != "acme/mobile" {
				t.Fatalf("unexpected relationships: %+v", entries[0].Related)
			}
			if stalled {
				raw, err := os.ReadFile(pidPath)
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
				if err != nil {
					t.Fatal(err)
				}
				if process.Alive(pid) {
					t.Fatalf("root probe (pid %d) survived its deadline", pid)
				}
			}
		})
	}
}

func TestRedisEndpointDisplayRedactsUserinfo(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"password", "redis://alice:s3cret@cache.example.com:6380/2", "redis://cache.example.com:6380/2"},
		{"username only", "rediss://alice@cache.example.com:6380", "rediss://cache.example.com:6380"},
		{"sensitive query and fragment", "redis://cache.example.com:6380/2?token=s3cret#alice", "redis://cache.example.com:6380/2"},
		{"no userinfo", "redis://localhost:6379", "redis://localhost:6379"},
		{"malformed escape", "redis://alice:s3cret%zz@cache.example.com", "<invalid Redis URL>"},
		{"missing authority", "alice:s3cret@cache.example.com", "<invalid Redis URL>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redisEndpointDisplay(tt.raw)
			if got != tt.want {
				t.Fatalf("redisEndpointDisplay() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "alice") || strings.Contains(got, "s3cret") {
				t.Fatalf("userinfo leaked in %q", got)
			}
		})
	}
}

func TestRenderRedisCapacityDoesNotPrintCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GTL_HOME", home)
	const secret = "credential-do-not-print"
	configJSON := `{"redis":{"strategy":"database","databases":16,"url":"redis://alice:` + secret + `@cache.example.com:6380/2?token=` + secret + `#` + secret + `"}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	registryJSON := `{"version":2,"allocations":[{"project":"club","worktree_name":"feature","redis_db":1}]}`
	reg := registry.New(filepath.Join(home, "registry.json"))
	if err := os.WriteFile(reg.Path, []byte(registryJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { renderRedisCapacity(reg) })
	if strings.Contains(out, secret) || strings.Contains(out, "alice") {
		t.Fatalf("Redis credential leaked in status output: %q", out)
	}
	if !strings.Contains(out, "redis://cache.example.com:6380/2") {
		t.Fatalf("redacted endpoint missing from status output: %q", out)
	}
}
