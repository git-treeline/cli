package service

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeConn struct{ net.Conn }

func (f fakeConn) Close() error { return nil }

func fakeDial(succeed bool) func(string, string, time.Duration) (net.Conn, error) {
	return func(_, _ string, _ time.Duration) (net.Conn, error) {
		if succeed {
			return fakeConn{}, nil
		}
		return nil, fmt.Errorf("connection refused")
	}
}

func fakeHTTP(status int, err error) func(string, time.Duration) (int, error) {
	return func(_ string, _ time.Duration) (int, error) {
		return status, err
	}
}

func fakeHTTPBody(status int, body string, err error) func(string, time.Duration) (int, string, error) {
	return func(_ string, _ time.Duration) (int, string, error) {
		return status, body, err
	}
}

func fakePF(configuredOnDisk, loadedInKernel, pfEnabled bool, detail string) func(int) PortForwardStatus {
	// kernelStateKnown defaults to true for legacy callers — most existing
	// tests want "we read the kernel and saw [or didn't see] the rule." Use
	// fakePFKernelUnknown for the without-sudo path.
	return fakePFFull(configuredOnDisk, loadedInKernel, pfEnabled, true, detail)
}

func fakePFKernelUnknown(configuredOnDisk, pfEnabled bool, detail string) func(int) PortForwardStatus {
	// PfStateKnown=true (we read pf state) but KernelStateKnown=false (we
	// couldn't list rules without sudo).
	return fakePFAll(configuredOnDisk, false, pfEnabled, true, false, detail)
}

func fakePFFull(configuredOnDisk, loadedInKernel, pfEnabled, kernelStateKnown bool, detail string) func(int) PortForwardStatus {
	// PfStateKnown defaults to true here — most tests set pfEnabled
	// definitively. fakePFPfUnknown is the helper for the without-sudo case.
	return fakePFAll(configuredOnDisk, loadedInKernel, pfEnabled, true, kernelStateKnown, detail)
}

func fakePFPfUnknown(configuredOnDisk bool, detail string) func(int) PortForwardStatus {
	return fakePFAll(configuredOnDisk, false, false, false, false, detail)
}

func fakePFAll(configuredOnDisk, loadedInKernel, pfEnabled, pfStateKnown, kernelStateKnown bool, detail string) func(int) PortForwardStatus {
	return func(int) PortForwardStatus {
		return PortForwardStatus{
			ConfiguredOnDisk: configuredOnDisk,
			LoadedInKernel:   loadedInKernel,
			PfEnabled:        pfEnabled,
			PfStateKnown:     pfStateKnown,
			KernelStateKnown: kernelStateKnown,
			Detail:           detail,
		}
	}
}

// allHealthy returns a healthDeps where every probe reports the happy path.
// Listener PID matches launchd's recorded PID; pf rules are loaded; the
// router answers the liveness probe with 200.
func allHealthy() healthDeps {
	return healthDeps{
		isRunning:                  func() bool { return true },
		installedBinaryPath:        func() string { return "/usr/local/bin/gtl" },
		runningRouterVersion:       func() string { return "1.0.0" },
		runningPID:                 func() int { return 1234 },
		isPortForwardConfigured:    func() bool { return true },
		checkPortForward:           fakePF(true, true, true, ""),
		dialTimeout:                fakeDial(true),
		httpProbe:                  fakeHTTP(200, nil),
		httpProbeBody:              fakeHTTPBody(200, "ok\n", nil),
		usesPf:                     true,
		executable:                 func() (string, error) { return "/usr/local/bin/gtl", nil },
		processOnPort:              func(int) processInfo { return processInfo{Name: "git-treeline", PID: 1234} },
		isPfReloadDaemonInstalled:  func() bool { return true },
		pfReloadDaemonSupported:    true,
		routerUsesTLS:              func() bool { return false },
		loopbackListen:             net.Listen,
	}
}

// --- checkHealthWith integration ---

func TestCheckHealthWith_AllHealthy(t *testing.T) {
	checks := checkHealthWith(allHealthy(), 8443, "1.0.0")

	if len(checks) != 8 {
		t.Fatalf("expected 8 checks, got %d", len(checks))
	}
	for _, c := range checks {
		if c.Status != "ok" {
			t.Errorf("check %q: expected ok, got %s (%s)", c.Name, c.Status, c.Detail)
		}
	}
}

