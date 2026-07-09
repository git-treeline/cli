package cmd

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/interpolation"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/resolve"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/templates"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

var startAwait bool
var startAwaitTimeout int
var startWith string
var stopKill bool

func init() {
	startCmd.Flags().BoolVar(&startAwait, "await", false, "Block until the server is accepting connections, then exit 0")
	startCmd.Flags().IntVar(&startAwaitTimeout, "await-timeout", 60, "Timeout in seconds for --await")
	startCmd.Flags().StringVar(&startWith, "with", "", "Comma-separated hooks to activate (defined in .treeline.yml hooks:)")
	rootCmd.AddCommand(startCmd)
	stopCmd.Flags().BoolVar(&stopKill, "kill", false, "Shut down the supervisor entirely instead of keeping it alive")
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the dev server with a supervised process",
	Long: `Run the commands.start from .treeline.yml under a lightweight supervisor.
The server runs in your terminal with full log output. Other processes
(AI agents, scripts) can restart or stop it via 'gtl restart' and 'gtl stop'
without interrupting your terminal session.

If the supervisor is already running but the server was stopped, this
resumes the server in the original terminal. Ctrl+C exits the supervisor.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)
		pc := config.LoadProjectConfig(absPath)

		startCommand := pc.StartCommand()
		if startCommand == "" {
			return cliErr(cmd, errNoStartCommand())
		}

		if err := ensureAllocation(cmd, absPath); err != nil {
			return err
		}

		warnPortWiring(startCommand, absPath)
		if service.IsRunning() {
			warnRouterVersionMismatch()
		}

		activeHooks, err := resolveStartHooks(pc, startWith)
		if err != nil {
			return cliErr(cmd, err)
		}

		sockPath := supervisor.SocketPath(absPath)
		port := resolvePort(absPath)

		startCommand = interpolateCommand(startCommand, port)

		// Resume path — supervisor already running, no hooks re-fired
		resp, err := supervisor.Send(sockPath, "status")
		if err == nil {
			if resp == "running" {
				// --await is for scripts: just wait for readiness, no prompt.
				if startAwait {
					return cliErr(cmd, awaitReady(sockPath))
				}
				// Non-interactive (agent, piped stdin) — keep the legacy error.
				if !stdinIsTTY() {
					return cliErr(cmd, errServerAlreadyRunning())
				}
				action := promptRunningElsewhere(nil)
				switch action {
				case runningActionCancel:
					return nil
				case runningActionRestart:
					if len(activeHooks) > 0 {
						fmt.Fprintln(os.Stderr, style.Warnf("--with ignored: supervisor already running. Hooks only run on fresh start."))
					}
					return cliErr(cmd, restartViaSupervisor(sockPath))
				case runningActionMove:
					if err := stopOtherSupervisor(sockPath, 15*time.Second); err != nil {
						return cliErr(cmd, err)
					}
					fmt.Println(style.Dimf("Stopped the server in the other terminal — starting fresh here."))
					// Fall through to the fresh-start path below.
				}
			} else {
				// Supervisor alive but server stopped — the original terminal is
				// likely gone (e.g. app restart). Kill the orphaned supervisor and
				// start fresh here so output appears in the current terminal.
				if err := stopOtherSupervisor(sockPath, 15*time.Second); err != nil {
					return cliErr(cmd, err)
				}
				fmt.Println(style.Dimf("Restarting in this terminal..."))
				// Fall through to fresh start below.
			}
		}

		// Fresh start — check for project name drift before proceeding
		if err := checkDriftOrAbort(absPath); err != nil {
			return cliErr(cmd, err)
		}

		if err := clearOrphanedPortProcess(port, absPath); err != nil {
			return cliErr(cmd, err)
		}

		if err := runPreStartHooks(activeHooks, port, absPath); err != nil {
			return cliErr(cmd, err)
		}
		if len(activeHooks) > 0 {
			writeHooksState(sockPath, activeHooks)
		}

		uc := config.LoadUserConfig("")
		if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: env sync skipped: %s\n", err)
		}
		branch := worktree.CurrentBranch(absPath)
		setup.ConfigureEditor(absPath, pc, uc, port, branch)
		printLocalAndRouter(uc, pc.Project(), branch, port)

		if startAwait {
			sv := supervisor.New(startCommand, absPath, sockPath)
			sv.Env = resolveEnvVars(pc, absPath)
			sv.Port = port
			svErr := make(chan error, 1)
			go func() { svErr <- sv.Run() }()

			for i := 0; i < 50; i++ {
				select {
				case err := <-svErr:
					return cliErr(cmd, &CliError{
						Message: fmt.Sprintf("Supervisor exited before ready: %s", err),
						Hint:    "Check commands.start in .treeline.yml — the process crashed on startup.",
					})
				default:
				}
				time.Sleep(100 * time.Millisecond)
				if _, err := os.Stat(sockPath); err == nil {
					break
				}
			}

			select {
			case err := <-svErr:
				return cliErr(cmd, &CliError{
					Message: fmt.Sprintf("Supervisor exited before ready: %s", err),
					Hint:    "Check commands.start in .treeline.yml — the process crashed on startup.",
				})
			default:
			}

			if err := awaitReady(sockPath); err != nil {
				return cliErr(cmd, err)
			}
			return nil
		}

		sv := supervisor.New(startCommand, absPath, sockPath)
		sv.Env = resolveEnvVars(pc, absPath)
		sv.Port = port
		svErr := sv.Run()

		// Post-stop hooks — run after supervisor exits (reverse order)
		runPostStopHooks(sockPath, pc, port, absPath)

		return svErr
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the dev server (supervisor stays alive for resume)",
	Long: `Stop the running dev server process. The supervisor remains alive so
the server can be resumed with 'gtl start'. Use --kill to shut down the
supervisor entirely.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		sockPath, err := resolveSocket()
		if err != nil {
			return err
		}

		command := "stop"
		if stopKill {
			command = "shutdown"
		}

		resp, err := supervisor.Send(sockPath, command)
		if err != nil {
			if stopKill {
				if killed, killErr := forceKillSupervisor(sockPath); killed {
					fmt.Println("Supervisor force-killed (was unresponsive).")
					return nil
				} else if killErr != nil {
					return cliErr(cmd, &CliError{
						Message: fmt.Sprintf("Could not force-kill supervisor: %s", killErr),
						Hint:    "Find the supervisor PID manually and kill it.",
					})
				}
			}
			return err
		}
		if strings.HasPrefix(resp, "error") {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Server error: %s", resp),
				Hint:    "The supervisor may be in an unexpected state. Check 'gtl start' output.",
			})
		}

		if stopKill {
			fmt.Println("Supervisor shut down.")
		} else {
			fmt.Println("Server stopped. Supervisor still running — 'gtl start' to resume.")
		}
		return nil
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the supervised dev server",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)
		sockPath := supervisor.SocketPath(absPath)

		pc := config.LoadProjectConfig(absPath)
		uc := config.LoadUserConfig("")

		if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: env sync skipped: %s\n", err)
		}

		envVars := resolveEnvVars(pc, absPath)
		if len(envVars) > 0 {
			var pairs []string
			for k, v := range envVars {
				pairs = append(pairs, k+"="+v)
			}
			payload := "update-env:" + strings.Join(pairs, "\x00")
			if _, err := supervisor.Send(sockPath, payload); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not update supervisor env: %s\n", err)
			}
		}

		resp, err := supervisor.Send(sockPath, "restart")
		if err != nil {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Could not reach supervisor: %s", err),
				Hint:    "Is 'gtl start' running? Start the server first, then use 'gtl restart'.",
			})
		}
		if strings.HasPrefix(resp, "error") {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Server error: %s", resp),
				Hint:    "The server may have crashed. Check logs and try 'gtl stop' then 'gtl start'.",
			})
		}
		fmt.Println("Server restarted.")

		newStartCmd := pc.StartCommand()
		if newStartCmd != "" {
			port := resolvePort(absPath)
			newStartCmd = interpolateCommand(newStartCmd, port)
			if running, err := supervisor.Send(sockPath, "get-command"); err == nil {
				warnStaleCommand(os.Stderr, running, newStartCmd)
			}
		}

		return nil
	},
}

