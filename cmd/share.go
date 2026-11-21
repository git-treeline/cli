package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/share"
	"github.com/git-treeline/cli/internal/templates"
	"github.com/git-treeline/cli/internal/tunnel"
	"github.com/spf13/cobra"
)

var shareTunnelName string
var shareTailscale bool

func init() {
	shareCmd.Flags().StringVar(&shareTunnelName, "tunnel", "", "Named tunnel config to use (overrides default)")
	shareCmd.Flags().BoolVar(&shareTailscale, "tailscale", false, "Share via Tailscale Serve (tailnet-only, identity-based auth)")
	shareCmd.MarkFlagsMutuallyExclusive("tunnel", "tailscale")
	rootCmd.AddCommand(shareCmd)
}

var shareCmd = &cobra.Command{
	Use:   "share [port]",
	Short: "Create a private share URL for a local port",
	Long: `Creates a private URL for sharing a local dev server.

Default (Cloudflare tunnel):
  gtl share              # share current worktree's port
  gtl share 3050         # share a specific port

If a named tunnel is configured (via 'gtl tunnel setup'), the share URL
uses your domain. Otherwise falls back to a random *.trycloudflare.com URL.
Use --tunnel <name> to pick a specific tunnel config.

Tailscale backend:
  gtl share --tailscale  # share via Tailscale Serve (tailnet-only)

The --tailscale flag uses Tailscale Serve to expose the port on your tailnet.
No token needed — Tailscale handles identity-based auth. Only people on your
tailnet can access the URL. Requires Tailscale to be installed and running.

Tokens rotate on every invocation. When you Ctrl+C, the share is destroyed.

Related commands:
  gtl tunnel   Public tunnel with predictable subdomains (webhooks, OAuth)
  gtl serve    Local HTTPS subdomain router`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _, err := resolveTunnelTarget(args)
		if err != nil {
			return cliErr(cmd, err)
		}

		if shareTailscale {
			return share.RunTailscale(port)
		}

		uc := config.LoadUserConfig("")
		tunnelName := uc.TunnelName(shareTunnelName)
		domain := uc.TunnelDomain(shareTunnelName)

		// share.Run falls back to a quick tunnel when the named one is already
		// running, so the hint must match.
		hintDomain := domain
		if tunnelName == "" || tunnel.IsTunnelRunning(tunnelName) {
			hintDomain = ""
		}
		printShareHint(hintDomain)

		return share.Run(port, tunnelName, domain)
	},
}

func printShareHint(domain string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	absPath, _ := filepath.Abs(cwd)
	det := detect.Detect(absPath)
	if formatted := templates.FormatTunnelHint(templates.ShareHint(det, domain)); formatted != "" {
		fmt.Fprint(os.Stderr, formatted)
		fmt.Fprintln(os.Stderr)
	}
}
