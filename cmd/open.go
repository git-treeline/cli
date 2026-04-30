package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
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
			return cliErr(cmd, errNoAllocation(absPath))
		}

		fa := format.Allocation(entry)
		ports := format.GetPorts(fa)
		if len(ports) == 0 {
			return cliErr(cmd, errNoAllocationNoPorts(absPath))
		}

		pc := config.LoadProjectConfig(absPath)
		uc := config.LoadUserConfig("")

		project := pc.Project()
		branch := format.GetStr(fa, "branch")

		url := buildOpenURL(ports[0], project, branch, uc.RouterDomain(), uc.RouterPort(), service.IsRunning(), service.IsPortForwardConfigured())

		fmt.Printf("Opening %s\n", url)
		return cliErr(cmd, openBrowser(url))
	},
}

func buildOpenURL(port int, project, branch, domain string, routerPort int, svcRunning, pfConfigured bool) string {
	return proxy.BuildRouterURL(port, project, branch, domain, routerPort, svcRunning, pfConfigured)
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return &CliError{
			Message: fmt.Sprintf("Unsupported platform: %s", runtime.GOOS),
			Hint:    "'gtl open' supports macOS and Linux. Open the URL manually.",
		}
	}
}
