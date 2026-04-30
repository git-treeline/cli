package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/confirm"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/templates"
	"github.com/git-treeline/cli/internal/tunnel"
	"github.com/git-treeline/cli/internal/worktree"
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
	tunnelCmd.AddCommand(tunnelRemoveCmd)
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
  gtl serve    Local HTTPS subdomain router (https://{branch}.prt.dev)
  gtl proxy    Forward a single port (e.g. OAuth callbacks on :3000)`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		port, entry, err := resolveTunnelTarget(args)
		if err != nil {
			return cliErr(cmd, err)
		}

		domain := tunnelDomain
		if domain == "" {
			domain = uc.TunnelDomain(tunnelConfigName)
		}

		if domain != "" {
			tunnelName := uc.TunnelName(tunnelConfigName)
			if tunnelName == "" {
				return cliErr(cmd, &CliError{
					Message: "Tunnel domain is configured but no tunnel name found.",
					Hint:    "Run 'gtl tunnel setup' to complete configuration.",
				})
			}
			if err := validateTunnelPrereqs(tunnelName); err != nil {
				return cliErr(cmd, err)
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
	Long: `Interactive setup for named tunnels. Installs cloudflared if needed,
authenticates with Cloudflare, creates a tunnel, and routes your domain.

After setup, 'gtl tunnel' will automatically use your domain.
Subdomains are derived from project and branch names, matching gtl serve routes.

Multi-domain support: Each domain requires authentication with the Cloudflare
account that owns that zone. gtl stores per-domain credentials automatically.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")

		if _, err := tunnel.ResolveCloudflared(); err != nil {
			if !confirm.Prompt("cloudflared not found. Install it?", false, nil) {
				return cliErr(cmd, &CliError{
					Message: "cloudflared is required for tunnel setup.",
					Hint:    "Install it with 'brew install cloudflare/cloudflare/cloudflared' or see https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/",
				})
			}
			if !tunnel.OfferInstall() {
				return cliErr(cmd, &CliError{
					Message: "cloudflared still not found after install attempt.",
					Hint:    "Install manually: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/",
				})
			}
			if _, err := tunnel.ResolveCloudflared(); err != nil {
				return cliErr(cmd, &CliError{
					Message: "cloudflared still not found after install attempt.",
					Hint:    "Install manually: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/",
				})
			}
		}

		// Initial login if no credentials exist at all
		if !tunnel.IsLoggedIn() {
			fmt.Println(style.Actionf("Opening Cloudflare login — select the account that owns your domain."))
			if err := tunnel.Login(); err != nil {
				return fmt.Errorf("cloudflared login failed: %w", err)
			}
		}

		tunnelName := uc.TunnelDefault()
		if tunnelName == "" {
			tunnelName = "gtl"
		}
		existingConfigs := uc.TunnelConfigs()
		if len(existingConfigs) == 0 {
			fmt.Println("Identifier for this machine (e.g. gtl, gtl-work)")
		}
		tunnelName = confirm.Input("Tunnel identifier", tunnelName, nil)
		tunnelName = strings.TrimSpace(tunnelName)
		if tunnelName == "" || strings.ContainsAny(tunnelName, " \t\n/\\:.") {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Invalid tunnel name %q.", tunnelName),
				Hint:    "Use only letters, numbers, and hyphens (this is an identifier, not your domain).",
			})
		}

		if !tunnel.TunnelExists(tunnelName) {
			fmt.Println(style.Actionf("Creating tunnel %q", tunnelName))
			if err := tunnel.CreateTunnel(tunnelName); err != nil {
				return fmt.Errorf("failed to create tunnel: %w", err)
			}
		}

		existingDomain := uc.TunnelDomain("")
		domain := strings.TrimSpace(confirm.Input("Domain (e.g. myteam.dev)", existingDomain, nil))
		if domain == "" {
			return cliErr(cmd, &CliError{
				Message: "Domain is required for named tunnel setup.",
				Hint:    "Enter the domain you manage in Cloudflare, e.g. myteam.dev",
			})
		}
		if strings.ContainsAny(domain, " \t\n/:") || !strings.Contains(domain, ".") {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Invalid domain %q.", domain),
				Hint:    "Expected a bare domain like myteam.dev (no protocol, path, or port).",
			})
		}

		certPath := tunnel.CertPathForDomain(domain)
		if !tunnel.IsLoggedInForDomain(domain) {
			// Try with default cert first
			certPath = ""
		}

		wildcardHost := "*." + domain
		fmt.Println(style.Actionf("Routing %s → tunnel %q", wildcardHost, tunnelName))
		if err := tunnel.RouteDNSWithCert(tunnelName, wildcardHost, certPath); err != nil {
			return cliErr(cmd, printDNSManualInstructions(tunnelName, domain, err))
		}

		// Verify DNS was created in the correct zone
		testHost := "gtl-verify." + domain
		fmt.Println(style.Dimf("Verifying DNS propagation..."))
		if !tunnel.VerifyDNS(testHost, 10*time.Second) {
			// DNS routing "succeeded" but record wasn't created in the right zone
			// This happens when cert.pem is scoped to a different zone
			fmt.Println()
			fmt.Println(style.Warnf("DNS record was not created in the %s zone.", domain))
			fmt.Println(style.Dimf("Your cloudflared credentials are for a different Cloudflare zone."))
			fmt.Println()

			if confirm.Prompt(fmt.Sprintf("Authenticate with the Cloudflare account that owns %s?", domain), true, nil) {
				fmt.Println()
				fmt.Println(style.Actionf("Opening Cloudflare login — select the zone for %s", domain))
				if err := tunnel.LoginForDomain(domain); err != nil {
					return fmt.Errorf("login failed: %w", err)
				}

			certPath = tunnel.CertPathForDomain(domain)
				fmt.Println(style.Actionf("Routing %s → tunnel %q", wildcardHost, tunnelName))
				if err := tunnel.RouteDNSWithCert(tunnelName, wildcardHost, certPath); err != nil {
					return cliErr(cmd, printDNSManualInstructions(tunnelName, domain, err))
				}

			if !tunnel.VerifyDNS(testHost, 10*time.Second) {
					return cliErr(cmd, printDNSManualInstructions(tunnelName, domain, nil))
				}
			} else {
				return cliErr(cmd, printDNSManualInstructions(tunnelName, domain, nil))
			}
		}

		if certPath != "" {
			uc.Set("tunnel.tunnels."+tunnelName+".cert", certPath)
		}

		uc.Set("tunnel.tunnels."+tunnelName+".domain", domain)
		currentDefault := uc.TunnelDefault()
		if currentDefault == "" || len(existingConfigs) <= 1 {
			uc.Set("tunnel.default", tunnelName)
		} else if currentDefault != tunnelName {
			if confirm.Prompt(fmt.Sprintf("Make default? (current: %s)", currentDefault), false, nil) {
				uc.Set("tunnel.default", tunnelName)
			}
		}
		if err := uc.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		if uc.TunnelDefault() == tunnelName {
			fmt.Println(style.Actionf("Saved (default)"))
		} else {
			fmt.Println(style.Actionf("Saved"))
		}

		fmt.Println()
		fmt.Printf("%s gtl tunnel → %s\n", style.Successf("Done!"), style.Link("https://{branch}."+domain))
		return nil
	},
}