// warnStaleCommand prints a hint if the supervisor's active command differs
// from the config's current commands.start value.
func warnStaleCommand(w io.Writer, running, configured string) {
	if running == configured {
		return
	}
	_, _ = fmt.Fprintln(w, style.Warnf("Note: commands.start has changed in .treeline.yml."))
	_, _ = fmt.Fprintln(w, style.Dimf("  The supervisor is still using the original command."))
	_, _ = fmt.Fprintln(w, style.Dimf("  To apply: Ctrl+C the supervisor, then run 'gtl start'."))
}

// resolveEnvVars looks up the worktree's allocation from the registry and
// interpolates the env template from the project config, including {resolve:...}
// cross-worktree tokens. Returns nil if there's no allocation or no env template.
// ensureAllocation checks whether the current directory has a registry entry.
// If not and stdin is a TTY, it warns and offers to run setup interactively.
// If not and stdin is not a TTY, it returns a clear error immediately.
func ensureAllocation(cmd *cobra.Command, absPath string) error {
	reg := registry.New("")
	if reg.Find(absPath) != nil {
		return nil
	}

	if !stdinIsTTY() {
		return cliErr(cmd, errNoAllocation(absPath))
	}

	fmt.Fprintln(os.Stderr, style.Warnf("No allocation found for this directory — 'gtl setup' has not been run."))
	if !confirm.Prompt("Run 'gtl setup' now?", false, nil) {
		return cliErr(cmd, errNoAllocation(absPath))
	}

	uc := config.LoadUserConfig("")
	s := setup.New(absPath, "", uc)
	if _, err := s.Run(); err != nil {
		return cliErr(cmd, errSetupFailed(err))
	}
	return nil
}

