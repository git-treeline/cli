package service

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/git-treeline/cli/internal/process"
	"github.com/git-treeline/cli/internal/proxy"
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
	isRunning                 func() bool
	installedBinaryPath       func() string
	runningRouterVersion      func() string
	runningPID                func() int
	isPortForwardConfigured   func() bool
	checkPortForward          func(routerPort int) PortForwardStatus
	dialTimeout               func(network, address string, timeout time.Duration) (net.Conn, error)
	httpProbe                 func(url string, timeout time.Duration) (int, error)
	executable                func() (string, error)
	processOnPort             func(port int) processInfo
	isPfReloadDaemonInstalled func() bool
	pfReloadDaemonSupported   bool
	routerUsesTLS             func() bool
	loopbackListen            func(network, address string) (net.Listener, error)
	// Linux boot-time redirect persistence (systemd oneshot). The macOS
	// equivalent is the pf-reload LaunchDaemon above; these are the Linux
	// analog so `gtl doctor` flags a missing persistence unit there too.
	isPortForwardPersistenceInstalled func() bool
	portForwardPersistenceSupported   bool
}

func defaultHealthDeps() healthDeps {
	return healthDeps{
		isRunning:                         IsRunning,
		installedBinaryPath:               InstalledBinaryPath,
		runningRouterVersion:              RunningRouterVersion,
		runningPID:                        RunningPID,
		isPortForwardConfigured:           IsPortForwardConfigured,
		checkPortForward:                  CheckPortForward,
		dialTimeout:                       net.DialTimeout,
		httpProbe:                         httpProbe,
		executable:                        os.Executable,
		processOnPort:                     processOnPort,
		isPfReloadDaemonInstalled:         IsPfReloadDaemonInstalled,
		pfReloadDaemonSupported:           runtime.GOOS == "darwin",
		routerUsesTLS:                     proxy.IsCAInstalled,
		loopbackListen:                    net.Listen,
		isPortForwardPersistenceInstalled: IsLinuxPortForwardPersistenceInstalled,
		portForwardPersistenceSupported:   runtime.GOOS == "linux",
	}
}

// CheckHealth runs all serve-related health checks and returns the results.
func CheckHealth(routerPort int, cliVersion string) []HealthCheck {
	return checkHealthWith(defaultHealthDeps(), routerPort, cliVersion)
}

func checkHealthWith(d healthDeps, routerPort int, cliVersion string) []HealthCheck {
	var checks []HealthCheck

	checks = append(checks, checkLoopback(d))
	checks = append(checks, checkServiceRegistered(d))
	checks = append(checks, checkBinaryMatch(d))
	checks = append(checks, checkRouterVersion(d, cliVersion))
	checks = append(checks, checkRouterListening(d, routerPort))
	checks = append(checks, checkRouterResponding(d, routerPort))
	checks = append(checks, checkPortForward(d, routerPort))
	if d.pfReloadDaemonSupported {
		checks = append(checks, checkPfReloadDaemon(d))
	}
	if d.portForwardPersistenceSupported {
		checks = append(checks, checkLinuxPortForwardPersistence(d))
	}

	return checks
}

// checkLinuxPortForwardPersistence is the Linux analog of checkPfReloadDaemon:
// it flags the absence of the boot-time systemd oneshot that re-applies the
// iptables redirect after a reboot when port forwarding is otherwise
// configured. Linux-only; gated by portForwardPersistenceSupported.
func checkLinuxPortForwardPersistence(d healthDeps) HealthCheck {
	if !d.isPortForwardConfigured() {
		return HealthCheck{
			Name:   "port_forward_persistence",
			Status: "ok",
			Detail: "n/a (port forwarding not configured)",
		}
	}
	if !d.isPortForwardPersistenceInstalled() {
		return HealthCheck{
			Name:   "port_forward_persistence",
			Status: "warn",
			Detail: "missing — 443 redirect will drop on reboot",
			Fix:    "gtl serve install",
		}
	}
	return HealthCheck{
		Name:   "port_forward_persistence",
		Status: "ok",
		Detail: "installed (443 redirect survives reboot)",
	}
}

// checkLoopback binds a throwaway 127.0.0.1 port and immediately dials it.
// If binding or dialing fails, the machine's loopback interface is broken or
// being filtered (a local firewall, VPN kill-switch, or endpoint-security
// agent). That matters because it invalidates every downstream "router
// unreachable" verdict: the router could be perfectly healthy while nothing
// local can reach it. Surfacing this plainly stops doctor from blaming the
// router for a machine-level network policy.
func checkLoopback(d healthDeps) HealthCheck {
	ln, err := d.loopbackListen("tcp", "127.0.0.1:0")
	if err != nil {
		return HealthCheck{
			Name:   "loopback",
			Status: "error",
			Detail: fmt.Sprintf("could not bind a 127.0.0.1 port: %v", err),
			Fix:    "check for a local firewall, VPN, or security agent blocking loopback",
		}
	}
	defer func() { _ = ln.Close() }()

	conn, err := d.dialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		return HealthCheck{
			Name:   "loopback",
			Status: "error",
			Detail: "bound a 127.0.0.1 port but could not connect to it — loopback is being filtered (local firewall, VPN, or security agent)",
			Fix:    "allow loopback traffic; while this is broken every 'router unreachable' result is meaningless",
		}
	}
	_ = conn.Close()
	return HealthCheck{
		Name:   "loopback",
		Status: "ok",
		Detail: "127.0.0.1 accepts local connections",
	}
}

