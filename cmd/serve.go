package cmd

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/style"
	"github.com/spf13/cobra"
)

func routeHostnames(baseDomain string) []string {
	reg := registry.New("")
	allocs := reg.Allocations()
	var hostnames []string
	for _, a := range allocs {
		project, _ := a["project"].(string)
		branch, _ := a["branch"].(string)
		if project == "" {
			continue
		}
		key := proxy.RouteKey(project, branch)
		hostnames = append(hostnames, key+"."+baseDomain)
	}
	return hostnames
}

func init() {
	serveCmd.AddCommand(serveInstallCmd)
	serveCmd.AddCommand(serveUninstallCmd)
	serveCmd.AddCommand(serveStatusCmd)
	serveCmd.AddCommand(serveRunCmd)
	serveHostsCmd.AddCommand(serveHostsSyncCmd)
	serveHostsCmd.AddCommand(serveHostsCleanCmd)
	serveCmd.AddCommand(serveHostsCmd)
	serveAliasCmd.Flags().BoolVar(&serveAliasRemove, "remove", false, "Remove the named alias")
	serveCmd.AddCommand(serveAliasCmd)
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Local HTTPS subdomain router for worktree access",
	Long: `Starts a local HTTPS subdomain router that maps {project}-{branch}.{domain}
to the correct worktree port. Routes are derived from the git-treeline registry.
Default domain is prt.dev (configurable via router.domain).

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
  - Trusting the CA so browsers accept https://*.{domain}
  - Redirecting port 443 → the router so URLs need no port number

After install, access worktrees at https://{project}-{branch}.{domain}`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("gtl serve requires macOS or Linux (detected %s).", runtime.GOOS),
				Hint:    "Windows support via WSL2 is planned.",
			})
		}

		uc := config.LoadUserConfig("")
		if err := runServeInstall(uc); err != nil {
			return err
		}
		if issues := routerInstallIssues(); len(issues) > 0 {
			return fmt.Errorf("HTTPS router install incomplete: missing %s", strings.Join(issues, ", "))
		}

		domain := uc.RouterDomain()
		fmt.Println()
		fmt.Println(style.Actionf("Router running."))
		fmt.Printf("  Status: %s\n", style.Cmd("gtl serve status"))
		fmt.Printf("  URL:    %s\n", style.Link(fmt.Sprintf("https://{project}-{branch}.%s", domain)))
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

		if err := service.CleanHosts(); err != nil {
			fmt.Fprintln(os.Stderr, style.Warnf("could not clean hosts file: %v", err))
		} else if runtime.GOOS == "darwin" {
			fmt.Println("Hosts entries removed.")
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
			warnRouterVersionMismatch()
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

		domain := uc.RouterDomain()
		reg := registry.New("")
		router := proxy.NewRouter(port, reg).
			WithBaseDomain(domain).
			WithAliases(func() map[string]int { return config.LoadUserConfig("").RouterAliases() }).
			WithAliases(projectAliases(reg))
		router.Refresh()
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
				fmt.Printf("  %s://%s.%s → :%d\n", scheme, key, domain, routes[key])
			} else {
				fmt.Printf("  %s://%s.%s:%d → :%d\n", scheme, key, domain, port, routes[key])
			}
		}

		if runtime.GOOS == "darwin" && uc.SafariWarningsEnabled() {
			hostnames := routeHostnames(domain)
			if service.NeedsHostsSync(hostnames) {
				fmt.Println()
				fmt.Fprintln(os.Stderr, style.Warnf("Safari may not resolve some routes (hosts file out of date)."))
				fmt.Fprintln(os.Stderr, style.Dimf("  Run: gtl serve hosts sync"))
				fmt.Fprintln(os.Stderr, style.Dimf("  Or disable: gtl config set warnings.safari false"))
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
	service.WriteRouterVersion(Version)
	uc := config.LoadUserConfig("")
	port := uc.RouterPort()
	domain := uc.RouterDomain()
	reg := registry.New("")
	router := proxy.NewRouter(port, reg).
		WithBaseDomain(domain).
		WithAliases(func() map[string]int { return config.LoadUserConfig("").RouterAliases() }).
		WithAliases(projectAliases(reg))
	if proxy.IsCAInstalled() {
		router.WithTLS()
	}
	return router.Run()
}

// projectAliases returns an AliasSource that merges aliases from all
// registered worktrees' project configs.
func projectAliases(reg *registry.Registry) proxy.AliasSource {
	return func() map[string]int {
		allocs := reg.Allocations()
		seen := make(map[string]bool)
		merged := make(map[string]int)
		for _, a := range allocs {
			wt, _ := a["worktree"].(string)
			if wt == "" || seen[wt] {
				continue
			}
			seen[wt] = true
			pc := config.LoadProjectConfig(wt)
			for name, port := range pc.Aliases() {
				merged[name] = port
			}
		}
		return merged
	}
}

// warnRouterVersionMismatch prints a warning if the running router was started
// by a different version of the CLI.
func warnRouterVersionMismatch() {
	running := service.RunningRouterVersion()
	if running == "" || running == Version {
		return
	}
	fmt.Fprintln(os.Stderr, style.Warnf("Router is running %s but CLI is %s.", running, Version))
	fmt.Fprintln(os.Stderr, style.Dimf("  Run 'gtl serve install' to update the router."))
}

var serveAliasRemove bool

var serveAliasCmd = &cobra.Command{
	Use:   "alias [name] [port]",
	Short: "Manage static alias routes",
	Long: `Add, remove, or list static subdomain aliases.

Aliases let you route non-gtl services through the router:
  gtl serve alias redis-ui 8081    → redis-ui.{domain}:8081
  gtl serve alias redis-ui         → detect port from current directory
  gtl serve alias --remove redis-ui
  gtl serve alias                  → list all aliases`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")

		if len(args) == 0 {
			if serveAliasRemove {
				return cliErr(cmd, &CliError{
					Message: "Missing alias name.",
					Hint:    "Usage: gtl serve alias --remove <name>",
				})
			}
			aliases := uc.RouterAliases()
			if len(aliases) == 0 {
				fmt.Println("No aliases configured.")
				return nil
			}
			fmt.Println("User aliases:")
			for name, port := range aliases {
				fmt.Printf("  %s → :%d\n", name, port)
			}
			return nil
		}

		name := args[0]
		if serveAliasRemove {
			aliases, _ := config.Dig(uc.Data, "router", "aliases").(map[string]any)
			if aliases == nil {
				return cliErr(cmd, &CliError{
					Message: fmt.Sprintf("Alias %q not found.", name),
					Hint:    "Run 'gtl serve alias' to list existing aliases.",
				})
			}
			if _, exists := aliases[name]; !exists {
				return cliErr(cmd, &CliError{
					Message: fmt.Sprintf("Alias %q not found.", name),
					Hint:    "Run 'gtl serve alias' to list existing aliases.",
				})
			}
			delete(aliases, name)
			if err := uc.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}
			fmt.Printf("Removed alias %q.\n", name)
			return nil
		}

		var port int
		if len(args) >= 2 {
			var err error
			port, err = strconv.Atoi(args[1])
			if err != nil || port < 1 || port > 65535 {
				return cliErr(cmd, errInvalidPort(args[1]))
			}
		} else {
			detected, err := detectAliasPort()
			if err != nil {
				return cliErr(cmd, err)
			}
			port = detected
		}

		uc.Set("router.aliases."+name, float64(port))
		if err := uc.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Printf("Alias %s.%s → :%d\n", name, uc.RouterDomain(), port)
		fmt.Println("The router will pick this up on next refresh (~5s).")
		return nil
	},
}