func resolveEnvVars(pc *config.ProjectConfig, absPath string) map[string]string {
	reg := registry.New("")
	alloc := reg.Find(absPath)
	if alloc == nil {
		return nil
	}
	uc := config.LoadUserConfig("")
	interpAlloc := interpolation.Allocation(alloc)
	branch := worktree.CurrentBranch(absPath)
	setup.InjectRouterTokens(interpAlloc, pc.Project(), branch, uc.RouterDomain(), uc.TunnelDomain(""))
	redisURL := interpolation.BuildRedisURL(uc.RedisURL(), interpAlloc)
	r := resolve.New(reg, absPath, branch)
	result, err := setup.BuildEnvVarsWithResolver(pc, interpAlloc, redisURL, r.Resolve)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", err)
		fmt.Fprintf(os.Stderr, "  {resolve:...} tokens will not be expanded in process env.\n")
		fmt.Fprintf(os.Stderr, "  Your app should read from the env file (written correctly by gtl setup).\n")
		return setup.BuildEnvVars(pc, interpAlloc, redisURL)
	}
	return result
}

func resolveSocket() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	absPath, _ := filepath.Abs(cwd)
	return supervisor.SocketPath(absPath), nil
}

func resolvePort(absPath string) int {
	reg := registry.New("")
	entry := reg.Find(absPath)
	if entry == nil {
		return 0
	}
	ports := format.GetPorts(format.Allocation(entry))
	if len(ports) == 0 {
		return 0
	}
	return ports[0]
}

// interpolateCommand expands {port} (and {port_N}) tokens in the start
// command string. This lets frameworks that ignore PORT env (Vite, Angular,
// Expo) receive the allocated port via CLI flags.
func interpolateCommand(cmd string, port int) string {
	if !strings.Contains(cmd, "{port") {
		return cmd
	}
	cmd = strings.ReplaceAll(cmd, "{port}", fmt.Sprintf("%d", port))

	inc := 1
	for i := 2; i <= 10; i++ {
		token := fmt.Sprintf("{port_%d}", i)
		if strings.Contains(cmd, token) {
			cmd = strings.ReplaceAll(cmd, token, fmt.Sprintf("%d", port+inc))
		}
		inc++
	}
	return cmd
}

// warnPortWiring checks whether the start command is missing {port} for a
// framework that ignores the PORT env var. This is the same check that
// doctor and setup run, surfaced at start time when the user will actually
// see the wrong-port behavior.
func warnPortWiring(startCommand, worktreePath string) {
	if strings.Contains(startCommand, "{port") {
		return
	}
	det := detect.Detect(worktreePath)
	if hint := templates.PortHint(det); hint != "" {
		fmt.Fprintln(os.Stderr, style.Warnf("Port wiring: your start command doesn't include {port}."))
		fmt.Fprintln(os.Stderr, style.Dimf("  %s", strings.Split(hint, "\n")[0]))
		fmt.Fprintln(os.Stderr, style.Dimf("  The server may start on the wrong port. See 'gtl doctor' for details."))
		fmt.Fprintln(os.Stderr)
	}
}

// --- Start hooks ---

