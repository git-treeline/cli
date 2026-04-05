package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(openCmd)
}

var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Open the current worktree in the browser",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)

		reg := registry.New("")
		entry := reg.Find(absPath)
		if entry == nil {
			fmt.Fprintf(os.Stderr, "No allocation found for %s\nRun `gtl setup` first.\n", absPath)
			os.Exit(1)
		}

		fa := format.Allocation(entry)
		ports := format.GetPorts(fa)
		if len(ports) == 0 {
			return fmt.Errorf("allocation exists but has no ports")
		}

		mainRepo := worktree.DetectMainRepo(absPath)
		pc := config.LoadProjectConfig(mainRepo)
		uc := config.LoadUserConfig("")

		project := pc.Project()
		branch := format.GetStr(fa, "branch")

		url := fmt.Sprintf("http://localhost:%d", ports[0])

		if branch != "" && service.IsRunning() {
			routeKey := proxy.RouteKey(project, branch)
			if service.IsPortForwardConfigured() {
				url = fmt.Sprintf("https://%s.localhost", routeKey)
			} else {
				routerPort := uc.RouterPort()
				url = fmt.Sprintf("https://%s.localhost:%d", routeKey, routerPort)
			}
		}

		fmt.Printf("Opening %s\n", url)
		return openBrowser(url)
	},
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}
