package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/spf13/cobra"
)

var nukeForce bool

func init() {
	nukeCmd.Flags().BoolVarP(&nukeForce, "force", "f", false, "Skip confirmation prompt")
	rootCmd.AddCommand(nukeCmd)
}

var nukeCmd = &cobra.Command{
	Use:   "nuke",
	Short: "Machine-wide recovery: kill processes holding gtl ports and clear stale supervisors",
	Long: `Recover from a wedged state where servers, supervisors, or stray processes
are still holding ports that gtl allocated but the normal stop/release path
can no longer reach.

nuke inspects every port allocated across the registry, identifies whatever is
currently holding each one, and — after showing you the full list and asking
for confirmation — terminates them and clears any leftover supervisor sockets.

This is a blunt recovery tool. It does NOT touch the registry, databases, or
git worktrees; it only reclaims runtime processes and sockets.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg := registry.New("")
		killed, cleared, ran := runNuke(reg, nukeForce, nil, portHolder, killProcess)
		if !ran {
			return nil
		}
		fmt.Printf("\nDone. Killed %d process(es), cleared %d supervisor socket(s).\n", killed, cleared)
		return nil
	},
}

// runNuke is the testable core of `gtl nuke`: it builds the plan, previews it,
// gates on confirmation, and (only when confirmed) executes. The process
// discovery (holder) and kill seams are injected so tests can drive the
// selection and gating logic without touching real processes. ran reports
// whether the destructive phase actually ran (false when there was nothing to
// do or the user declined), so the caller knows whether to print the summary.
func runNuke(reg *registry.Registry, force bool, reader io.Reader, holder func(int) (int, string), kill func(int) bool) (killed, cleared int, ran bool) {
	plan := buildNukePlanWith(reg, holder)

	if plan.empty() {
		fmt.Println("Nothing to nuke — no gtl processes or stale sockets found.")
		return 0, 0, false
	}

	// Preview everything that will be killed BEFORE doing anything.
	printNukePlan(plan)

	if !confirm.Prompt("Kill these processes and clear these sockets?", force, reader) {
		fmt.Println("Aborted.")
		return 0, 0, false
	}

	killed, cleared = executeNukePlanWith(plan, holder, kill)
	return killed, cleared, true
}

// portTarget is one port and the process (if any) holding it.
type portTarget struct {
	port int
	pid  int
	name string
}

// nukePlan is the pre-computed, previewable set of destructive actions.
type nukePlan struct {
	ports   []portTarget // ports with a live holder
	sockets []string     // supervisor socket paths that exist and should be cleared
}

func (p nukePlan) empty() bool { return len(p.ports) == 0 && len(p.sockets) == 0 }

// buildNukePlan gathers every gtl-allocated port, the process holding each, and
// every existing supervisor socket across the registry.
func buildNukePlan(reg *registry.Registry) nukePlan {
	return buildNukePlanWith(reg, portHolder)
}

// buildNukePlanWith is the testable core of buildNukePlan with the
// process-discovery seam injected.
func buildNukePlanWith(reg *registry.Registry, holder func(int) (int, string)) nukePlan {
	var plan nukePlan

	seenPort := make(map[int]bool)
	for _, port := range reg.UsedPorts() {
		if port <= 0 || seenPort[port] {
			continue
		}
		seenPort[port] = true
		pid, name := holder(port)
		if pid <= 0 || pid == os.Getpid() {
			continue // nothing holding it, or it's us — leave alone
		}
		plan.ports = append(plan.ports, portTarget{port: port, pid: pid, name: name})
	}
	sort.Slice(plan.ports, func(i, j int) bool { return plan.ports[i].port < plan.ports[j].port })

	seenSock := make(map[string]bool)
	for _, a := range reg.Allocations() {
		wt := format.GetStr(format.Allocation(a), "worktree")
		if wt == "" {
			continue
		}
		sock := supervisor.SocketPath(wt)
		if seenSock[sock] {
			continue
		}
		if _, err := os.Stat(sock); err == nil {
			seenSock[sock] = true
			plan.sockets = append(plan.sockets, sock)
		}
	}
	sort.Strings(plan.sockets)

	return plan
}

func printNukePlan(plan nukePlan) {
	fmt.Println(style.Warnf("gtl nuke will terminate the following:"))
	if len(plan.ports) > 0 {
		fmt.Println("\n  Processes holding allocated ports:")
		for _, t := range plan.ports {
			name := t.name
			if name == "" {
				name = "unknown"
			}
			fmt.Printf("    port %-6d pid %-7d %s\n", t.port, t.pid, name)
		}
	}
	if len(plan.sockets) > 0 {
		fmt.Println("\n  Supervisor sockets to clear:")
		for _, s := range plan.sockets {
			fmt.Printf("    %s\n", s)
		}
	}
	fmt.Println()
}

// executeNukePlanWith carries out the plan with the process-discovery and kill
// seams injected (for testing). Supervisors are shut down first (a graceful
// shutdown frees their port), then any process still holding a port is killed,
// then leftover sockets are cleared. Returns counts of processes killed and
// sockets cleared.
func executeNukePlanWith(plan nukePlan, holder func(int) (int, string), kill func(int) bool) (killed, cleared int) {
	// Phase 1: ask supervisors to shut down cleanly; this releases most ports.
	for _, sock := range plan.sockets {
		_, _ = supervisor.Send(sock, "shutdown")
	}

	// Phase 2: kill whatever still holds each port.
	for _, t := range plan.ports {
		// Re-check: a supervisor shutdown above may have already freed it.
		pid, _ := holder(t.port)
		if pid <= 0 {
			continue
		}
		if pid == os.Getpid() {
			continue
		}
		if kill(pid) {
			killed++
			fmt.Printf("  Killed pid %d (port %d)\n", pid, t.port)
		} else {
			fmt.Fprintln(os.Stderr, style.Warnf("could not kill pid %d on port %d", pid, t.port))
		}
	}

	// Phase 3: clear any leftover supervisor sockets / pid files.
	for _, sock := range plan.sockets {
		// forceKillSupervisor SIGKILLs a hung supervisor and removes its socket
		// + pid file; the explicit removes below cover the not-running case.
		_, _ = forceKillSupervisor(sock)
		_ = os.Remove(sock)
		_ = os.Remove(supervisor.PidPath(sock))
		cleared++
	}

	return killed, cleared
}

// killProcess sends SIGTERM, waits briefly, then escalates to SIGKILL if the
// process is still alive. Returns true once the process is gone (or was never
// there). Kills the whole process group when possible so child servers spawned
// via `sh -c` die with their parent.
func killProcess(pid int) bool {
	// Prefer the process group (negative pid) so children die too; fall back to
	// the single pid if the group signal isn't accepted.
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	time.Sleep(100 * time.Millisecond)
	return syscall.Kill(pid, 0) == syscall.ESRCH
}

// portHolder returns the pid and command name of the process listening on the
// given TCP port, or (0, "") if none can be determined. Uses lsof, available on
// macOS and Linux.
func portHolder(port int) (int, string) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return 0, ""
	}
	out, err := exec.Command("lsof", "-i", fmt.Sprintf("TCP:%d", port),
		"-sTCP:LISTEN", "-n", "-P", "-F", "cn").Output()
	if err != nil {
		return 0, ""
	}
	var pid int
	var name string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "p") {
			if _, err := fmt.Sscanf(line[1:], "%d", &pid); err != nil {
				pid = 0
			}
		}
		if strings.HasPrefix(line, "c") {
			name = line[1:]
		}
	}
	return pid, name
}
