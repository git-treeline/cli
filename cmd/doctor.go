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

		doctorConfig(pc, det, absPath)
		doctorProjectDrift(absPath)
		doctorPortConfig()
		doctorAllocation(absPath)
		doctorRuntime(absPath, pc)
		doctorServe()
		doctorDiagnostics(det)
		doctorRequestFlow(absPath, pc)
		if doctorFix {
			return doctorAutoFix(absPath, pc)
		}
		return nil
	},
}

func doctorJSONOutput(pc *config.ProjectConfig, det *detect.Result, absPath string) error {
	result := map[string]any{}

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
	result["config"] = cfgInfo

	if drift := doctorProjectDriftJSON(absPath); drift != nil {
		result["project_drift"] = drift
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
	result["allocation"] = allocInfo

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
	result["runtime"] = rt

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
	result["serve"] = serveInfo

	diags := templates.Diagnose(det)
	if len(diags) > 0 {
		diagList := make([]map[string]string, 0, len(diags))
		for _, d := range diags {
			diagList = append(diagList, map[string]string{
				"level":   d.Level,
				"message": d.Message,
			})
		}
		result["diagnostics"] = diagList
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

// classifyPortConfig checks whether a port base conflicts with the router port
// or is a well-known framework default that should stay free.
// Returns "conflict", "common_dev_port", or "" (ok).
func classifyPortConfig(base, routerPort int) string {
	if base == routerPort {
		return "conflict"
	}
	if allocator.IsCommonDevPort(base) {
		return "common_dev_port"
	}
	return ""
}

func doctorPortConfig() {
	uc := config.LoadUserConfig("")
	base := uc.PortBase()
	routerPort := uc.RouterPort()

	switch classifyPortConfig(base, routerPort) {
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
func doctorRequestFlow(absPath string, pc *config.ProjectConfig) {
	fmt.Println("\nRequest flow")

	step := firstFailingStep(absPath, pc)
	if step == nil {
		doctorLine("Status", "✓ all links healthy")
		return
	}
	doctorLine("Blocked at", "✗ "+step.label)
	if step.detail != "" {
		fmt.Printf("    %s\n", step.detail)
	}
	if step.fix != "" {
		fmt.Printf("    fix: %s\n", step.fix)
	}
}

type flowStep struct {
	label  string
	detail string
	fix    string
}

// flowInput is the snapshot of state evaluateRequestFlow examines. Extracted
// so the orchestration logic can be tested without standing up a real
// router / launchd / pf / CA.
type flowInput struct {
	allocatedPorts  []int
	appListening    bool
	startCommand    string
	serviceChecks   []service.HealthCheck
	caInstalled     bool
}

func firstFailingStep(absPath string, pc *config.ProjectConfig) *flowStep {
	uc := config.LoadUserConfig("")
	reg := registry.New("")
	alloc := reg.Find(absPath)

	in := flowInput{
		startCommand:  pc.StartCommand(),
		serviceChecks: service.CheckHealth(uc.RouterPort(), Version),
		caInstalled:   proxy.IsCAInstalled(),
	}
	if alloc != nil {
		in.allocatedPorts = format.GetPorts(format.Allocation(alloc))
		if len(in.allocatedPorts) > 0 {
			in.appListening = allocator.CheckPortsListening(in.allocatedPorts)
		}
	}
	return evaluateRequestFlow(in)
}

func evaluateRequestFlow(in flowInput) *flowStep {
	// 1. App listening on its allocated port?
	if len(in.allocatedPorts) > 0 && !in.appListening {
		fix := "start the dev server"
		if in.startCommand != "" {
			fix = in.startCommand
		}
		return &flowStep{
			label:  fmt.Sprintf("app on :%d", in.allocatedPorts[0]),
			detail: "the dev server is not listening — the router has nowhere to forward to",
			fix:    fix,
		}
	}

	// 2. Router service registered + listening + responding.
	for _, c := range in.serviceChecks {
		if c.Name == "port_forwarding" {
			continue // handled below
		}
		if c.Status == "error" || (c.Status == "warn" && c.Name == "router_responding") {
			return &flowStep{
				label:  c.Name,
				detail: c.Detail,
				fix:    c.Fix,
			}
		}
	}
	// router_version mismatch isn't a hard block but is the most likely
	// surprise after a brew upgrade — call it out specifically.
	for _, c := range in.serviceChecks {
		if c.Name == "router_version" && c.Status == "warn" {
			return &flowStep{
				label:  "router_version",
				detail: c.Detail,
				fix:    "gtl serve restart",
			}
		}
	}

	// 3. Port forwarding loaded in kernel.
	for _, c := range in.serviceChecks {
		if c.Name == "port_forwarding" && c.Status != "ok" {
			return &flowStep{
				label:  "port_forwarding",
				detail: c.Detail,
				fix:    c.Fix,
			}
		}
	}

	// 4. CA cert installed and not expired (browser will warn otherwise,
	// but the request still reaches the router — soft warning only).
	if !in.caInstalled {
		return &flowStep{
			label:  "ca_cert",
			detail: "CA not installed — browsers will reject HTTPS",
			fix:    "gtl serve install",
		}
	}
	return nil
}

func doctorServe() {
	uc := config.LoadUserConfig("")
	port := uc.RouterPort()

	fmt.Println("\nServe")

	displayNames := map[string]string{
		"service":           "Service",
		"binary":            "Binary",
		"router_version":    "Router version",
		"router_port":       "Router port",
		"router_responding": "Router responding",
		"port_forwarding":   "Port forwarding",
		"pf_reload_daemon":  "pf reboot survival",
	}

	checks := service.CheckHealth(port, Version)
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

// fixAction names a remediation that doctor --fix can apply automatically.
type fixAction string

const (
	fixServeRestart fixAction = "gtl serve restart"
	fixReloadPF     fixAction = "gtl serve reload-pf"
	fixPrune        fixAction = "gtl prune"
)

// planAutoFix maps the doctor's findings to a deduplicated, ordered list of
// actions to run. Pure function for testability — the caller actually
// executes the actions.
func planAutoFix(in flowInput, registryHasOrphans bool) []fixAction {
	seen := map[fixAction]bool{}
	var plan []fixAction
	add := func(a fixAction) {
		if !seen[a] {
			seen[a] = true
			plan = append(plan, a)
		}
	}

	// First failing link in the request flow drives most fixes.
	step := evaluateRequestFlow(in)
	if step != nil {
		switch step.fix {
		case "gtl serve restart":
			add(fixServeRestart)
		case "gtl serve reload-pf":
			add(fixReloadPF)
		}
	}
	// Independent of the flow chain: a stale router-version warning, even if
	// the router is responding, deserves a restart.
	for _, c := range in.serviceChecks {
		if c.Name == "router_version" && c.Status == "warn" {
			add(fixServeRestart)
		}
	}
	// Registry orphans (entries whose worktree directory is gone) are safe
	// to prune.
	if registryHasOrphans {
		add(fixPrune)
	}
	return plan
}

func doctorAutoFix(absPath string, pc *config.ProjectConfig) error {
	in := buildFlowInput(absPath, pc)
	plan := planAutoFix(in, shouldUseRegistryRepair())

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

func buildFlowInput(absPath string, pc *config.ProjectConfig) flowInput {
	uc := config.LoadUserConfig("")
	reg := registry.New("")
	alloc := reg.Find(absPath)
	in := flowInput{
		startCommand:  pc.StartCommand(),
		serviceChecks: service.CheckHealth(uc.RouterPort(), Version),
		caInstalled:   proxy.IsCAInstalled(),
	}
	if alloc != nil {
		in.allocatedPorts = format.GetPorts(format.Allocation(alloc))
		if len(in.allocatedPorts) > 0 {
			in.appListening = allocator.CheckPortsListening(in.allocatedPorts)
		}
	}
	return in
}

// runAutoFixFn is the indirection point for tests — replaces the real
// remediation actions with no-ops.
var runAutoFixFn = runAutoFixDefault

func runAutoFix(a fixAction) error { return runAutoFixFn(a) }

func runAutoFixDefault(a fixAction) error {
	switch a {
	case fixServeRestart:
		return service.Bounce(service.DefaultBounceTimeout)
	case fixReloadPF:
		return service.ReloadPortForward()
	case fixPrune:
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