// resolveStartHooks collects auto hooks (always run) plus any --with hooks.
// Auto hooks come first; --with hooks are appended in flag order.
// Duplicates are deduplicated (--with naming an auto hook is a no-op).
func resolveStartHooks(pc *config.ProjectConfig, withFlag string) ([]startHookEntry, error) {
	allHooks := pc.StartHooks()

	var result []startHookEntry
	seen := map[string]bool{}
	for name, h := range allHooks {
		if h.Auto {
			result = append(result, startHookEntry{Name: name, Hook: h})
			seen[name] = true
		}
	}

	if withFlag == "" {
		if len(result) == 0 {
			return nil, nil
		}
		return result, nil
	}

	if allHooks == nil {
		return nil, &CliError{
			Message: "No hooks defined in .treeline.yml.",
			Hint:    "Add a hooks: block with named pre_start/post_stop entries.",
		}
	}

	for _, name := range strings.Split(withFlag, ",") {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		h, ok := allHooks[name]
		if !ok {
			available := make([]string, 0, len(allHooks))
			for k := range allHooks {
				available = append(available, k)
			}
			return nil, &CliError{
				Message: fmt.Sprintf("Unknown hook %q.", name),
				Hint:    fmt.Sprintf("Available hooks: %s", strings.Join(available, ", ")),
			}
		}
		result = append(result, startHookEntry{Name: name, Hook: h})
		seen[name] = true
	}
	return result, nil
}

type startHookEntry struct {
	Name string
	Hook config.StartHook
}

// runPreStartHooks executes pre_start commands. Aborts on first failure.
func runPreStartHooks(hooks []startHookEntry, port int, dir string) error {
	for _, entry := range hooks {
		for _, cmdStr := range entry.Hook.PreStart {
			expanded := interpolateCommand(cmdStr, port)
			fmt.Printf("==> Hook [%s] pre_start: %s\n", entry.Name, expanded)
			cmd := exec.Command("sh", "-c", expanded)
			cmd.Dir = dir
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return &CliError{
					Message: fmt.Sprintf("Hook %q pre_start failed: %s", entry.Name, err),
					Hint:    "Fix the hook command or start without --with.",
				}
			}
		}
	}
	return nil
}

// runPostStopHooks reads the hooks state file, re-reads the project config,
// and runs post_stop commands in reverse order. Errors are logged, not fatal.
func runPostStopHooks(sockPath string, pc *config.ProjectConfig, port int, dir string) {
	names := readHooksState(sockPath)
	if len(names) == 0 {
		return
	}
	defer cleanHooksState(sockPath)

	allHooks := pc.StartHooks()
	if allHooks == nil {
		return
	}

	for i := len(names) - 1; i >= 0; i-- {
		h, ok := allHooks[names[i]]
		if !ok || len(h.PostStop) == 0 {
			continue
		}
		for _, cmdStr := range h.PostStop {
			expanded := interpolateCommand(cmdStr, port)
			fmt.Printf("==> Hook [%s] post_stop: %s\n", names[i], expanded)
			cmd := exec.Command("sh", "-c", expanded)
			cmd.Dir = dir
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: hook %q post_stop failed: %s\n", names[i], err)
			}
		}
	}
}

// hooksStatePath returns the path for persisting active hook names alongside
// the supervisor socket.
func hooksStatePath(sockPath string) string {
	return strings.TrimSuffix(sockPath, ".sock") + ".hooks"
}

func writeHooksState(sockPath string, hooks []startHookEntry) {
	names := make([]string, len(hooks))
	for i, h := range hooks {
		names[i] = h.Name
	}
	_ = os.WriteFile(hooksStatePath(sockPath), []byte(strings.Join(names, "\n")), 0o600)
}

