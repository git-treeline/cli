package service

import (
	"fmt"
	"net"
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

func allHealthy() healthDeps {
	return healthDeps{
		isRunning:               func() bool { return true },
		installedBinaryPath:     func() string { return "/usr/local/bin/gtl" },
		runningRouterVersion:    func() string { return "1.0.0" },
		isPortForwardConfigured: func() bool { return true },
		dialTimeout:             fakeDial(true),
		executable:              func() (string, error) { return "/usr/local/bin/gtl", nil },
		processOnPort:           func(int) string { return "gtl (pid 1234)" },
	}
}

// --- checkHealthWith integration ---

func TestCheckHealthWith_AllHealthy(t *testing.T) {
	checks := checkHealthWith(allHealthy(), 8443, "1.0.0")

	if len(checks) != 5 {
		t.Fatalf("expected 5 checks, got %d", len(checks))
	}
	for _, c := range checks {
		if c.Status != "ok" {
			t.Errorf("check %q: expected ok, got %s (%s)", c.Name, c.Status, c.Detail)
		}
	}
}

func TestCheckHealthWith_AllBroken(t *testing.T) {
	d := healthDeps{
		isRunning:               func() bool { return false },
		installedBinaryPath:     func() string { return "" },
		runningRouterVersion:    func() string { return "" },
		isPortForwardConfigured: func() bool { return false },
		dialTimeout:             fakeDial(false),
		executable:              func() (string, error) { return "/usr/local/bin/gtl", nil },
		processOnPort:           func(int) string { return "" },
	}

	checks := checkHealthWith(d, 8443, "1.0.0")

	expected := map[string]string{
		"service":         "error",
		"binary":          "warn",
		"router_version":  "warn",
		"router_port":     "error",
		"port_forwarding": "warn",
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

func TestCheckRouterListening_Ok(t *testing.T) {
	d := allHealthy()
	c := checkRouterListening(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s", c.Status)
	}
}

func TestCheckRouterListening_NotListening_ServiceRunning(t *testing.T) {
	d := allHealthy()
	d.dialTimeout = fakeDial(false)
	c := checkRouterListening(d, 8443)
	if c.Status != "error" {
		t.Errorf("expected error, got %s", c.Status)
	}
	if c.Detail != "service registered but port 8443 not listening" {
		t.Errorf("unexpected detail: %s", c.Detail)
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
	if c.Detail != "port 8443 not listening" {
		t.Errorf("unexpected detail: %s", c.Detail)
	}
}

func TestCheckRouterListening_RogueProcess(t *testing.T) {
	d := allHealthy()
	d.processOnPort = func(int) string { return "nginx (pid 5678)" }
	c := checkRouterListening(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn for rogue process, got %s", c.Status)
	}
}

// --- checkPortForward ---

func TestCheckPortForward_Ok(t *testing.T) {
	d := allHealthy()
	c := checkPortForward(d, 8443)
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s", c.Status)
	}
}

func TestCheckPortForward_NotConfigured(t *testing.T) {
	d := allHealthy()
	d.isPortForwardConfigured = func() bool { return false }
	c := checkPortForward(d, 8443)
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}

func TestCheckPortForward_ConfiguredButUnreachable(t *testing.T) {
	d := allHealthy()
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
}
