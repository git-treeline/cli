package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-treeline/git-treeline/internal/allocator"
	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/detect"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/proxy"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/service"
	"github.com/git-treeline/git-treeline/internal/supervisor"
	"github.com/git-treeline/git-treeline/internal/templates"
	"github.com/git-treeline/git-treeline/internal/worktree"
	"github.com/spf13/cobra"
)

var doctorJSON bool

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output as JSON")
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
		mainRepo := worktree.DetectMainRepo(absPath)
		det := detect.Detect(absPath)
		pc := config.LoadProjectConfig(mainRepo)

		if doctorJSON {
			return doctorJSONOutput(pc, det, absPath)
		}

		doctorConfig(pc, det, absPath)
		doctorAllocation(absPath)
		doctorRuntime(absPath)
		doctorServe()
		doctorDiagnostics(det)

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
	checks := service.CheckHealth(servePort)
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

	data, _ := json.MarshalIndent(result, "", "  ")
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

	if sc := pc.StartCommand(); sc != "" {
		doctorLine("commands.start", fmt.Sprintf("ok (%s)", sc))
	} else {
		doctorLine("commands.start", "not configured")
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

func doctorRuntime(absPath string) {
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

func doctorServe() {
	uc := config.LoadUserConfig("")
	port := uc.RouterPort()

	fmt.Println("\nServe")

	displayNames := map[string]string{
		"service":         "Service",
		"binary":          "Binary",
		"router_port":     "Router port",
		"port_forwarding": "Port forwarding",
	}

	checks := service.CheckHealth(port)
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
			fmt.Println("  fix: gtl serve uninstall && gtl serve install")
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

func doctorLine(label, value string) {
	const width = 30
	dots := width - len(label)
	if dots < 2 {
		dots = 2
	}
	fmt.Printf("  %s %s %s\n", label, strings.Repeat(".", dots), value)
}