func readHooksState(sockPath string) []string {
	data, err := os.ReadFile(hooksStatePath(sockPath))
	if err != nil {
		return nil
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

func cleanHooksState(sockPath string) {
	_ = os.Remove(hooksStatePath(sockPath))
}

// stdinIsTTY reports whether stdin is connected to a terminal device.
// Used to gate interactive prompts so scripts/agents keep getting structured
// errors instead of hanging on a read.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

type runningAction int

const (
	runningActionCancel runningAction = iota
	runningActionMove
	runningActionRestart
)

// promptRunningElsewhere asks the user what to do when 'gtl start' finds the
// server already running under a supervisor owned by another terminal.
// reader is the input source for confirm.Select (nil = os.Stdin).
func promptRunningElsewhere(reader io.Reader) runningAction {
	fmt.Println(style.Warnf("Server is already running, attached to another terminal."))
	idx := confirm.Select(
		"What would you like to do?",
		[]string{
			"Stop it and start fresh in this terminal",
			"Restart in place (logs stay in the other terminal)",
			"Cancel",
		},
		0,
		reader,
	)
	switch idx {
	case 0:
		return runningActionMove
	case 1:
		return runningActionRestart
	default:
		return runningActionCancel
	}
}

// forceKillSupervisor reads the supervisor's PID file and SIGKILLs the
// process, then cleans up the socket and PID files. Used as a fallback when
// the supervisor is alive enough to hold the socket but too hung to respond.
// Returns (true, nil) on success, (false, nil) if no PID file exists,
// (false, err) if the kill fails.
func forceKillSupervisor(sockPath string) (bool, error) {
	pidPath := supervisor.PidPath(sockPath)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false, nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return false, err
	}
	// Killing only the supervisor orphans the child process group, which keeps
	// holding the port with every handle erased. Reap the whole group via the
	// pgid the supervisor persisted alongside its PID file.
	reapChildGroup(sockPath)
	_ = os.Remove(sockPath)
	_ = os.Remove(pidPath)
	return true, nil
}

// reapChildGroup reads the child pgid sidecar written by the supervisor and
// SIGKILLs the entire process group so an orphaned dev server can't keep
// holding the port. It is deliberately defensive: a missing or garbage file,
// or a pgid that could target the session/init, is a no-op.
func reapChildGroup(sockPath string) {
	childPidPath := supervisor.ChildPidPath(sockPath)
	data, err := os.ReadFile(childPidPath)
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(childPidPath) }()
	pgid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pgid <= 1 {
		return
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return
	}
}

