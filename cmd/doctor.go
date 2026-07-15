package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/detect"
	"github.com/git-treeline/cli/internal/doctor"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/git-treeline/cli/internal/templates"
	"github.com/spf13/cobra"
)

var (
	doctorJSON bool
	doctorFix  bool
)

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output as JSON")
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "Run auto-remediation for detected issues (router restart, pf reload, registry prune)")
	rootCmd.AddCommand(doctorCmd)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check project config, allocation, and runtime health",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absPath, _ := filepath.Abs(cwd)
		det := detect.Detect(absPath)
		// Load from worktree (not mainRepo) so branch-specific config is respected
		pc := config.LoadProjectConfig(absPath)

		if doctorJSON {
			return doctorJSONOutput(pc, det, absPath)
		}

		// One health snapshot for the whole run. The Serve section, the
		// request-flow diagnosis, and auto-fix must agree with each other;
		// re-probing per section produced self-contradicting output when a
		// check flapped between samples.
		serveChecks := service.CheckHealth(config.LoadUserConfig("").RouterPort(), Version)

		// Section the output so a machine-level investigation isn't muddled
		// by cwd-specific project state (and vice versa). Machine health —
		// the serve daemon, router, loopback, port forwarding — is identical
		// no matter which directory you run doctor from. Project health is
		// specific to this worktree's config and allocation.
		hasProject := pc.Exists() || registry.New("").Find(absPath) != nil

		fmt.Println("MACHINE  (serve & router — same from any directory)")
		doctorServe(serveChecks)
		doctorPortConfig()

		fmt.Println("\nPROJECT  (this directory)")
		if !hasProject {
			doctorLine("Status", "no .treeline.yml or allocation here — nothing project-specific to check")
			fmt.Println("  (the machine health above is unaffected by the current directory)")
			if doctorFix {
				return doctorAutoFix(absPath, pc, serveChecks)
			}
			return nil
		}
		doctorConfig(pc, det, absPath)
		doctorProjectDrift(absPath)
		doctorAllocation(absPath)
		doctorRuntime(absPath, pc)
		doctorDiagnostics(det)
		doctorRequestFlow(absPath, pc, serveChecks)
		if doctorFix {
			return doctorAutoFix(absPath, pc, serveChecks)
		}
		return nil
	},
}