func TestCheckHealthWith_NoPfReloadDaemonOnLinux(t *testing.T) {
	d := allHealthy()
	d.pfReloadDaemonSupported = false
	checks := checkHealthWith(d, 8443, "1.0.0")
	for _, c := range checks {
		if c.Name == "pf_reload_daemon" {
			t.Errorf("pf_reload_daemon check should not run on non-darwin, got %+v", c)
		}
	}
}

func TestCheckHealthWith_AllBroken(t *testing.T) {
	d := healthDeps{
		isRunning:                  func() bool { return false },
		installedBinaryPath:        func() string { return "" },
		runningRouterVersion:       func() string { return "" },
		runningPID:                 func() int { return 0 },
		isPortForwardConfigured:    func() bool { return false },
		checkPortForward:           fakePF(false, false, false, "not configured"),
		dialTimeout:                fakeDial(false),
		httpProbe:                  fakeHTTP(0, fmt.Errorf("connection refused")),
		executable:                 func() (string, error) { return "/usr/local/bin/gtl", nil },
		processOnPort:              func(int) processInfo { return processInfo{} },
		isPfReloadDaemonInstalled:  func() bool { return false },
		pfReloadDaemonSupported:    true,
		routerUsesTLS:              func() bool { return false },
		loopbackListen:             net.Listen,
	}

	checks := checkHealthWith(d, 8443, "1.0.0")

	expected := map[string]string{
		"loopback":          "error", // binds, but the fake dialer refuses
		"service":           "error",
		"binary":            "warn",
		"router_version":    "warn",
		"router_port":       "error",
		"router_responding": "warn",
		"port_forwarding":   "warn",
		"pf_reload_daemon":  "ok", // pf not configured → daemon is n/a
	}
	for _, c := range checks {
		want, ok := expected[c.Name]
		if !ok {
			t.Errorf("unexpected check %q", c.Name)
			continue
		}
		if c.Status != want {
			t.Errorf("check %q: got %s, want %s", c.Name, c.Status, want)
		}
	}
}

// --- checkLoopback ---

func TestCheckLoopback_Ok(t *testing.T) {
	d := allHealthy() // real net.Listen + fake dial that succeeds
	c := checkLoopback(d)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckLoopback_BindFails(t *testing.T) {
	d := allHealthy()
	d.loopbackListen = func(string, string) (net.Listener, error) {
		return nil, fmt.Errorf("operation not permitted")
	}
	c := checkLoopback(d)
	if c.Status != "error" {
		t.Fatalf("expected error, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "could not bind") {
		t.Errorf("expected bind detail, got %q", c.Detail)
	}
}

func TestCheckLoopback_DialFails(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false) // bound but nothing can connect
	c := checkLoopback(d)
	if c.Status != "error" {
		t.Fatalf("expected error, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "filtered") {
		t.Errorf("expected filtered detail, got %q", c.Detail)
	}
}

// --- checkServiceRegistered ---

func TestCheckServiceRegistered_Running(t *testing.T) {
	d := allHealthy()
	c := checkServiceRegistered(d)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s", c.Status)
	}
	if c.Fix != "" {
		t.Error("expected no fix when running")
	}
}

func TestCheckServiceRegistered_NotRunning(t *testing.T) {
	d := allHealthy()
	d.isRunning = func() bool { return false }
	c := checkServiceRegistered(d)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
	if c.Fix == "" {
		t.Error("expected fix suggestion")
	}
}

// --- checkBinaryMatch ---

func TestCheckBinaryMatch_Match(t *testing.T) {
	d := allHealthy()
	c := checkBinaryMatch(d)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s", c.Status)
	}
}

func TestCheckBinaryMatch_Mismatch(t *testing.T) {
	d := allHealthy()
	d.executable = func() (string, error) { return "/other/path/gtl", nil }
	c := checkBinaryMatch(d)
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
	if c.Fix == "" {
		t.Error("expected fix for mismatch")
	}
}

func TestCheckBinaryMatch_NoServiceDef(t *testing.T) {
	d := allHealthy()
	d.installedBinaryPath = func() string { return "" }
	c := checkBinaryMatch(d)
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}

func TestCheckBinaryMatch_ExecutableError(t *testing.T) {
	d := allHealthy()
	d.executable = func() (string, error) { return "", fmt.Errorf("cannot resolve") }
	c := checkBinaryMatch(d)
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}

// --- checkRouterVersion ---

func TestCheckRouterVersion_Match(t *testing.T) {
	d := allHealthy()
	c := checkRouterVersion(d, "1.0.0")
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s", c.Status)
	}
}

