package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCurrentBranchContext(t *testing.T) {
	repo := initTestRepo(t)

	tests := []struct {
		name    string
		prepare func()
		dir     string
		want    string
	}{
		{name: "on a branch", prepare: func() {}, dir: repo, want: "main"},
		{name: "detached HEAD reads as empty", prepare: func() { run(t, repo, "git", "checkout", "-q", "--detach") }, dir: repo, want: ""},
		{name: "missing directory reads as empty", prepare: func() {}, dir: filepath.Join(repo, "nope"), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.prepare()
			if got := CurrentBranchContext(context.Background(), tt.dir); got != tt.want {
				t.Fatalf("CurrentBranchContext = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestGitOutputContextKillsStalledGit is the regression for the process storm
// seen by pollers of `gtl status`: a git that never answers must die when the
// probe is cancelled (deadline or signal), not be left running for the next
// poll to stack another one on. The test waits for the fake git to report its
// pid before cancelling, so it holds under -race and CI load rather than
// racing a fixed timeout.
func TestGitOutputContextKillsStalledGit(t *testing.T) {
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "git.pid")
	// exec replaces the shell so the pid we record is the process that hangs;
	// the rename makes the pid file appear atomically.
	script := "#!/bin/sh\necho $$ > " + pidFile + ".tmp && mv " + pidFile + ".tmp " + pidFile + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan string, 1)
	go func() { done <- CurrentBranchContext(ctx, t.TempDir()) }()

	var raw []byte
	deadline := time.Now().Add(5 * time.Second)
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
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("bad pid %q: %v", raw, err)
	}

	cancel()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("cancelled git produced branch %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CurrentBranchContext did not return after cancel")
	}
	// Once Wait has reaped the child, signal 0 reports ESRCH.
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("stalled git (pid %d) survived cancellation (kill -0: %v)", pid, err)
	}
}
