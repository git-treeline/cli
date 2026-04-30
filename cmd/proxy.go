package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/spf13/cobra"
)

var proxyTLS bool

func init() {
	proxyCmd.Flags().BoolVar(&proxyTLS, "tls", false, "Terminate TLS on the listen port (HTTPS → HTTP)")
	rootCmd.AddCommand(proxyCmd)
}

var proxyCmd = &cobra.Command{
	Use:   "proxy <listen-port> [target-port]",
	Short: "Forward traffic from a stable port to a worktree's allocated port",
	Long: `Starts a local reverse proxy that forwards all traffic (HTTP, WebSocket, SSE)
from the listen port to the target port. If target-port is omitted, the current
worktree's allocated port is used.

Use --tls to enable HTTPS on the listen port. Certificates are generated via
mkcert (if installed) or a self-signed fallback.

Examples:
  gtl proxy 3000 3050        # forward :3000 → :3050
  gtl proxy 3000             # forward :3000 → current worktree's port
  gtl proxy 3000 --tls       # HTTPS on :3000 → current worktree's port

Related commands:
  gtl serve    Local HTTPS subdomain router (https://{branch}.prt.dev)
  gtl tunnel   Public HTTPS tunneling via Cloudflare`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		listenPort, err := strconv.Atoi(args[0])
		if err != nil || listenPort < 1 || listenPort > 65535 {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Invalid listen port: %s", args[0]),
				Hint:    "Port must be a number between 1 and 65535.",
			})
		}

		var targetPort int
		if len(args) == 2 {
			targetPort, err = strconv.Atoi(args[1])
			if err != nil || targetPort < 1 || targetPort > 65535 {
				return cliErr(cmd, &CliError{
					Message: fmt.Sprintf("Invalid target port: %s", args[1]),
					Hint:    "Port must be a number between 1 and 65535.",
				})
			}
		} else {
			targetPort, err = inferTargetPort()
			if err != nil {
				return err
			}
		}

		if listenPort == targetPort {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Listen port and target port are the same (%d).", listenPort),
				Hint:    "The proxy needs different ports for listen and target.",
			})
		}

		return proxy.Run(proxy.Options{
			ListenPort: listenPort,
			TargetPort: targetPort,
			TLS:        proxyTLS,
		})
	},
}

func inferTargetPort() (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	absPath, _ := filepath.Abs(cwd)

	reg := registry.New("")
	entry := reg.Find(absPath)
	if entry == nil {
		return 0, errNoAllocation(absPath)
	}

	ports := format.GetPorts(format.Allocation(entry))
	if len(ports) == 0 {
		return 0, errNoAllocationNoPorts(absPath)
	}

	return ports[0], nil
}