func TestCheckRouterVersion_Mismatch(t *testing.T) {
	d := allHealthy()
	d.runningRouterVersion = func() string { return "0.9.0" }
	c := checkRouterVersion(d, "1.0.0")
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}

func TestCheckRouterVersion_NoVersionFile(t *testing.T) {
	d := allHealthy()
	d.runningRouterVersion = func() string { return "" }
	c := checkRouterVersion(d, "1.0.0")
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}

// --- checkRouterListening ---

func TestCheckRouterListening_Ok_PIDMatches(t *testing.T) {
	d := allHealthy()
	c := checkRouterListening(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "pid 1234") {
		t.Errorf("expected detail to include the listener pid, got %q", c.Detail)
	}
}

func TestCheckRouterListening_NotListening_ServiceRunning(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false)
	d.processOnPort = func(int) processInfo { return processInfo{} }
	c := checkRouterListening(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
}

// macOS pf has been seen dropping direct SYNs to the rdr target port while
// the listener is up (lsof still sees the socket) and rdr'd :443 traffic
// flows. That must not read as "port dead": a restart can't fix it. The pf
// verdict requires the :443 forward to answer — that is what separates a
// pf drop from a wedged router (next test).
func TestCheckRouterListening_DialFailsButForwardAnswers_WarnsPf(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false)
	c := checkRouterListening(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn when lsof sees our listener and 443 answers, got %s (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "packet filter") {
		t.Errorf("expected detail to name the packet filter, got %q", c.Detail)
	}
	if c.Fix == "gtl serve restart" {
		t.Errorf("restart must not be suggested for an OS-level drop, got %q", c.Fix)
	}
}

// Same dial-fails-but-listener-present signature with a dead :443 forward is
// a wedged router (accept loop hung, backlog full) — there a restart IS the
// fix and the check must say so, not blame the packet filter.
func TestCheckRouterListening_DialFailsForwardDead_WedgedError(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false)
	d.httpProbeBody = fakeHTTPBody(0, "", fmt.Errorf("connection timed out"))
	c := checkRouterListening(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error for a wedged listener, got %s (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "wedged") {
		t.Errorf("expected detail to name the wedged process, got %q", c.Detail)
	}
	if c.Fix != "gtl serve restart" {
		t.Errorf("expected restart fix for a wedged router, got %q", c.Fix)
	}
}