// detectAliasPort resolves the port for the current directory's allocation.
// If the allocation has multiple ports, the user is prompted to choose.
func detectAliasPort() (int, error) {
	absPath, err := os.Getwd()
	if err != nil {
		return 0, &CliError{
			Message: "Could not determine current directory.",
			Hint:    "Usage: gtl serve alias <name> <port>",
		}
	}
	return detectAliasPortFrom(absPath, registry.New(""), nil)
}

// detectAliasPortFrom is the testable core. reader overrides stdin for the
// multi-port prompt; pass nil for real interactive use.
func detectAliasPortFrom(absPath string, reg *registry.Registry, reader io.Reader) (int, error) {
	alloc := reg.Find(absPath)
	if alloc == nil {
		return 0, &CliError{
			Message: "No port found for this directory.",
			Hint:    "Run 'gtl setup' to allocate a port, or specify one explicitly:\n  gtl serve alias <name> <port>",
		}
	}

	ports := registry.ExtractPorts(alloc)
	if len(ports) == 0 {
		return 0, &CliError{
			Message: "Allocation exists but has no ports assigned.",
			Hint:    "Run 'gtl start' to assign a port, or specify one explicitly:\n  gtl serve alias <name> <port>",
		}
	}

	if len(ports) == 1 {
		return ports[0], nil
	}

	options := make([]string, len(ports))
	for i, p := range ports {
		options[i] = strconv.Itoa(p)
	}
	idx := confirm.Select("Multiple ports allocated — which one?", options, 0, reader)
	return ports[idx], nil
}

var serveHostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "Manage /etc/hosts entries for Safari support (macOS)",
	Long: `Safari on macOS does not resolve *.localhost subdomains without /etc/hosts
entries. These commands add and remove managed entries so Safari works
with the gtl router.

Other browsers (Chrome, Firefox, Arc) resolve *.localhost natively.`,
}

var serveHostsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Add /etc/hosts entries for all active routes",
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		domain := uc.RouterDomain()
		if runtime.GOOS != "darwin" && domain == "localhost" {
			fmt.Println("Hosts sync is macOS-only (*.localhost resolves natively on Linux).")
			return nil
		}
		hostnames := routeHostnames(domain)
		if len(hostnames) == 0 {
			fmt.Println("No active routes to sync.")
			return nil
		}
		if err := service.SyncHosts(hostnames); err != nil {
			return fmt.Errorf("hosts sync failed: %w", err)
		}
		fmt.Printf("Synced %d host(s) to /etc/hosts.\n", len(hostnames))
		return nil
	},
}

var serveHostsCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove all git-treeline entries from /etc/hosts",
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			fmt.Println("Nothing to clean (hosts sync is macOS-only).")
			return nil
		}
		if err := service.CleanHosts(); err != nil {
			return fmt.Errorf("hosts clean failed: %w", err)
		}
		fmt.Println("Removed git-treeline entries from /etc/hosts.")
		return nil
	},
}

func sortedRouteKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
