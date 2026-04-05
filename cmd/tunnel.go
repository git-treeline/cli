package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/confirm"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/templates"
	"github.com/git-treeline/git-treeline/internal/tunnel"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var tunnelDomain string
var tunnelConfigName string

func init() {
	tunnelCmd.PersistentFlags().StringVar(&tunnelConfigName, "tunnel", "", "Named tunnel config to use (overrides default)")
	tunnelCmd.Flags().StringVar(&tunnelDomain, "domain", "", "BYO domain (overrides saved config)")
	tunnelCmd.AddCommand(tunnelSetupCmd)
	tunnelCmd.AddCommand(tunnelStatusCmd)
	tunnelCmd.AddCommand(tunnelDefaultCmd)
	rootCmd.AddCommand(tunnelCmd)
}

var tunnelCmd = &cobra.Command{
	Use:   "tunnel [port]",
	Short: "Expose a local port via a public Cloudflare tunnel",
	Long: `Creates a public HTTPS URL that forwards to a local port using cloudflared.

Quick mode (random URL, no account needed):
  gtl tunnel              # expose current worktree's port
  gtl tunnel 3050         # expose specific port

Named tunnel (uses domain from 'gtl tunnel setup'):
  gtl tunnel              # current worktree via configured domain
  gtl tunnel --domain myteam.dev  # override domain

Run 'gtl tunnel setup' first to configure a named tunnel with your domain.
Run 'gtl tunnel status' to see current tunnel configuration.

Related commands:
  gtl serve    Local HTTPS subdomain router (https://{branch}.localhost)
  gtl proxy    Forward a single port (e.g. OAuth callbacks on :3000)`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		port, entry, err := resolveTunnelTarget(args)
		if err != nil {
			return err
		}

		domain := tunnelDomain
		if domain == "" {
			domain = uc.TunnelDomain(tunnelConfigName)
		}

		if domain != "" {
			tunnelName := uc.TunnelName(tunnelConfigName)
			if tunnelName == "" {
				return fmt.Errorf("tunnel domain is configured but no tunnel name found\nRun 'gtl tunnel setup' to complete configuration")
			}
			if err := validateTunnelPrereqs(tunnelName); err != nil {
				return err
			}

			project, branch := resolveProjectAndBranch(entry)
			if project != "" {
				routeKey := proxy.RouteKey(project, branch)
				hostname := routeKey + "." + domain
				printTunnelHint(hostname, domain)
				return tunnel.RunNamed(tunnelName, domain, routeKey, port)
			}

			fmt.Fprintf(os.Stderr, "Note: using quick tunnel (random URL) because project/branch could not be determined.\n")
			fmt.Fprintf(os.Stderr, "  For a named tunnel at *.%s, run from inside a git repo with a .treeline.yml.\n\n", domain)
		}

		printTunnelHint("", "")
		return tunnel.RunQuick(port)
	},
}

var tunnelSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure a named Cloudflare tunnel with your domain",
	Long: `Interactive setup for named tunnels. Walks you through:

  1. Installing cloudflared (if missing)
  2. Authenticating with Cloudflare (opens browser)
  3. Creating a named tunnel
  4. Routing a wildcard DNS record to your domain
  5. Saving the configuration

After setup, 'gtl tunnel' will automatically use your domain.
Subdomains are derived from project and branch names, matching gtl serve routes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")

		// Step 1: Check cloudflared
		fmt.Println("  Step 1: Checking cloudflared")
		if _, err := tunnel.ResolveCloudflared(); err != nil {
			if !tunnel.OfferInstall() {
				return fmt.Errorf("cloudflared is required for tunnel setup")
			}
			if _, err := tunnel.ResolveCloudflared(); err != nil {
				return fmt.Errorf("cloudflared still not found after install attempt")
			}
		}
		fmt.Println("    cloudflared found.")

		// Step 2: Authenticate
		fmt.Println()
		fmt.Println("  Step 2: Cloudflare authentication")
		if tunnel.IsLoggedIn() {
			fmt.Println("    Already authenticated.")
		} else {
			fmt.Println("    Opening Cloudflare login in your browser...")
			fmt.Println("    Select the account that owns your domain.")
			fmt.Println()
			if err := tunnel.Login(); err != nil {
				return fmt.Errorf("cloudflared login failed: %w", err)
			}
			fmt.Println("    Authenticated.")
		}

		// Step 3: Create tunnel
		fmt.Println()
		fmt.Println("  Step 3: Create tunnel")
		fmt.Println("    Cloudflare needs a named tunnel as the connection endpoint.")
		fmt.Println("    The name is just an identifier — one tunnel handles all your projects.")
		fmt.Println("    You'd only change this if you run multiple machines (e.g. gtl-work, gtl-home).")
		fmt.Println()
		tunnelName := uc.TunnelDefault()
		if tunnelName == "" {
			tunnelName = "gtl"
		}
		tunnelName = confirm.Input("    Tunnel name", tunnelName, nil)
		tunnelName = strings.TrimSpace(tunnelName)
		if tunnelName == "" || strings.ContainsAny(tunnelName, " \t\n/\\:") {
			return fmt.Errorf("invalid tunnel name %q — use only letters, numbers, and hyphens", tunnelName)
		}

		if tunnel.TunnelExists(tunnelName) {
			fmt.Printf("    Tunnel %q already exists.\n", tunnelName)
		} else {
			fmt.Printf("    Creating tunnel %q...\n", tunnelName)
			if err := tunnel.CreateTunnel(tunnelName); err != nil {
				return fmt.Errorf("failed to create tunnel: %w", err)
			}
			fmt.Println("    Tunnel created.")
		}

		// Step 4: Domain + DNS
		fmt.Println()
		fmt.Println("  Step 4: Domain configuration")
		fmt.Println("    Enter the domain you want to use for tunnel subdomains.")
		fmt.Println("    This domain must be on the Cloudflare account you just authenticated.")
		fmt.Println("    A wildcard DNS record (*.domain) will be created automatically.")
		fmt.Println()

		existingDomain := uc.TunnelDomain("")
		domain := strings.TrimSpace(confirm.Input("    Domain (e.g. myteam.dev)", existingDomain, nil))
		if domain == "" {
			return fmt.Errorf("domain is required for named tunnel setup")
		}
		if strings.ContainsAny(domain, " \t\n/:") || !strings.Contains(domain, ".") {
			return fmt.Errorf("invalid domain %q — expected something like myteam.dev", domain)
		}

		wildcardHost := "*." + domain
		fmt.Printf("    Routing %s → tunnel %q...\n", wildcardHost, tunnelName)
		if err := tunnel.RouteDNS(tunnelName, wildcardHost); err != nil {
			fmt.Fprintf(os.Stderr, "\n    DNS routing failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "    You may need to add a wildcard CNAME manually in Cloudflare DNS.")
			fmt.Fprintf(os.Stderr, "    CNAME: *.%s → %s.cfargotunnel.com\n", domain, tunnelName)
		} else {
			fmt.Println("    DNS configured.")
		}

		// Step 5: Save config
		fmt.Println()
		fmt.Println("  Step 5: Saving configuration")
		uc.Set("tunnel.tunnels."+tunnelName+".domain", domain)
		existingConfigs := uc.TunnelConfigs()
		if uc.TunnelDefault() == "" || len(existingConfigs) <= 1 {
			uc.Set("tunnel.default", tunnelName)
		}
		if err := uc.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		if uc.TunnelDefault() == tunnelName {
			fmt.Printf("    Saved tunnel %q (domain: %s, default)\n", tunnelName, domain)
		} else {
			fmt.Printf("    Saved tunnel %q (domain: %s)\n", tunnelName, domain)
			fmt.Printf("    Default tunnel is %q. Switch with: gtl tunnel default %s\n", uc.TunnelDefault(), tunnelName)
		}

		fmt.Println()
		fmt.Println("Done! You can now run:")
		fmt.Println("  gtl tunnel              # expose current worktree via your domain")
		fmt.Printf("  Example: https://salt-staff-reporting.%s\n", domain)
		return nil
	},
}

var tunnelStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show tunnel configuration and readiness",
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")

		// cloudflared
		if _, err := tunnel.ResolveCloudflared(); err != nil {
			fmt.Println("cloudflared: not installed")
		} else {
			fmt.Println("cloudflared: installed")
		}

		// Auth
		if tunnel.IsLoggedIn() {
			fmt.Println("Authentication: logged in")
		} else {
			fmt.Println("Authentication: not logged in (run 'gtl tunnel setup')")
		}

		// Config
		configs := uc.TunnelConfigs()
		defaultName := uc.TunnelDefault()

		if len(configs) == 0 {
			fmt.Println("Tunnels: not configured (run 'gtl tunnel setup')")
		} else {
			fmt.Println("Tunnels:")
			for name, domain := range configs {
				marker := "   "
				suffix := ""
				if name == defaultName {
					marker = " * "
					suffix = " (default)"
				}
				exists := tunnel.TunnelExists(name)
				status := "exists"
				if !exists {
					status = "not found"
				}
				if domain != "" {
					fmt.Printf("%s%s  *.%s  (%s)%s\n", marker, name, domain, status, suffix)
				} else {
					fmt.Printf("%s%s  (%s)%s\n", marker, name, status, suffix)
				}
			}
		}

		return nil
	},
}

var tunnelDefaultCmd = &cobra.Command{
	Use:   "default [name]",
	Short: "Get or set the default tunnel configuration",
	Long: `Without arguments, prints the current default tunnel name.
