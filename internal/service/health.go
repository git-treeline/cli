package service

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

type processInfo struct {
	Name string
	PID  int
}

type healthDeps struct {
	isRunning               func() bool
	installedBinaryPath     func() string
	runningRouterVersion    func() string
	runningPID              func() int
	isPortForwardConfigured func() bool
	checkPortForward        func(routerPort int) PortForwardStatus
	dialTimeout             func(network, address string, timeout time.Duration) (net.Conn, error)
	httpProbe               func(url string, timeout time.Duration) (int, error)
	executable              func() (string, error)
	processOnPort           func(port int) processInfo
}

func defaultHealthDeps() healthDeps {
	return healthDeps{
		isRunning:               IsRunning,
		installedBinaryPath:     InstalledBinaryPath,
		runningRouterVersion:    RunningRouterVersion,
		runningPID:              RunningPID,
		isPortForwardConfigured: IsPortForwardConfigured,
		checkPortForward:        CheckPortForward,
		dialTimeout:             net.DialTimeout,
		httpProbe:               httpProbe,
		executable:              os.Executable,
		processOnPort:           processOnPort,
	}
}

// CheckHealth runs all serve-related health checks and returns the results.
func CheckHealth(routerPort int, cliVersion string) []HealthCheck {
	return checkHealthWith(defaultHealthDeps(), routerPort, cliVersion)
}

func checkHealthWith(d healthDeps, routerPort int, cliVersion string) []HealthCheck {
	var checks []HealthCheck

	checks = append(checks, checkServiceRegistered(d))
	checks = append(checks, checkBinaryMatch(d))
	checks = append(checks, checkRouterVersion(d, cliVersion))
	checks = append(checks, checkRouterListening(d, routerPort))
	checks = append(checks, checkRouterResponding(d, routerPort))
	checks = append(checks, checkPortForward(d, routerPort))

	return checks
}

