package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/interpolation"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/resolve"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/supervisor"
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
		mainRepo := worktree.DetectMainRepo(absPath)
		pc := config.LoadProjectConfig(mainRepo)

		startCommand := pc.StartCommand()
		if startCommand == "" {
			return fmt.Errorf("no commands.start configured in .treeline.yml")
		}

		sockPath := supervisor.SocketPath(absPath)
		port := resolvePort(absPath)

		resp, err := supervisor.Send(sockPath, "status")
		if err == nil {
			if resp == "running" {
				if startAwait {
					return awaitReady(sockPath)
				}
				return fmt.Errorf("server is already running — use 'gtl restart' to restart it")
			}
			resp, err = supervisor.Send(sockPath, "start")
			if err != nil {
				return err
			}
			if strings.HasPrefix(resp, "error") {
				return fmt.Errorf("server: %s", resp)
			}
			fmt.Println("Server resumed.")
			if startAwait {
				return awaitReady(sockPath)
			}
			return nil
		}

		if startAwait {
			sv := supervisor.New(startCommand, absPath, sockPath)
			sv.Env = resolveEnvVars(pc, absPath)
			sv.Port = port
			go func() { _ = sv.Run() }()

			for i := 0; i < 50; i++ {
				time.Sleep(100 * time.Millisecond)
				if _, err := os.Stat(sockPath); err == nil {
					break
				}
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
			return fmt.Errorf("server: %s", resp)
		}
		fmt.Println("Server stopped. Supervisor still running — 'gtl start' to resume.")
		return nil
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the supervised dev server",
	RunE: func(cmd *cobra.Command, args []string) error {
		sockPath, err := resolveSocket()
		if err != nil {
			return err
		}
		resp, err := supervisor.Send(sockPath, "restart")
		if err != nil {
			return err
		}
		if strings.HasPrefix(resp, "error") {
			return fmt.Errorf("server: %s", resp)
		}
		fmt.Println("Server restarted.")
		return nil
	},
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
	redisURL := interpolation.BuildRedisURL(
		config.LoadUserConfig("").RedisURL(),
		interpolation.Allocation(alloc),
	)
	branch := detectCurrentBranch(absPath)
	r := resolve.New(reg, absPath, branch)
	result, err := setup.BuildEnvVarsWithResolver(pc, interpolation.Allocation(alloc), redisURL, r.Resolve)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", err)
		fmt.Fprintf(os.Stderr, "  {resolve:...} tokens will not be expanded in process env.\n")
		fmt.Fprintf(os.Stderr, "  Your app should read from the env file (written correctly by gtl setup).\n")
		return setup.BuildEnvVars(pc, interpolation.Allocation(alloc), redisURL)
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

func awaitReady(sockPath string) error {
	cmd := fmt.Sprintf("wait-ready:%d", startAwaitTimeout)
	resp, err := supervisor.SendWithTimeout(sockPath, cmd, time.Duration(startAwaitTimeout+5)*time.Second)
	if err != nil {
		return fmt.Errorf("waiting for server: %w", err)
	}
	if resp == "ok" {
		fmt.Println("Server is ready.")
		return nil
	}
	return fmt.Errorf("server: %s", resp)
}