With an argument, sets the default to the named tunnel config.

  gtl tunnel default                # print current default
  gtl tunnel default gtl-personal   # switch default`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		if len(args) == 0 {
			d := uc.TunnelDefault()
			if d == "" {
				fmt.Println("No default tunnel configured. Run 'gtl tunnel setup'.")
			} else {
				fmt.Println(d)
			}
			return nil
		}

		name := args[0]
		configs := uc.TunnelConfigs()
		if _, ok := configs[name]; !ok {
			return fmt.Errorf("tunnel %q not found in config\nAvailable: %v\nRun 'gtl tunnel setup' to add a new tunnel", name, tunnelConfigNames(configs))
		}
		uc.Set("tunnel.default", name)
		if err := uc.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Printf("Default tunnel set to %q\n", name)
		return nil
	},
}

func tunnelConfigNames(configs map[string]string) []string {
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	return names
}

func validateTunnelPrereqs(tunnelName string) error {
	if !tunnel.IsLoggedIn() {
		return fmt.Errorf("not authenticated with Cloudflare\n  Run 'gtl tunnel setup' to log in and configure your tunnel")
	}
	if !tunnel.TunnelExists(tunnelName) {
		return fmt.Errorf("tunnel %q not found\n  Run 'gtl tunnel setup' to create it", tunnelName)
	}
	return nil
}

func resolveTunnelTarget(args []string) (int, format.Allocation, error) {
	if len(args) == 1 {
		p, err := strconv.Atoi(args[0])
		if err != nil || p < 1 || p > 65535 {
			return 0, nil, fmt.Errorf("invalid port: %s", args[0])
		}
		entry := findAllocationForCwd()
		return p, entry, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return 0, nil, err
	}
	absPath, _ := filepath.Abs(cwd)
	reg := registry.New("")
	entry := reg.Find(absPath)
	if entry == nil {
		return 0, nil, fmt.Errorf("no allocation found for %s\nSpecify a port: gtl tunnel <port>", absPath)
	}

	ports := format.GetPorts(format.Allocation(entry))
	if len(ports) == 0 {
		return 0, nil, fmt.Errorf("allocation exists but has no ports")
	}
	return ports[0], format.Allocation(entry), nil
}

// resolveProjectAndBranch tries the registry allocation first, then falls back
// to git (project from .treeline.yml or directory name, branch from HEAD).
func resolveProjectAndBranch(entry format.Allocation) (string, string) {
	if entry != nil {
		project, _ := entry["project"].(string)
		branch, _ := entry["branch"].(string)
		if project != "" {
			return project, branch
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	absPath, _ := filepath.Abs(cwd)

	mainRepo := worktree.DetectMainRepo(absPath)
	pc := config.LoadProjectConfig(mainRepo)
	project := pc.Project()
	if project == "" {
		return "", ""
	}

	branch := currentGitBranch(absPath)
	return project, branch
}

func currentGitBranch(dir string) string {
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func findAllocationForCwd() format.Allocation {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	absPath, _ := filepath.Abs(cwd)
	reg := registry.New("")
	entry := reg.Find(absPath)
	if entry == nil {
		return nil
	}
	return format.Allocation(entry)
}

func printTunnelHint(hostname, domain string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	absPath, _ := filepath.Abs(cwd)
	det := detect.Detect(absPath)
	hint := templates.TunnelHint(det, hostname, domain)
	if formatted := templates.FormatTunnelHint(hint); formatted != "" {
		fmt.Fprint(os.Stderr, formatted)
		fmt.Fprintln(os.Stderr)
	}
}
