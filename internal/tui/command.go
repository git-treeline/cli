package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const commandOutputLimit = 64 * 1024

func runCLI(ctx context.Context, worktreePath string, args ...string) (string, error) {
	return runCLIExecutable(ctx, gtlBinaryPath(), worktreePath, args...)
}

func runCLIExecutable(ctx context.Context, executable, worktreePath string, args ...string) (string, error) {
	if strings.TrimSpace(worktreePath) == "" {
		return "", fmt.Errorf("worktree path is required")
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = worktreePath
	// Release addresses its allocation by absolute path, even after the directory is gone.
	if len(args) == 3 && args[0] == "release" && args[1] == "--force" && args[2] == worktreePath && filepath.IsAbs(args[2]) {
		for {
			_, err := os.Stat(cmd.Dir)
			if err == nil {
				break
			}
			parent := filepath.Dir(cmd.Dir)
			if !errors.Is(err, os.ErrNotExist) || parent == cmd.Dir {
				return "", err
			}
			cmd.Dir = parent
		}
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "GTL_NO_UPDATE_NOTIFY=1", "GTL_NO_STALE_WARN=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Give start --await time to clean up its detached supervisor on cancellation.
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != syscall.ESRCH {
			return err
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = 10 * time.Second
	var output commandOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if ctx.Err() != nil {
		if cmd.Process != nil {
			// Stop remaining setup children even if the CLI already exited cleanly.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		err = errors.Join(err, ctx.Err())
	}
	clean := ansi.Strip(strings.ToValidUTF8(string(output.tail), ""))
	clean = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, clean)
	if output.truncated {
		clean = "[Earlier output omitted]\n" + clean
	}
	return clean, err
}

type commandOutput struct {
	tail      []byte
	truncated bool
}

func (w *commandOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n >= commandOutputLimit {
		w.truncated = w.truncated || len(w.tail)+n > commandOutputLimit
		w.tail = append(w.tail[:0], p[n-commandOutputLimit:]...)
		return n, nil
	}
	if overflow := len(w.tail) + n - commandOutputLimit; overflow > 0 {
		w.truncated = true
		copy(w.tail, w.tail[overflow:])
		w.tail = w.tail[:len(w.tail)-overflow]
	}
	w.tail = append(w.tail, p...)
	return n, nil
}
