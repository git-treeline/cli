package cmd

import (
	"fmt"
	"os"
	"runtime"
	"sort"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/style"
	"github.com/spf13/cobra"
)

func init() {
	serveCmd.AddCommand(serveInstallCmd)
	serveCmd.AddCommand(serveUninstallCmd)
	serveCmd.AddCommand(serveStatusCmd)
	serveCmd.AddCommand(serveRunCmd)
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Local HTTPS subdomain router for worktree access",
	Long: `Starts a local HTTPS subdomain router that maps {project}-{branch}.localhost
to the correct worktree port. Routes are derived from the git-treeline registry.

When run without a subcommand, starts in foreground mode (useful for testing).
Use 'gtl serve install' to run as a persistent system service.

Related commands:
  gtl proxy     Forward a single port (e.g. OAuth callbacks on :3000)
  gtl tunnel    Public HTTPS tunneling via Cloudflare`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRouter()
	},
}

var serveInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the router as a system service with HTTPS",
	Long: `One-time setup that generates HTTPS certificates, trusts them in your
system keychain, sets up port forwarding, and installs a background service.

Requires sudo for two things (explained before each prompt):
  - Trusting the CA so browsers accept https://*.localhost
  - Redirecting port 443 → the router so URLs need no port number

After install, access worktrees at https://{project}-{branch}.localhost`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return fmt.Errorf("gtl serve requires macOS or Linux (detected %s)", runtime.GOOS)
		}

		gtlPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("could not resolve executable path: %w", err)
		}

		uc := config.LoadUserConfig("")
		port := uc.RouterPort()

		caCertFile, err := proxy.EnsureCA()
		if err != nil {
			return fmt.Errorf("CA generation failed: %w", err)
		}

		fmt.Println("System password needed for:")
		fmt.Println("  1. Trusting the CA (browsers accept *.localhost)")
		fmt.Printf("  2. Port forwarding (443 → %d)\n", port)
		fmt.Println()

		if err := proxy.TrustCA(caCertFile); err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("CA trust failed: %v", err))
			fmt.Fprintln(os.Stderr, style.Dimf("  HTTPS will work but browsers will show a certificate warning."))
		}

		if err := service.InstallPortForward(port); err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("port forwarding skipped: %v", err))
			fmt.Fprintln(os.Stderr, style.Dimf("  URLs will require a port number: https://{branch}.localhost:%d", port))
			fmt.Println()
		}

		if _, err := service.Install(gtlPath, port); err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(style.Actionf("Router running."))
		fmt.Printf("  Status: %s\n", style.Cmd("gtl serve status"))
		fmt.Printf("  URL:    %s\n", style.Link("https://{project}-{branch}.localhost"))
		return nil
	},
}

var serveUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop and remove the router, CA trust, and port forwarding",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.Uninstall(); err != nil {
			return err
		}
		fmt.Println("Router service removed.")

		if service.IsPortForwardConfigured() {
			if err := service.UninstallPortForward(); err != nil {
				fmt.Fprintln(os.Stderr, style.Warnf("could not remove port forwarding: %v", err))
			} else {
				fmt.Println("Port forwarding removed.")
			}
		}

		if err := proxy.UntrustCA(); err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("could not remove CA trust: %v", err))
		} else {
			fmt.Println("CA trust removed.")
		}
		return nil
	},
}

var serveStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show router service status and active routes",
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		port := uc.RouterPort()
		portFwd := service.IsPortForwardConfigured()
		caInstalled := proxy.IsCAInstalled()

		if service.IsRunning() {
			fmt.Printf("Router: running on port %d (HTTPS)\n", port)
		} else {
			fmt.Printf("Router: not running (port %d configured)\n", port)
		}

		if caInstalled {
			fmt.Println("CA: installed")
		} else {
			fmt.Println("CA: not installed (run 'gtl serve install')")
		}

		if portFwd {
			fmt.Println("Port forwarding: active (443 → router)")
		} else {
			fmt.Println("Port forwarding: not configured")
		}

		reg := registry.New("")
		router := proxy.NewRouter(port, reg)
		if caInstalled {
			router.WithTLS()
		}
		routes := router.Routes()

		if len(routes) == 0 {
			fmt.Println("No active routes.")
			return nil
		}

		scheme := "https"
		if !caInstalled {
			scheme = "http"
		}

		fmt.Printf("\nRoutes (%d):\n", len(routes))
		for _, key := range sortedRouteKeys(routes) {
			if portFwd {
				fmt.Printf("  %s://%s.localhost → :%d\n", scheme, key, routes[key])
			} else {
				fmt.Printf("  %s://%s.localhost:%d → :%d\n", scheme, key, port, routes[key])
			}
		}
		return nil
	},
}

var serveRunCmd = &cobra.Command{
	Use:    "run",
	Short:  "Run the router daemon (called by launchd/systemd)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRouter()
	},
}

func runRouter() error {
	uc := config.LoadUserConfig("")
	port := uc.RouterPort()
	reg := registry.New("")
	router := proxy.NewRouter(port, reg)
	if proxy.IsCAInstalled() {
		router.WithTLS()
	}
	return router.Run()
}

func sortedRouteKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
