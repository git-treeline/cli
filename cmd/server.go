package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/interpolation"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/resolve"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/supervisor"
	"github.com/git-treeline/git-treeline/internal/templates"
	"github.com/git-treeline/git-treeline/internal/worktree"
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
			return errNoStartCommand()
		}

		warnPortWiring(startCommand, absPath)
		if service.IsRunning() {
			warnRouterVersionMismatch()
		}

		activeHooks, err := resolveStartHooks(pc, startWith)
		if err != nil {
			return err
		}

		sockPath := supervisor.SocketPath(absPath)
		port := resolvePort(absPath)

		startCommand = interpolateCommand(startCommand, port)

		// Resume path — supervisor already running, no hooks re-fired
		resp, err := supervisor.Send(sockPath, "status")
		if err == nil {
			if len(activeHooks) > 0 {
				fmt.Fprintln(os.Stderr, style.Warnf("--with ignored: supervisor already running. Hooks only run on fresh start."))
			}
			if resp == "running" {
				if startAwait {
					return awaitReady(sockPath)
				}
				return errServerAlreadyRunning()
			}
			resp, err = supervisor.Send(sockPath, "start")
			if err != nil {
				return err
			}
			if strings.HasPrefix(resp, "error") {
				return &CliError{
					Message: fmt.Sprintf("Server error: %s", resp),
					Hint:    "Check the server logs, or run 'gtl stop' and try again.",
				}
			}
			fmt.Println("Server resumed.")
			if startAwait {
				return awaitReady(sockPath)
			}
			return nil
		}

		// Fresh start path — run pre_start hooks
		if err := runPreStartHooks(activeHooks, port, absPath); err != nil {
			return err
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
		printRouterAndTunnel(uc, pc.Project(), branch)

		if startAwait {
			sv := supervisor.New(startCommand, absPath, sockPath)
			sv.Env = resolveEnvVars(pc, absPath)
			sv.Port = port
			svErr := make(chan error, 1)
			go func() { svErr <- sv.Run() }()

			for i := 0; i < 50; i++ {
				select {
				case err := <-svErr:
					return &CliError{
						Message: fmt.Sprintf("Supervisor exited before ready: %s", err),
						Hint:    "Check commands.start in .treeline.yml — the process crashed on startup.",
					}
				default:
				}
				time.Sleep(100 * time.Millisecond)
				if _, err := os.Stat(sockPath); err == nil {
					break
				}
			}

			select {
			case err := <-svErr:
				return &CliError{
					Message: fmt.Sprintf("Supervisor exited before ready: %s", err),
					Hint:    "Check commands.start in .treeline.yml — the process crashed on startup.",
				}
			default:
			}

			if err := awaitReady(sockPath); err != nil {
				return err
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
			return err
		}
		if strings.HasPrefix(resp, "error") {
			return &CliError{
				Message: fmt.Sprintf("Server error: %s", resp),
				Hint:    "The supervisor may be in an unexpected state. Check 'gtl start' output.",
			}
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
			return &CliError{
				Message: fmt.Sprintf("Could not reach supervisor: %s", err),
				Hint:    "Is 'gtl start' running? Start the server first, then use 'gtl restart'.",
			}
		}
		if strings.HasPrefix(resp, "error") {
			return &CliError{
				Message: fmt.Sprintf("Server error: %s", resp),
				Hint:    "The server may have crashed. Check logs and try 'gtl stop' then 'gtl start'.",
			}
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
func resolveEnvVars(pc *config.ProjectConfig, absPath string) map[string]string {
	reg := registry.New("")
	alloc := reg.Find(absPath)
	if alloc == nil {
		return nil
	}
	uc := config.LoadUserConfig("")
	interpAlloc := interpolation.Allocation(alloc)
	branch := worktree.CurrentBranch(absPath)
	setup.InjectRouterTokens(interpAlloc, pc.Project(), branch, uc.RouterDomain())
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
