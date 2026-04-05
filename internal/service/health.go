package service

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// HealthCheck represents a single doctor check result.
type HealthCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok", "warn", "error"
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// CheckHealth runs all serve-related health checks and returns the results.
func CheckHealth(routerPort int) []HealthCheck {
	var checks []HealthCheck

	checks = append(checks, checkServiceRegistered())
	checks = append(checks, checkBinaryMatch())
	checks = append(checks, checkRouterListening(routerPort))
	checks = append(checks, checkPortForward(routerPort))

	return checks
}

func checkServiceRegistered() HealthCheck {
	if IsRunning() {
		return HealthCheck{
			Name:   "service",
			Status: "ok",
			Detail: fmt.Sprintf("registered and running (%s)", LaunchLabel()),
		}
	}
	return HealthCheck{
		Name:   "service",
		Status: "error",
		Detail: "not running",
		Fix:    "gtl serve install",
	}
}

func checkBinaryMatch() HealthCheck {
	installed := InstalledBinaryPath()
	if installed == "" {
		return HealthCheck{
			Name:   "binary",
			Status: "warn",
			Detail: "no service definition found",
			Fix:    "gtl serve install",
		}
	}

	current, err := os.Executable()
	if err != nil {
		return HealthCheck{
			Name:   "binary",
			Status: "warn",
			Detail: "could not resolve current executable",
		}
	}

	if current == installed {
		return HealthCheck{
			Name:   "binary",
			Status: "ok",
			Detail: installed,
		}
	}

	return HealthCheck{
		Name:   "binary",
		Status: "warn",
		Detail: fmt.Sprintf("mismatch: service=%s, current=%s", installed, current),
		Fix:    "gtl serve uninstall && gtl serve install",
	}
}

func checkRouterListening(port int) HealthCheck {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		if IsRunning() {
			return HealthCheck{
				Name:   "router_port",
				Status: "error",
				Detail: fmt.Sprintf("service registered but port %d not listening", port),
				Fix:    "gtl serve uninstall && gtl serve install",
			}
		}
		return HealthCheck{
			Name:   "router_port",
			Status: "error",
			Detail: fmt.Sprintf("port %d not listening", port),
			Fix:    "gtl serve install",
		}
	}
	_ = conn.Close()

	proc := processOnPort(port)
	if proc != "" && !strings.Contains(proc, "gtl") {
		return HealthCheck{
			Name:   "router_port",
			Status: "warn",
			Detail: fmt.Sprintf("port %d occupied by: %s", port, proc),
			Fix:    "kill the rogue process, then gtl serve install",
		}
	}

	return HealthCheck{
		Name:   "router_port",
		Status: "ok",
		Detail: fmt.Sprintf("listening on %d", port),
	}
}

func checkPortForward(routerPort int) HealthCheck {
	if !IsPortForwardConfigured() {
		return HealthCheck{
			Name:   "port_forwarding",
			Status: "warn",
			Detail: "443 → router not configured",
			Fix:    "gtl serve install",
		}
	}

	conn, err := net.DialTimeout("tcp", "127.0.0.1:443", 2*time.Second)
	if err != nil {
		return HealthCheck{
			Name:   "port_forwarding",
			Status: "error",
			Detail: "configured in pf.conf but port 443 not reachable — rules may need reload",
			Fix:    "gtl serve uninstall && gtl serve install",
		}
	}
	_ = conn.Close()

	return HealthCheck{
		Name:   "port_forwarding",
		Status: "ok",
		Detail: fmt.Sprintf("443 → %d", routerPort),
	}
}

// processOnPort returns a description of the process listening on the given
// TCP port, or "" if it can't be determined.
func processOnPort(port int) string {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return ""
	}
	out, err := exec.Command("lsof", "-i", fmt.Sprintf("TCP:%d", port),
		"-sTCP:LISTEN", "-n", "-P", "-F", "cn").Output()
	if err != nil {
		return ""
	}

	var name, pid string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "c") {
			name = line[1:]
		}
		if strings.HasPrefix(line, "p") {
			pid = line[1:]
		}
	}
	if name == "" {
		return ""
	}
	if pid != "" {
		return fmt.Sprintf("%s (pid %s)", name, pid)
	}
	return name
}