func doctorJSONOutput(pc *config.ProjectConfig, det *detect.Result, absPath string) error {
	// Two top-level sections mirror the human output: machine-level health
	// (serve/router — identical from any directory) vs project-level state
	// (config/allocation/runtime — specific to this worktree).
	machine := map[string]any{}
	project := map[string]any{}
	result := map[string]any{
		"machine": machine,
		"project": project,
	}

	cfgInfo := map[string]any{}
	if pc.Exists() {
		cfgInfo["treeline_yml"] = "ok"
		cfgInfo["project"] = pc.Project()
		if fw := det.Framework; fw != "" && fw != "unknown" {
			cfgInfo["framework"] = fw
		}
		cfgInfo["env_file"] = pc.EnvFileTarget()
		cfgInfo["start_command"] = pc.StartCommand()
	} else {
		cfgInfo["treeline_yml"] = "missing"
	}
	project["config"] = cfgInfo

	if drift := doctorProjectDriftJSON(absPath); drift != nil {
		project["project_drift"] = drift
	}

	reg := registry.New("")
	alloc := reg.Find(absPath)
	allocInfo := map[string]any{}
	if alloc != nil {
		fa := format.Allocation(alloc)
		allocInfo["ports"] = format.GetPorts(fa)
		allocInfo["database"] = format.GetStr(fa, "database")
		if links := reg.GetLinks(absPath); len(links) > 0 {
			allocInfo["links"] = links
		}
	} else {
		allocInfo["status"] = "not allocated"
	}
	project["allocation"] = allocInfo

	rt := map[string]any{}
	if alloc != nil {
		fa := format.Allocation(alloc)
		ports := format.GetPorts(fa)
		if len(ports) > 0 {
			rt["listening"] = allocator.CheckPortsListening(ports)
		}
	}
	sockPath := supervisor.SocketPath(absPath)
	if resp, err := supervisor.Send(sockPath, "status"); err == nil {
		rt["supervisor"] = resp
	} else {
		rt["supervisor"] = "not running"
	}
	project["runtime"] = rt

	uc := config.LoadUserConfig("")
	servePort := uc.RouterPort()
	checks := service.CheckHealth(servePort, Version)
	serveInfo := map[string]any{}
	for _, c := range checks {
		entry := map[string]any{"status": c.Status, "detail": c.Detail}
		if c.Fix != "" {
			entry["fix"] = c.Fix
		}
		serveInfo[c.Name] = entry
	}
	if proxy.IsCAInstalled() {
		expiry, err := proxy.CACertExpiry()
		if err != nil {
			serveInfo["ca_cert"] = map[string]any{"status": "warn", "detail": err.Error()}
		} else if time.Now().After(expiry) {
			serveInfo["ca_cert"] = map[string]any{"status": "error", "detail": "expired", "expires": expiry.Format(time.RFC3339)}
		} else {
			serveInfo["ca_cert"] = map[string]any{"status": "ok", "expires": expiry.Format(time.RFC3339)}
		}
	} else {
		serveInfo["ca_cert"] = map[string]any{"status": "not_installed"}
	}
	machine["serve"] = serveInfo

	diags := templates.Diagnose(det)
	if len(diags) > 0 {
		diagList := make([]map[string]string, 0, len(diags))
		for _, d := range diags {
			diagList = append(diagList, map[string]string{
				"level":   d.Level,
				"message": d.Message,
			})
		}
		project["diagnostics"] = diagList
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding doctor output: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func doctorConfig(pc *config.ProjectConfig, det *detect.Result, absPath string) {
	fmt.Println("Config")

	if !pc.Exists() {
		doctorLine(".treeline.yml", "missing — run gtl init")
		doctorLine("env_file", "N/A")
		doctorLine("commands.start", "N/A")
		return
	}

	fw := det.Framework
	label := pc.Project()
	if fw != "" && fw != "unknown" {
		label += ", " + fw
	}
	doctorLine(".treeline.yml", fmt.Sprintf("ok (%s)", label))

	target := pc.EnvFileTarget()
	targetPath := filepath.Join(absPath, target)
	if _, err := os.Stat(targetPath); err == nil {
		doctorLine("env_file", fmt.Sprintf("ok (%s)", target))
	} else {
		doctorLine("env_file", fmt.Sprintf("configured (%s) but file missing on disk", target))
	}

	sc := pc.StartCommand()
	if sc != "" {
		doctorLine("commands.start", fmt.Sprintf("ok (%s)", sc))
	} else {
		doctorLine("commands.start", "not configured")
	}

	if sc != "" && !strings.Contains(sc, "{port}") {
		switch det.Framework {
		case "vite":
			doctorLine("port wiring", "⚠ Vite ignores PORT env — add {port} to commands.start")
		case "phoenix":
			doctorLine("port wiring", "⚠ Phoenix needs PORT in the command — use PORT={port} mix phx.server")
		case "django", "python":
			if !strings.Contains(sc, "$PORT") && !strings.Contains(sc, "${PORT") {
				doctorLine("port wiring", "⚠ Django needs the port in the command — use {port}")
			}
		}
	}
}

func doctorPortConfig() {
	uc := config.LoadUserConfig("")
	base := uc.PortBase()
	routerPort := uc.RouterPort()

	switch doctor.ClassifyPortConfig(base, routerPort) {
	case "conflict":
		fmt.Println("\nPort config")
		doctorLine("port.base", fmt.Sprintf("✗ %d conflicts with router.port", base))
		fmt.Println("  The router listens on this port to proxy traffic to your worktrees.")
		fmt.Println("  Allocating worktrees here will prevent the router from starting.")
		fmt.Printf("  Fix: gtl config set port.base %d\n", routerPort+1)
	case "common_dev_port":
		fmt.Println("\nPort config")
		doctorLine("port.base", fmt.Sprintf("⚠ %d is a common framework default", base))
		fmt.Println()
		fmt.Println("  Port 3000 should stay free for the proxy. Third-party services")
		fmt.Println("  (OAuth, Mapbox, Stripe) whitelist localhost:3000 as their origin.")
		fmt.Println("  The proxy can sit on 3000 and forward to any branch transparently —")
		fmt.Println("  but only if no worktree has claimed the port.")
		fmt.Println()
		fmt.Printf("  Port %d is reserved for the router (proxy listener).\n", routerPort)
		fmt.Println()
		fmt.Println("  The default base is 3002 — the first port after the reserved range.")
		fmt.Println("  Fix: gtl config set port.base 3002")
		fmt.Println()
		fmt.Println("  See: https://git-treeline.dev/docs/port-preservation")
	}
}

func doctorAllocation(absPath string) {
	fmt.Println("\nAllocation")

	reg := registry.New("")
	alloc := reg.Find(absPath)
	if alloc == nil {
		doctorLine("Status", "none — run gtl setup")
		return
	}

	fa := format.Allocation(alloc)
	ports := format.GetPorts(fa)
	if len(ports) > 0 {
		doctorLine(fmt.Sprintf("Port %s", format.JoinInts(ports, ", ")), "allocated")
	}
	if db := format.GetStr(fa, "database"); db != "" {
		doctorLine("Database", db)
	} else {
		doctorLine("Database", "not configured")
	}

	links := reg.GetLinks(absPath)
	if len(links) > 0 {
		for proj, branch := range links {
			doctorLine(fmt.Sprintf("Link: %s", proj), branch)
		}
	}
}

func doctorRuntime(absPath string, pc *config.ProjectConfig) {
	fmt.Println("\nRuntime")

	reg := registry.New("")
	alloc := reg.Find(absPath)
	if alloc != nil {
		fa := format.Allocation(alloc)
		ports := format.GetPorts(fa)
		if len(ports) > 0 {
			if allocator.CheckPortsListening(ports) {
				doctorLine(fmt.Sprintf("Port %d", ports[0]), "listening")
			} else {
				doctorLine(fmt.Sprintf("Port %d", ports[0]), "not listening")
				if sc := pc.StartCommand(); sc != "" {
					fmt.Printf("  fix: %s\n", sc)
				}
			}
		}
	}

	sockPath := supervisor.SocketPath(absPath)
	resp, err := supervisor.Send(sockPath, "status")
	if err == nil {
		doctorLine("Supervisor", resp)
	} else {
		doctorLine("Supervisor", "not running")
	}
}

// doctorRequestFlow walks the request chain a worktree URL takes (app port
// → router → router has the route → port forwarding → CA) and surfaces the
// FIRST failing link as the actionable diagnosis. Everything else is noise
// when the first link is broken.
func doctorRequestFlow(absPath string, pc *config.ProjectConfig, serveChecks []service.HealthCheck) {
	fmt.Println("\nRequest flow")

	step := doctor.EvaluateRequestFlow(buildFlowInput(absPath, pc, serveChecks))
	if step == nil {
		doctorLine("Status", "✓ all links healthy")
		return
	}
	doctorLine("Blocked at", "✗ "+step.Label)
	if step.Detail != "" {
		fmt.Printf("    %s\n", step.Detail)
	}
	if step.Fix != "" {
		fmt.Printf("    fix: %s\n", step.Fix)
	}
}

func doctorServe(checks []service.HealthCheck) {
	fmt.Println("\nServe")

	displayNames := map[string]string{
		"loopback":          "Loopback",
		"service":           "Service",
		"binary":            "Binary",
		"router_version":    "Router version",
		"router_port":       "Router port",
		"router_responding": "Router responding",
		"port_forwarding":   "Port forwarding",
		"pf_reload_daemon":  "pf reboot survival",
	}

	for _, c := range checks {
		label := displayNames[c.Name]
		if label == "" {
			label = c.Name
		}
		switch c.Status {
		case "ok":
			doctorLine(label, c.Detail)
		case "warn":
			doctorLine(label, "⚠ "+c.Detail)
			if c.Fix != "" {
				fmt.Printf("  fix: %s\n", c.Fix)
			}
		case "error":
			doctorLine(label, "✗ "+c.Detail)
			if c.Fix != "" {
				fmt.Printf("  fix: %s\n", c.Fix)
			}
		}
	}

	if proxy.IsCAInstalled() {
		expiry, err := proxy.CACertExpiry()
		if err != nil {
			doctorLine("CA cert", "⚠ could not read: "+err.Error())
		} else if time.Now().After(expiry) {
			doctorLine("CA cert", "✗ expired on "+expiry.Format("2006-01-02"))
			fmt.Println("  fix: gtl serve install")
		} else {
			doctorLine("CA cert", "ok (expires "+expiry.Format("2006-01-02")+")")
		}
	} else {
		doctorLine("CA cert", "not installed")
	}
}

func doctorDiagnostics(det *detect.Result) {
	diags := templates.Diagnose(det)
	if len(diags) == 0 {
		return
	}

	fmt.Println("\nDiagnostics")
	for _, d := range diags {
		prefix := "  "
		if d.Level == "warn" {
			prefix = "  Warning: "
		}
		for i, line := range strings.Split(d.Message, "\n") {
			if i == 0 {
				fmt.Printf("%s%s\n", prefix, line)
			} else {
				fmt.Printf("  %s\n", line)
			}
		}
	}
}

func doctorAutoFix(absPath string, pc *config.ProjectConfig, serveChecks []service.HealthCheck) error {
	in := buildFlowInput(absPath, pc, serveChecks)
	plan := doctor.PlanAutoFix(in, shouldUseRegistryRepair())

	fmt.Println("\nAuto-fix")
	if len(plan) == 0 {
		doctorLine("Status", "nothing to fix automatically")
		return nil
	}

	for _, a := range plan {
		fmt.Printf("  → %s\n", a)
		if err := runAutoFix(a); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %v\n", err)
			return err
		}
		fmt.Println("    done.")
	}
	return nil
}

func buildFlowInput(absPath string, pc *config.ProjectConfig, serveChecks []service.HealthCheck) doctor.FlowInput {
	reg := registry.New("")
	alloc := reg.Find(absPath)
	in := doctor.FlowInput{
		StartCommand:  pc.StartCommand(),
		ServiceChecks: serveChecks,
		CAInstalled:   proxy.IsCAInstalled(),
	}
	if alloc != nil {
		in.AllocatedPorts = format.GetPorts(format.Allocation(alloc))
		if len(in.AllocatedPorts) > 0 {
			in.AppListening = allocator.CheckPortsListening(in.AllocatedPorts)
		}
	}
	return in
}

// runAutoFixFn is the indirection point for tests — replaces the real
// remediation actions with no-ops.
var runAutoFixFn = runAutoFixDefault

func runAutoFix(a doctor.FixAction) error { return runAutoFixFn(a) }

func runAutoFixDefault(a doctor.FixAction) error {
	switch a {
	case doctor.FixServeRestart:
		return service.Bounce(config.LoadUserConfig("").RouterPort(), service.DefaultReadyTimeout)
	case doctor.FixReloadPF:
		return service.ReloadPortForward()
	case doctor.FixPrune:
		_, err := registry.New("").Prune()
		return err
	}
	return fmt.Errorf("unknown fix action: %s", a)
}

func doctorLine(label, value string) {
	const width = 30
	dots := width - len(label)
	if dots < 2 {
		dots = 2
	}
	fmt.Printf("  %s %s %s\n", label, strings.Repeat(".", dots), value)
}
