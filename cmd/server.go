package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/interpolation"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/resolve"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/git-treeline/git-treeline/internal/supervisor"
	"github.com/git-treeline/git-treeline/internal/templates"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var startAwait bool
var startAwaitTimeout int

func init() {
	startCmd.Flags().BoolVar(&startAwait, "await", false, "Block until the server is accepting connections, then exit 0")
	startCmd.Flags().IntVar(&startAwaitTimeout, "await-timeout", 60, "Timeout in seconds for --await")
	rootCmd.AddCommand(startCmd)
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
		// Load from worktree (not mainRepo) so branch-specific config is respected
		pc := config.LoadProjectConfig(absPath)

		startCommand := pc.StartCommand()
		if startCommand == "" {
			return errNoStartCommand()
		}

		warnPortWiring(startCommand, absPath)

		sockPath := supervisor.SocketPath(absPath)
		port := resolvePort(absPath)

		startCommand = interpolateCommand(startCommand, port)

		resp, err := supervisor.Send(sockPath, "status")
		if err == nil {
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

		uc := config.LoadUserConfig("")
		if err := setup.RegenerateEnvFile(absPath, uc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: env sync skipped: %s\n", err)
		}
		branch := worktree.CurrentBranch(absPath)
		setup.ConfigureEditor(absPath, pc, uc, port, branch)

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
		return sv.Run()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the dev server (supervisor stays alive for resume)",
	Long: `Stop the running dev server process. The supervisor remains alive so
the server can be resumed with 'gtl start'. Use Ctrl+C in the original
terminal to fully exit the supervisor.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		sockPath, err := resolveSocket()
		if err != nil {
			return err
		}
		resp, err := supervisor.Send(sockPath, "stop")
		if err != nil {
			return err
		}
		if strings.HasPrefix(resp, "error") {
			return &CliError{
				Message: fmt.Sprintf("Server error: %s", resp),
				Hint:    "The supervisor may be in an unexpected state. Check 'gtl start' output.",
			}
		}
		fmt.Println("Server stopped. Supervisor still running — 'gtl start' to resume.")
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