func printDNSManualInstructions(tunnelName, domain string, err error) error {
	tunnelUUID := tunnel.GetTunnelUUID(tunnelName)
	if tunnelUUID == "" {
		tunnelUUID = tunnelName
	}

	fmt.Println()
	fmt.Println(style.Warnf("Could not create DNS record automatically."))
	fmt.Println()
	fmt.Println("Add this record manually in Cloudflare DNS for", style.Bold.Render(domain)+":")
	fmt.Println()
	fmt.Printf("  Type:   CNAME\n")
	fmt.Printf("  Name:   *\n")
	fmt.Printf("  Target: %s.cfargotunnel.com\n", tunnelUUID)
	fmt.Printf("  Proxy:  Proxied (orange cloud)\n")
	fmt.Println()

	if err != nil {
		return &CliError{
			Message: "DNS routing failed",
			Hint:    "Add the CNAME record manually in Cloudflare dashboard, then re-run setup.",
		}
	}
	return &CliError{
		Message: "DNS record not found in target zone",
		Hint:    "Add the CNAME record manually in Cloudflare dashboard.",
	}
}

var tunnelStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show tunnel configuration and readiness",
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")

		if _, err := tunnel.ResolveCloudflared(); err != nil {
			fmt.Println("cloudflared: not installed")
			fmt.Println("Authentication: unknown")
		} else {
			fmt.Println("cloudflared: installed")
			if tunnel.IsLoggedIn() {
				fmt.Println("Authentication: logged in")
			} else {
				fmt.Println("Authentication: not logged in (run 'gtl tunnel setup')")
			}
		}

		configs := uc.TunnelConfigs()
		defaultName := uc.TunnelDefault()

		if len(configs) == 0 {
			fmt.Println("Tunnels: not configured (run 'gtl tunnel setup')")
		} else {
			fmt.Println("Tunnels:")
			for name, domain := range configs {
				marker := "  "
				suffix := ""
				if name == defaultName {
					marker = "* "
					suffix = " (default)"
				}
				exists := tunnel.TunnelExists(name)
				notFound := ""
				if !exists {
					notFound = " (not found)"
				}
				if domain != "" {
					fmt.Printf("%s%s  *.%s%s%s\n", marker, name, domain, notFound, suffix)
				} else {
					fmt.Printf("%s%s%s%s\n", marker, name, notFound, suffix)
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
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Tunnel %q not found in config.", name),
				Hint:    fmt.Sprintf("Available tunnels: %v", tunnelConfigNames(configs)),
			})
		}
		uc.Set("tunnel.default", name)
		if err := uc.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Printf("Default tunnel set to %q\n", name)
		return nil
	},
}

var tunnelRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a named tunnel configuration",
	Long: `Remove a tunnel from the local config. Does not delete the Cloudflare tunnel
itself — run 'cloudflared tunnel delete <name>' separately if needed.

If the removed tunnel was the default and other tunnels remain, another is
promoted automatically. If it was the last tunnel, gtl falls back to quick
tunnels (random *.trycloudflare.com URLs).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc := config.LoadUserConfig("")
		name := args[0]

		configs := uc.TunnelConfigs()
		if _, ok := configs[name]; !ok {
			return cliErr(cmd, &CliError{
				Message: fmt.Sprintf("Tunnel %q not found in config.", name),
				Hint:    fmt.Sprintf("Available tunnels: %v", tunnelConfigNames(configs)),
			})
		}

		wasDefault := uc.TunnelDefault() == name
		newDefault := uc.DeleteTunnel(name)
		if err := uc.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Removed tunnel %q from config.\n", name)
		if wasDefault && newDefault != "" {
			fmt.Printf("Default tunnel is now %q.\n", newDefault)
		} else if wasDefault {
			fmt.Println("No tunnels remaining — gtl tunnel will use quick tunnels.")
		}
		fmt.Printf("\nTo delete the Cloudflare tunnel: cloudflared tunnel delete %s\n", name)
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
		return &CliError{
			Message: "Not authenticated with Cloudflare.",
			Hint:    "Run 'gtl tunnel setup' to authenticate.",
		}
	}
	if !tunnel.TunnelExists(tunnelName) {
		return &CliError{
			Message: fmt.Sprintf("Tunnel %q not found.", tunnelName),
			Hint:    "Run 'gtl tunnel setup' to create it.",
		}
	}
	return nil
}

func resolveTunnelTarget(args []string) (int, format.Allocation, error) {
	if len(args) == 1 {
		p, err := strconv.Atoi(args[0])
		if err != nil || p < 1 || p > 65535 {
			return 0, nil, errInvalidPort(args[0])
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
		return 0, nil, &CliError{
			Message: fmt.Sprintf("No allocation found for %s", absPath),
			Hint:    "Run 'gtl setup' first, or specify a port: gtl tunnel <port>",
		}
	}

	ports := format.GetPorts(format.Allocation(entry))
	if len(ports) == 0 {
		return 0, nil, errNoAllocationNoPorts(absPath)
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

	pc := config.LoadProjectConfig(absPath)
	project := pc.Project()
	if project == "" {
		return "", ""
	}

	branch := worktree.CurrentBranch(absPath)
	return project, branch
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
