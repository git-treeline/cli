package tui

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestCloseRejectsQueuedCommands(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	cmd := m.restartSupervisor()
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for a command that never started")
	}
	result := cmd().(operationResultMsg)
	if result.operation.err != context.Canceled || len(calls) != 0 {
		t.Fatalf("queued command ran after close: result=%+v calls=%v", result, calls)
	}
}

func TestCloseCancelsAndAwaitsRunningCommand(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	started := make(chan struct{})
	canceled := make(chan struct{})
	cleanup := make(chan struct{})
	m.deps.runCommand = func(ctx context.Context, _ string, _ ...string) (string, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-cleanup
		return "cleaned up", ctx.Err()
	}
	cmd := m.restartSupervisor()
	go cmd()
	<-started
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the command")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before command cleanup")
	default:
	}
	close(cleanup)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after command cleanup")
	}
}

type lifecycleProgramModel struct {
	Model
	command tea.Cmd
}

func (m lifecycleProgramModel) Init() tea.Cmd { return m.command }

func TestProgramExitClosesRunningCommand(t *testing.T) {
	for _, mode := range []string{"quit", "context error"} {
		t.Run(mode, func(t *testing.T) {
			var calls []callRecord
			m := buildTestModelWithDeps(&calls)
			started := make(chan struct{})
			cleaned := make(chan struct{})
			m.deps.runCommand = func(ctx context.Context, _ string, _ ...string) (string, error) {
				close(started)
				<-ctx.Done()
				close(cleaned)
				return "", ctx.Err()
			}
			command := m.restartSupervisor()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			program := tea.NewProgram(lifecycleProgramModel{Model: m, command: command}, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
			finished := make(chan error, 1)
			go func() {
				_, err := program.Run()
				m.Close()
				finished <- err
			}()
			select {
			case <-started:
			case err := <-finished:
				t.Fatalf("program exited before command started: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("program did not start command")
			}
			if mode == "quit" {
				program.Send(tea.Quit())
			} else {
				cancel()
			}
			select {
			case err := <-finished:
				if mode == "quit" && err != nil {
					t.Fatalf("normal quit returned error: %v", err)
				}
				if mode == "context error" && err == nil {
					t.Fatal("expected program cancellation error")
				}
				select {
				case <-cleaned:
				default:
					t.Fatal("program exit did not await command cleanup")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("program did not finish cleanup")
			}
		})
	}
}

func TestCloseLeavesCompletedStartRunning(t *testing.T) {
	var calls []callRecord
	m := buildTestModelWithDeps(&calls)
	m.deps.runCommand = func(ctx context.Context, path string, _ ...string) (string, error) {
		return runCLIExecutable(ctx, "/bin/sh", path, "-c", "sleep 30 >/dev/null 2>&1 & echo $!")
	}
	m.flatList[m.cursor].wt.WorktreePath = t.TempDir()
	m.flatList[m.cursor].wt.Supervisor = "stopped"
	command := m.toggleSupervisor()
	result := command().(operationResultMsg)
	if result.operation.err != nil {
		t.Fatal(result.operation.err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(result.operation.output))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	m.Close()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("Close killed a process from a completed command: %v", err)
	}
	// Confirm this is a live sleeper rather than a zombie awaiting reaping.
	status, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || strings.HasPrefix(strings.TrimSpace(string(status)), "Z") {
		t.Fatalf("completed process is not running: %q %v", status, err)
	}
}