func checkServiceRegistered(d healthDeps) HealthCheck {
	if d.isRunning() {
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

func checkBinaryMatch(d healthDeps) HealthCheck {
	installed := d.installedBinaryPath()
	if installed == "" {
		return HealthCheck{
			Name:   "binary",
			Status: "warn",
			Detail: "no service definition found",
			Fix:    "gtl serve install",
		}
	}

	current, err := d.executable()
	if err != nil {
		return HealthCheck{
			Name:   "binary",
			Status: "warn",
			Detail: "could not resolve current executable",
		}
	}

	currentCmp, installedCmp := current, installed
	if r, err := filepath.EvalSymlinks(current); err == nil {
		currentCmp = r
	}
	if r, err := filepath.EvalSymlinks(installed); err == nil {
		installedCmp = r
	}
	if currentCmp == installedCmp {
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
		Fix:    "gtl serve install",
	}
}

func checkRouterVersion(d healthDeps, cliVersion string) HealthCheck {
	running := d.runningRouterVersion()
	if running == "" {
		return HealthCheck{
			Name:   "router_version",
			Status: "warn",
			Detail: "no version file (router may predate version tracking)",
			Fix:    "gtl serve install",
		}
	}
	if running == cliVersion {
		return HealthCheck{
			Name:   "router_version",
			Status: "ok",
			Detail: running,
		}
	}
	return HealthCheck{
		Name:   "router_version",
		Status: "warn",
		Detail: fmt.Sprintf("router=%s, cli=%s", running, cliVersion),
		Fix:    "gtl serve install",
	}
}

func checkRouterListening(d healthDeps, port int) HealthCheck {
	conn, err := d.dialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		if d.isRunning() {
			return HealthCheck{
				Name:   "router_port",
				Status: "error",
				Detail: fmt.Sprintf("service registered but port %d not listening", port),
				Fix:    "gtl serve restart",
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

	// Compare the listener's PID with the PID launchd/systemd has registered
	// for our service. If they match, this is our router — full stop.
	// If they differ, something else holds the port (or our service crashed
	// and a stale process is squatting). The previous check substring-
	// matched the process name against "gtl", which produced false alarms
	// when the binary was named "git-treeline" — fixed.
	listener := d.processOnPort(port)
	registered := d.runningPID()
	if listener.Name == "" {
		// Couldn't read from lsof; fall back to "ok" since we did dial.
		return HealthCheck{
			Name:   "router_port",
			Status: "ok",
			Detail: fmt.Sprintf("listening on %d", port),
		}
	}
	if registered > 0 && listener.PID == registered {
		return HealthCheck{
			Name:   "router_port",
			Status: "ok",
			Detail: fmt.Sprintf("listening on %d (pid %d)", port, listener.PID),
		}
	}
	if registered > 0 && listener.PID != registered {
		return HealthCheck{
			Name:   "router_port",
			Status: "warn",
			Detail: fmt.Sprintf("port %d occupied by %s (pid %d), but launchd has %d for our service",
				port, listener.Name, listener.PID, registered),
			Fix: "gtl serve restart",
		}
	}
	// registered == 0 means we couldn't read launchd's PID — fall back to a
	// loose name check rather than crying wolf.
	if !looksLikeRouter(listener.Name) {
		return HealthCheck{
			Name:   "router_port",
			Status: "warn",
			Detail: fmt.Sprintf("port %d occupied by %s (pid %d) — does not look like our router", port, listener.Name, listener.PID),
			Fix:    "investigate the process, then 'gtl serve install'",
		}
	}
	return HealthCheck{
		Name:   "router_port",
		Status: "ok",
		Detail: fmt.Sprintf("listening on %d (pid %d)", port, listener.PID),
	}
}

// checkRouterResponding does an end-to-end liveness probe: a real HTTP
// request to the router's health endpoint. A listening socket is necessary
// but not sufficient — the router could be deadlocked or panicking on
// every request. Treats any 2xx/3xx/4xx as alive (the router answered).
// 5xx or transport error → warn.
func checkRouterResponding(d healthDeps, port int) HealthCheck {
	url := fmt.Sprintf("http://127.0.0.1:%d/_treeline/health", port)
	status, err := d.httpProbe(url, 2*time.Second)
	if err != nil {
		return HealthCheck{
			Name:   "router_responding",
			Status: "warn",
			Detail: fmt.Sprintf("liveness probe failed: %v", err),
			Fix:    "gtl serve restart",
		}
	}
	if status >= 500 {
		return HealthCheck{
			Name:   "router_responding",
			Status: "warn",
			Detail: fmt.Sprintf("router answered with %d", status),
			Fix:    "gtl serve restart",
		}
	}
	return HealthCheck{
		Name:   "router_responding",
		Status: "ok",
		Detail: fmt.Sprintf("HTTP %d from /_treeline/health", status),
	}
}

func checkPortForward(d healthDeps, routerPort int) HealthCheck {
	st := d.checkPortForward(routerPort)
	if !st.ConfiguredOnDisk {
		return HealthCheck{
			Name:   "port_forwarding",
			Status: "warn",
			Detail: "443 → router not configured",
			Fix:    "gtl serve install",
		}
	}
	// pf disabled is a definite failure — rules can't apply. The "couldn't
	// even read pf state" case falls through to a port 443 dial below as
	// the authoritative signal. Linux ignores PfStateKnown (it's macOS-
	// specific) but its branch sets KernelStateKnown=true on success.
	if st.PfStateKnown && !st.PfEnabled {
		return HealthCheck{
			Name:   "port_forwarding",
			Status: "error",
			Detail: st.Detail,
			Fix:    "gtl serve reload-pf",
		}
	}
	// We know pf is enabled (or its state is unknown) — verify port 443
	// actually accepts connections. This dial is the most reliable signal
	// when we couldn't read the kernel ruleset without sudo.
	conn, err := d.dialTimeout("tcp", "127.0.0.1:443", 2*time.Second)
	if err != nil {
		// Port 443 not reachable. If we read the kernel ruleset and it
		// didn't show our rule, the diagnosis is "rule missing"; otherwise
		// it's "loaded but unreachable" — both fix with reload-pf.
		detail := "rule loaded but port 443 not reachable"
		if st.KernelStateKnown && !st.LoadedInKernel {
			detail = st.Detail
		}
		return HealthCheck{
			Name:   "port_forwarding",
			Status: "error",
			Detail: detail,
			Fix:    "gtl serve reload-pf",
		}
	}
	_ = conn.Close()
	if !st.KernelStateKnown {
		return HealthCheck{
			Name:   "port_forwarding",
			Status: "ok",
			Detail: fmt.Sprintf("443 → %d (kernel state not readable without sudo, but the port answers)", routerPort),
		}
	}
	return HealthCheck{
		Name:   "port_forwarding",
		Status: "ok",
		Detail: fmt.Sprintf("443 → %d (loaded in kernel)", routerPort),
	}
}

// looksLikeRouter is used as a soft fallback when we couldn't read launchd's
// PID (e.g. on Linux when the test environment lacks systemctl). Treats any
// process name containing "gtl", "git-treeline", or "treeline" as plausibly
// ours. The previous code only matched "gtl", which produced false alarms
// for the "git-treeline" binary.
func looksLikeRouter(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "gtl") ||
		strings.Contains(lower, "git-treeline") ||
		strings.Contains(lower, "treeline")
}

// processOnPort returns name + PID of the process listening on the given
// TCP port, or zero processInfo if it can't be determined.
func processOnPort(port int) processInfo {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return processInfo{}
	}
	out, err := exec.Command("lsof", "-i", fmt.Sprintf("TCP:%d", port),
		"-sTCP:LISTEN", "-n", "-P", "-F", "cn").Output()
	if err != nil {
		return processInfo{}
	}

	var info processInfo
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "c") {
			info.Name = line[1:]
		}
		if strings.HasPrefix(line, "p") {
			var pid int
			if _, err := fmt.Sscanf(line[1:], "%d", &pid); err == nil {
				info.PID = pid
			}
		}
	}
	return info
}

// httpProbe is the default HTTP liveness implementation. Returns the HTTP
// status code, or an error if the request couldn't complete.
func httpProbe(url string, timeout time.Duration) (int, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
