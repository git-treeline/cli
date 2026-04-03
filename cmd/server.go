package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/interpolation"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/setup"
	"github.com/git-treeline/git-treeline/internal/supervisor"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

func init() {
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

		resp, err := supervisor.Send(sockPath, "status")
		if err == nil {
			if resp == "running" {
				return fmt.Errorf("server is already running — use 'gtl restart' to restart it")
			}
			// Supervisor alive but child stopped — resume it
			resp, err = supervisor.Send(sockPath, "start")
			if err != nil {
				return err
			}
			if strings.HasPrefix(resp, "error") {
				return fmt.Errorf("server: %s", resp)
			}
			fmt.Println("Server resumed.")
			return nil
		}

		sv := supervisor.New(startCommand, absPath, sockPath)
		sv.Env = resolveEnvVars(pc, absPath)
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
// interpolates the env template from the project config. Returns nil if
// there's no allocation or no env template.
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
	return setup.BuildEnvVars(pc, interpolation.Allocation(alloc), redisURL)
}

func resolveSocket() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	absPath, _ := filepath.Abs(cwd)
	return supervisor.SocketPath(absPath), nil
}