func TestCheckRouterListening_DialFailsListenerPIDMismatch_Error(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false)
	d.processOnPort = func(int) processInfo { return processInfo{Name: "nginx", PID: 5678} }
	c := checkRouterListening(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error when the socket isn't ours, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckRouterListening_NotListening_ServiceStopped(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false)
	d.isRunning = func() bool { return false }
	c := checkRouterListening(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
}

// Regression: previously the rogue check substring-matched the process name
// against "gtl"; the binary is named "git-treeline" which does NOT contain
// "gtl" as a substring, so the legitimate router got flagged as rogue.
// With PID-compare, when the listener PID matches launchd's recorded PID,
// it's our router regardless of the name.
func TestCheckRouterListening_GitTreelineNotRogue(t *testing.T) {
	d := allHealthy()
	d.processOnPort = func(int) processInfo { return processInfo{Name: "git-treeline", PID: 1234} }
	d.runningPID = func() int { return 1234 }

	c := checkRouterListening(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok for matching PID, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckRouterListening_PIDMismatch_Warns(t *testing.T) {
	d := allHealthy()
	d.processOnPort = func(int) processInfo { return processInfo{Name: "nginx", PID: 5678} }
	d.runningPID = func() int { return 1234 }

	c := checkRouterListening(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn for PID mismatch, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "5678") || !strings.Contains(c.Detail, "1234") {
		t.Errorf("expected detail to mention both PIDs, got %q", c.Detail)
	}
	if c.Fix != "gtl serve restart" {
		t.Errorf("expected fix to be 'gtl serve restart', got %q", c.Fix)
	}
}

func TestCheckRouterListening_FallbackWhenLaunchdPIDUnknown(t *testing.T) {
	// runningPID returns 0 → couldn't read launchd → fall back to a soft
	// name check. Both "gtl" and "git-treeline" should pass.
	for _, name := range []string{"gtl", "git-treeline"} {
		d := allHealthy()
		d.runningPID = func() int { return 0 }
		d.processOnPort = func(int) processInfo { return processInfo{Name: name, PID: 9999} }
		c := checkRouterListening(d, 8443)
		if c.Status != "ok" {
			t.Errorf("name=%q: expected ok, got %s (%s)", name, c.Status, c.Detail)
		}
	}

	// Something genuinely foreign should still warn.
	d := allHealthy()
	d.runningPID = func() int { return 0 }
	d.processOnPort = func(int) processInfo { return processInfo{Name: "nginx", PID: 9999} }
	c := checkRouterListening(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn for unknown listener with unknown launchd PID, got %s", c.Status)
	}
}

// --- checkRouterResponding ---

func TestCheckRouterResponding_2xx(t *testing.T) {
	d := allHealthy()
	c := checkRouterResponding(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckRouterResponding_5xx(t *testing.T) {
	d := allHealthy()
	d.httpProbe = fakeHTTP(503, nil)
	c := checkRouterResponding(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn for 5xx, got %s", c.Status)
	}
}

func TestCheckRouterResponding_TransportError(t *testing.T) {
	d := allHealthy()
	d.httpProbe = fakeHTTP(0, fmt.Errorf("connection refused"))
	d.httpProbeBody = fakeHTTPBody(0, "", fmt.Errorf("connection refused"))
	c := checkRouterResponding(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn for transport error, got %s", c.Status)
	}
	if c.Fix != "gtl serve restart" {
		t.Errorf("expected fix to suggest restart, got %q", c.Fix)
	}
}

func TestCheckRouterResponding_4xxIsWarn(t *testing.T) {
	// A 400 from the health endpoint historically meant the probe hit a TLS
	// router over plain HTTP (Go's "HTTP request to an HTTPS server" reply).
	// The endpoint itself only returns 200 — any 4xx means something is wrong.
	d := allHealthy()
	d.httpProbe = fakeHTTP(400, nil)
	c := checkRouterResponding(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn for 4xx, got %s (%s)", c.Status, c.Detail)
	}
}

// directDropped wires deps for the pf-drop scenario: the direct probe times
// out and the :443 fallback answers with the given status/body. Every URL
// probed (by either probe) is appended to probed, in call order.
func directDropped(d *healthDeps, fbStatus int, fbBody string, fbErr error, probed *[]string) {
	d.httpProbe = func(url string, _ time.Duration) (int, error) {
		*probed = append(*probed, url)
		return 0, fmt.Errorf("connection timed out")
	}
	d.httpProbeBody = func(url string, _ time.Duration) (int, string, error) {
		*probed = append(*probed, url)
		return fbStatus, fbBody, fbErr
	}
}

func TestCheckRouterResponding_DirectDroppedButForwardAnswers(t *testing.T) {
	d := allHealthy()
	var probed []string
	directDropped(&d, 200, "ok\n", nil, &probed)
	c, alive := routerResponding(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s (%s)", c.Status, c.Detail)
	}
	if !alive {
		t.Error("router answering via the 443 forward must count as alive")
	}
	if !strings.Contains(c.Detail, "443") || !strings.Contains(c.Detail, "packet filter") {
		t.Errorf("expected detail to explain the 443/packet-filter situation, got %q", c.Detail)
	}
	if c.Fix == "gtl serve restart" {
		t.Errorf("restart must not be suggested when the router is serving, got %q", c.Fix)
	}
	if len(probed) != 2 || !strings.Contains(probed[0], ":8443/") || !strings.Contains(probed[1], ":443/") {
		t.Errorf("expected direct probe then :443 fallback, got %v", probed)
	}
}

func TestCheckRouterResponding_DirectAndForwardFail(t *testing.T) {
	d := allHealthy()
	var probed []string
	directDropped(&d, 0, "", fmt.Errorf("connection timed out"), &probed)
	c, alive := routerResponding(d, 8443)
	if c.Status != "warn" || alive {
		t.Errorf("expected warn/not-alive, got %s alive=%v", c.Status, alive)
	}
	if !strings.Contains(c.Detail, "liveness probe failed") {
		t.Errorf("expected the plain probe-failure detail, got %q", c.Detail)
	}
}

// A squatter on :443 whose catch-all handler answers 200 must not count as
// our router — only the health contract body ("ok") proves identity.
func TestCheckRouterResponding_ForwardWrongBody_NotAlive(t *testing.T) {
	d := allHealthy()
	var probed []string
	directDropped(&d, 200, "<html>welcome to some dev server</html>", nil, &probed)
	c, alive := routerResponding(d, 8443)
	if alive {
		t.Error("a non-contract :443 response must not count as alive")
	}
	if !strings.Contains(c.Detail, "liveness probe failed") {
		t.Errorf("expected the plain probe-failure detail, got %q", c.Detail)
	}
}

func TestCheckRouterResponding_Forward4xx_NotAlive(t *testing.T) {
	d := allHealthy()
	var probed []string
	directDropped(&d, 404, "ok", nil, &probed)
	_, alive := routerResponding(d, 8443)
	if alive {
		t.Error("a 4xx from :443 must not count as alive")
	}
}

func TestCheckRouterResponding_NoForwardConfigured_NoFallbackProbe(t *testing.T) {
	d := allHealthy()
	d.isPortForwardConfigured = func() bool { return false }
	var probed []string
	directDropped(&d, 200, "ok\n", nil, &probed)
	_, alive := routerResponding(d, 8443)
	if alive {
		t.Error("without a 443 forward there is no fallback evidence of liveness")
	}
	if len(probed) != 1 {
		t.Errorf("expected a single direct probe, got %v", probed)
	}
}

func TestWaitRouterResponding_ForwardAliveSucceeds(t *testing.T) {
	d := allHealthy()
	var probed []string
	directDropped(&d, 200, "ok\n", nil, &probed)
	if err := waitRouterRespondingWith(d, 8443, 100*time.Millisecond); err != nil {
		t.Errorf("bounce verification must pass when the router answers via 443, got %v", err)
	}
}

func TestCheckRouterResponding_ProbeSchemeMatchesRouter(t *testing.T) {
	for _, tc := range []struct {
		tls    bool
		scheme string
	}{
		{tls: false, scheme: "http://"},
		{tls: true, scheme: "https://"},
	} {
		d := allHealthy()
		d.routerUsesTLS = func() bool { return tc.tls }
		var gotURL string
		d.httpProbe = func(url string, _ time.Duration) (int, error) {
			gotURL = url
			return 200, nil
		}
		checkRouterResponding(d, 8443)
		if !strings.HasPrefix(gotURL, tc.scheme) {
			t.Errorf("routerUsesTLS=%v: expected probe URL with %s, got %q", tc.tls, tc.scheme, gotURL)
		}
	}
}

// --- checkPortForward ---

func TestCheckPortForward_Ok(t *testing.T) {
	d := allHealthy()
	c := checkPortForward(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckPortForward_NotConfigured(t *testing.T) {
	d := allHealthy()
	d.checkPortForward = fakePF(false, false, false, "")
	c := checkPortForward(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}

func TestCheckPortForward_ConfiguredOnDiskButNotInKernel(t *testing.T) {
	// This is the exact failure mode the user hit: pf.conf has the
	// rule but `pfctl -s nat` doesn't, so traffic to :443 doesn't reach
	// the router. We also need port 443 to be unreachable for the
	// kernel-says-no path to dominate (otherwise the dial succeeds and
	// we treat the system as healthy).
	d := allHealthy()
	d.checkPortForward = fakePF(true, false, true, "rule not loaded in kernel — pf.conf has it but `pfctl -s nat` doesn't show port 8443 (run 'gtl serve reload-pf')")
	d.dialTimeout = func(_, addr string, _ time.Duration) (net.Conn, error) {
		if addr == "127.0.0.1:443" {
			return nil, fmt.Errorf("connection refused")
		}
		return fakeConn{}, nil
	}
	c := checkPortForward(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
	if c.Fix != "gtl serve reload-pf" {
		t.Errorf("expected fix to be 'gtl serve reload-pf', got %q", c.Fix)
	}
}

// When pfctl needs sudo and we can't read the kernel ruleset, we should
// fall back to a port-443 dial as the authoritative signal. If the dial
// succeeds, the system IS forwarding — report ok with a note that we
// couldn't verify via pfctl.
func TestCheckPortForward_KernelStateUnknown_DialSucceeds(t *testing.T) {
	d := allHealthy()
	d.checkPortForward = fakePFKernelUnknown(true, true, "kernel ruleset not readable without sudo — verify with 'sudo pfctl -s nat'")
	c := checkPortForward(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok (dial succeeded), got %s (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "not readable without sudo") {
		t.Errorf("expected sudo note in detail, got %q", c.Detail)
	}
}

// Without sudo on modern macOS, both `pfctl -s info` and `pfctl -s nat`
// can fail. We must NOT report "pf disabled" in that case — the dial is
// the only authoritative signal.
func TestCheckPortForward_PfStateUnknown_DialSucceedsIsHealthy(t *testing.T) {
	d := allHealthy()
	d.checkPortForward = fakePFPfUnknown(true, "pf state not readable without sudo")
	c := checkPortForward(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok when dial succeeds, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckPortForward_PfStateUnknown_DialFailsErrors(t *testing.T) {
	d := allHealthy()
	d.checkPortForward = fakePFPfUnknown(true, "pf state not readable without sudo")
	d.dialTimeout = func(_, addr string, _ time.Duration) (net.Conn, error) {
		if addr == "127.0.0.1:443" {
			return nil, fmt.Errorf("connection refused")
		}
		return fakeConn{}, nil
	}
	c := checkPortForward(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error when dial fails, got %s", c.Status)
	}
}

// Same scenario but the dial fails: rules might really be missing.
// Report error with the reload-pf fix.
func TestCheckPortForward_KernelStateUnknown_DialFails(t *testing.T) {
	d := allHealthy()
	d.checkPortForward = fakePFKernelUnknown(true, true, "kernel ruleset not readable without sudo — verify with 'sudo pfctl -s nat'")
	d.dialTimeout = func(_, addr string, _ time.Duration) (net.Conn, error) {
		if addr == "127.0.0.1:443" {
			return nil, fmt.Errorf("connection refused")
		}
		return fakeConn{}, nil
	}
	c := checkPortForward(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
	if c.Fix != "gtl serve reload-pf" {
		t.Errorf("expected fix to be 'gtl serve reload-pf', got %q", c.Fix)
	}
}

// --- checkPfReloadDaemon ---

func TestCheckPfReloadDaemon_OkWhenInstalled(t *testing.T) {
	d := allHealthy()
	c := checkPfReloadDaemon(d)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckPfReloadDaemon_WarnWhenPfConfiguredButDaemonMissing(t *testing.T) {
	// The exact upgrade path: user has pf rules from v0.40.0 but the
	// daemon was added in v0.40.1. They need to re-run gtl serve install.
	d := allHealthy()
	d.isPfReloadDaemonInstalled = func() bool { return false }
	c := checkPfReloadDaemon(d)
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
	if c.Fix != "gtl serve install" {
		t.Errorf("expected serve install fix, got %q", c.Fix)
	}
}

func TestCheckPfReloadDaemon_OkWhenPfNotConfigured(t *testing.T) {
	// User chose not to install port forwarding. The daemon would be
	// pointless — don't nag them about it.
	d := allHealthy()
	d.isPortForwardConfigured = func() bool { return false }
	d.isPfReloadDaemonInstalled = func() bool { return false }
	c := checkPfReloadDaemon(d)
	if c.Status != "ok" {
		t.Errorf("expected ok (n/a), got %s", c.Status)
	}
}

// When the rule is confirmed loaded in the kernel AND the router's own port
// answers but :443 does not, the culprit is an external filter (VPN
// killswitch / packet filter), not pf. We must NOT send the user to
// reload-pf in that case — reload-pf can't fix a loopback intercept.
func TestCheckPortForward_RuleLoadedButPort443Unreachable_ExternalFirewall(t *testing.T) {
	d := allHealthy() // fakePF(true, true, true) → loaded in kernel, kernel state known
	d.dialTimeout = func(_, addr string, _ time.Duration) (net.Conn, error) {
		if addr == "127.0.0.1:443" {
			return nil, fmt.Errorf("connection refused")
		}
		return fakeConn{}, nil // router port (8443) answers
	}
	c := checkPortForward(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
	if strings.Contains(c.Fix, "reload-pf") {
		t.Errorf("external-firewall diagnosis must not point at reload-pf, got %q", c.Fix)
	}
	if !strings.Contains(c.Detail, "outside gtl") {
		t.Errorf("expected external-firewall detail, got %q", c.Detail)
	}
}

// If the router's OWN port is also unreachable, we can't blame an external
// :443-specific filter — fall back to the reload-pf diagnosis.
func TestCheckPortForward_RuleLoadedButRouterPortAlsoDown(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false) // nothing reachable, including the router port
	c := checkPortForward(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
	if c.Fix != "gtl serve reload-pf" {
		t.Errorf("expected reload-pf fix when router port is also down, got %q", c.Fix)
	}
}
