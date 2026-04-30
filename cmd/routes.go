package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
	"github.com/spf13/cobra"
)

var routesJSON bool

func init() {
	routesCmd.Flags().BoolVar(&routesJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(routesCmd)
}

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Show routing URLs for the current worktree",
	Long: `Print the router and tunnel URLs for every allocated port in the
current worktree. When the HTTPS router is running, shows https://
URLs via prt.dev; otherwise falls back to http://localhost.

Examples:
  gtl routes              # human-readable output
  gtl routes --json       # structured output for scripting`,
	Args: cobra.NoArgs,
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
		domain := uc.RouterDomain()
		routerPort := uc.RouterPort()
		svcRunning := service.IsRunning()
		pfConfigured := service.IsPortForwardConfigured()

		routes := buildRoutes(ports, project, branch, domain, routerPort, svcRunning, pfConfigured)

		tunnelDomain := uc.TunnelDomain("")
		var tunnelURL string
		if tunnelDomain != "" && branch != "" {
			tunnelURL = "https://" + proxy.RouteKey(project, branch) + "." + tunnelDomain
		}

		if routesJSON {
			return printRoutesJSON(project, branch, routes, tunnelURL)
		}

		printRoutesHuman(project, branch, routes, tunnelURL)
		return nil
	},
}

// RouteEntry pairs a port with its resolved URL.
type RouteEntry struct {
	Port int    `json:"port"`
	URL  string `json:"url"`
}

func buildRoutes(ports []int, project, branch, domain string, routerPort int, svcRunning, pfConfigured bool) []RouteEntry {
	routes := make([]RouteEntry, len(ports))
	for i, p := range ports {
		routes[i] = RouteEntry{
			Port: p,
			URL:  proxy.BuildRouterURL(p, project, branch, domain, routerPort, svcRunning, pfConfigured),
		}
	}
	return routes
}

func printRoutesHuman(project, branch string, routes []RouteEntry, tunnelURL string) {
	label := project
	if branch != "" {
		label += " (" + branch + ")"
	}
	fmt.Printf("%s:\n", label)

	for _, r := range routes {
		fmt.Printf("  %-50s (port %d)\n", r.URL, r.Port)
	}

	if tunnelURL != "" {
		fmt.Printf("  tunnel: %s\n", tunnelURL)
	}
}

func printRoutesJSON(project, branch string, routes []RouteEntry, tunnelURL string) error {
	out := map[string]any{
		"project": project,
		"branch":  branch,
		"routes":  routes,
	}
	if tunnelURL != "" {
		out["tunnel"] = tunnelURL
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding routes: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