// checkPfReloadDaemon flags the absence of the boot-time pf reloader when
// port forwarding is otherwise configured. Without the daemon, pf rules
// drop on every reboot and the user has to manually run
// `gtl serve reload-pf`. macOS-only.
func checkPfReloadDaemon(d healthDeps) HealthCheck {
	if !d.isPortForwardConfigured() {
		// No pf rules to keep alive across reboots — daemon is irrelevant.
		return HealthCheck{
			Name:   "pf_reload_daemon",
			Status: "ok",
			Detail: "n/a (port forwarding not configured)",
		}
	}
	if !d.isPfReloadDaemonInstalled() {
		return HealthCheck{
			Name:   "pf_reload_daemon",
			Status: "warn",
			Detail: "missing — pf rules will drop on reboot",
			Fix:    "gtl serve install",
		}
	}
	return HealthCheck{
		Name:   "pf_reload_daemon",
		Status: "ok",
		Detail: "installed (pf rules survive reboot)",
	}
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
// every request. The scheme must match the router's: it serves TLS whenever
// the local CA is installed (see runRouter), and a plain-HTTP probe against
// a TLS listener never gets a health response — it either times out or gets
// Go's "sent an HTTP request to an HTTPS server" 400, both of which used to
// misreport a healthy router. The health endpoint returns 200, so anything
// other than 2xx/3xx means something is wrong.
func checkRouterResponding(d healthDeps, port int) HealthCheck {
	scheme := "http"
	if d.routerUsesTLS() {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://127.0.0.1:%d/_treeline/health", scheme, port)
	status, err := d.httpProbe(url, 2*time.Second)
	if err != nil {
		return HealthCheck{
			Name:   "router_responding",
			Status: "warn",
			Detail: fmt.Sprintf("liveness probe failed: %v", err),
			Fix:    "gtl serve restart",
		}
	}
	if status >= 400 {
		return HealthCheck{
			Name:   "router_responding",
			Status: "warn",
			Detail: fmt.Sprintf("router answered %d from /_treeline/health", status),
			Fix:    "gtl serve restart",
		}
	}
	return HealthCheck{
		Name:   "router_responding",
		Status: "ok",
		Detail: fmt.Sprintf("HTTP %d from /_treeline/health", status),
	}
}

// WaitRouterResponding polls the router's health endpoint until it answers,
// or until `wait` elapses. Used after a restart: launchd/systemd reporting
// the service as running (and even the version file being rewritten) only
// proves the process started, not that it is serving requests.
func WaitRouterResponding(routerPort int, wait time.Duration) error {
	d := defaultHealthDeps()
	deadline := nowFn().Add(wait)
	for {
		c := checkRouterResponding(d, routerPort)
		if c.Status == "ok" {
			return nil
		}
		if !nowFn().Before(deadline) {
			return fmt.Errorf("%s", c.Detail)
		}
		sleepFn(200 * time.Millisecond)
	}
}

// routerPortReachable reports whether the router's own listener accepts a
// loopback connection. Used to distinguish "pf isn't forwarding :443" from
// "the router is fine but an external filter is blocking :443 specifically".
func routerPortReachable(d healthDeps, routerPort int) bool {
	conn, err := d.dialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", routerPort), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
		//
		// EXCEPTION: if we positively confirmed the rule IS loaded in the
		// kernel AND the router's own port answers, then pf is doing its
		// job — some external filter (a VPN killswitch, Little Snitch,
		// EncryptMe) is intercepting loopback :443. reload-pf cannot fix
		// that; sending the user there just wastes a sudo round-trip. Point
		// at the real culprit instead. This was the root cause of the
		// incident that motivated the doctor overhaul.
		if st.KernelStateKnown && st.LoadedInKernel && routerPortReachable(d, routerPort) {
			return HealthCheck{
				Name:   "port_forwarding",
				Status: "error",
				Detail: "port 443 is blocked by something outside gtl (VPN killswitch / packet filter) — not a pf rule problem",
				Fix:    "check your firewall — a VPN killswitch, Little Snitch, or EncryptMe is intercepting loopback :443 (the pf rule is loaded and the router is up, so reloading pf rules won't help)",
			}
		}
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
// TCP port, or zero processInfo if it can't be determined. When several
// processes share the port it reports the first listener lsof lists.
func processOnPort(port int) processInfo {
	listeners := process.ListenersOnPort(port)
	if len(listeners) == 0 {
		return processInfo{}
	}
	return processInfo{Name: listeners[0].Name, PID: listeners[0].PID}
}

// httpProbe is the default HTTP liveness implementation. Returns the HTTP
// status code, or an error if the request couldn't complete. Certificate
// verification is skipped: the probe checks liveness, not identity, and the
// router's per-hostname certs are issued for browser use — trust-store state
// must not fail the health check.
func httpProbe(url string, timeout time.Duration) (int, error) {
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