// stopOtherSupervisor shuts the existing supervisor down and waits for its
// socket to disappear, so the caller can start a fresh supervisor without
// racing the old one's cleanup. timeout caps the wait for socket cleanup.
func stopOtherSupervisor(sockPath string, timeout time.Duration) error {
	if _, err := supervisor.Send(sockPath, "shutdown"); err != nil {
		return &CliError{
			Message: fmt.Sprintf("Could not stop the running server: %s", err),
			Hint:    "Try 'gtl stop --kill' manually, then re-run 'gtl start'.",
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return &CliError{
		Message: fmt.Sprintf("The other supervisor didn't shut down within %s.", timeout),
		Hint:    "Run 'gtl stop --kill' from this directory, then re-run 'gtl start'.",
	}
}

// restartViaSupervisor sends a restart over the socket. Used when the user
// picks "restart in place" from the running-elsewhere prompt.
func restartViaSupervisor(sockPath string) error {
	resp, err := supervisor.Send(sockPath, "restart")
	if err != nil {
		return &CliError{
			Message: fmt.Sprintf("Could not reach supervisor: %s", err),
			Hint:    "The other terminal may have exited. Run 'gtl start' again.",
		}
	}
	if strings.HasPrefix(resp, "error") {
		return &CliError{
			Message: fmt.Sprintf("Server error: %s", resp),
			Hint:    "The server may have crashed. Try 'gtl stop --kill' then 'gtl start'.",
		}
	}
	fmt.Println("Server restarted in the other terminal.")
	return nil
}

func awaitReady(sockPath string) error {
	cmd := fmt.Sprintf("wait-ready:%d", startAwaitTimeout)
	resp, err := supervisor.SendWithTimeout(sockPath, cmd, time.Duration(startAwaitTimeout+5)*time.Second)
	if err != nil {
		return &CliError{
			Message: fmt.Sprintf("Timed out waiting for server: %s", err),
			Hint:    fmt.Sprintf("Server didn't respond within %ds. It may still be starting — check logs.", startAwaitTimeout),
		}
	}
	if resp == "ok" {
		fmt.Println("Server is ready.")
		return nil
	}
	return &CliError{
		Message: fmt.Sprintf("Server not ready: %s", resp),
		Hint:    "The server started but isn't accepting connections. Check commands.start output.",
	}
}

// clearOrphanedPortProcess checks whether the worktree's allocated port is
// already occupied by a previous server that escaped cleanup. When a PID file
// in tmp/pids/server.pid identifies the owner and that process is alive, the
// user is prompted to kill it before the fresh start proceeds.
func clearOrphanedPortProcess(port int, worktreeDir string) error {
	if port == 0 {
		return nil
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return nil // port is free
	}
	_ = conn.Close()

	// Port is occupied. Check for a server PID file written by a previous run
	// of this worktree's server (Rails, Django, etc. write these on startup).
	pidFile := filepath.Join(worktreeDir, "tmp", "pids", "server.pid")
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return clearUnknownPortProcess(port)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		// Unreadable PID file — we can't confirm who owns the port, so don't
		// try to kill an unknown process. Let startup proceed; if the port is
		// truly occupied the server will fail to bind with a clear message.
		return nil
	}

	// Confirm the PID is still alive before offering to kill it.
	if err := syscall.Kill(pid, 0); err != nil {
		// Stale PID file pointing to a dead process; remove it so the next
		// server can start cleanly.
		_ = os.Remove(pidFile)
		return nil
	}

	// Live process identified from this worktree's PID file — safe to offer a kill.
	if !stdinIsTTY() {
		return &CliError{
			Message: fmt.Sprintf("Port %d is occupied by a previous server (pid: %d).", port, pid),
			Hint:    fmt.Sprintf("Kill it with: kill %d", pid),
		}
	}

	fmt.Fprintln(os.Stderr, style.Warnf("A previous server is still running on port %d (pid: %d).", port, pid))
	if !confirm.Prompt(fmt.Sprintf("Kill pid %d and continue?", pid), true, nil) {
		return &CliError{
			Message: "Aborted.",
			Hint:    fmt.Sprintf("Kill manually with: kill %d", pid),
		}
	}

	// SIGTERM first, escalate to SIGKILL if the port doesn't free in 2s.
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		c, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr != nil {
			_ = os.Remove(pidFile)
			return nil
		}
		_ = c.Close()
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	_ = os.Remove(pidFile)
	return nil
}

// clearUnknownPortProcess is called when the port is occupied but there is no
// server.pid to identify the owner. It uses lsof to discover the blocking
// process(es), shows what they are, and offers to kill them.
func clearUnknownPortProcess(port int) error {
	pids := lsofPortPIDs(port)
	if len(pids) == 0 {
		fmt.Fprintln(os.Stderr, style.Warnf("Port %d is in use by an unknown process. Stop it manually, then run 'gtl start'.", port))
		return &CliError{
			Message: fmt.Sprintf("Port %d is already in use.", port),
			Hint:    "Another process is listening on this port. Stop it and try again.",
		}
	}

	// Build a human-readable description of the blocking processes.
	var descs []string
	for _, pid := range pids {
		name := processCommandName(pid)
		if name != "" {
			descs = append(descs, fmt.Sprintf("%s (pid: %d)", name, pid))
		} else {
			descs = append(descs, fmt.Sprintf("pid: %d", pid))
		}
	}
	desc := strings.Join(descs, ", ")

	if !stdinIsTTY() {
		return &CliError{
			Message: fmt.Sprintf("Port %d is occupied by: %s.", port, desc),
			Hint:    fmt.Sprintf("Kill it with: kill -9 %s", joinPIDs(pids)),
		}
	}

	fmt.Fprintln(os.Stderr, style.Warnf("Port %d is in use by: %s.", port, desc))
	if !confirm.Prompt(fmt.Sprintf("Kill %s and continue?", joinPIDs(pids)), true, nil) {
		return &CliError{
			Message: "Aborted.",
			Hint:    fmt.Sprintf("Kill manually with: kill -9 %s", joinPIDs(pids)),
		}
	}

	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		c, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr != nil {
			return nil
		}
		_ = c.Close()
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	time.Sleep(200 * time.Millisecond)
	return nil
}

// lsofPortPIDs returns the PIDs of processes listening on the given TCP port
// using lsof. Returns nil if lsof is unavailable or finds nothing.
func lsofPortPIDs(port int) []int {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}
	var pids []int
	seen := map[int]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids
}

// processCommandName returns the short command name for a PID via ps.
func processCommandName(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	// ps returns the full path; trim to basename for readability.
	name := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

func joinPIDs(pids []int) string {
	parts := make([]string, len(pids))
	for i, pid := range pids {
		parts[i] = strconv.Itoa(pid)
	}
	return strings.Join(parts, " ")
}
