//go:build linux && linux_integration

// D3 VERIFICATION GATE — Linux 443/HTTPS parity.
//
// This file exercises the REAL Linux port-forwarding stack end to end against
// a live kernel: it installs the iptables/nft REDIRECT 443->router rule, starts
// the actual proxy.Router, and asserts that traffic to 127.0.0.1:443 is
// redirected into the router. None of this can run (or even be observed) on
// macOS — the developer building this can only compile-check it. The authority
// that these paths actually work is the `linux-integration` GitHub Actions job,
// which runs this test as root on ubuntu:
//
//	sudo -E go test -tags linux_integration -run Integration ./internal/service/...
//
// Requirements: Linux, root (CAP_NET_ADMIN to write the nat table), and an
// iptables command (or the iptables-nft shim). The test fails loudly rather
// than skipping when run as root without those, so a broken stack cannot pass
// silently.
package service

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
)

// requireRoot fails the test when not running as root — the nat table is
// unwritable without CAP_NET_ADMIN, so a non-root run would only produce a
// misleading pass. The CI gate always runs as root; a local run that forgets
// sudo should be told, not silently skipped.
func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatalf("linux_integration tests must run as root (try: sudo -E go test -tags linux_integration ...)")
	}
}

// freeLoopbackPort grabs an ephemeral port on 127.0.0.1 and releases it so the
// router can bind it. The tiny race window is acceptable for a serialized
// integration test.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a loopback port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// countTreelineRules returns how many of our marked REDIRECT rules are present
// in the live nat OUTPUT chain. Used to prove idempotency (install twice must
// not stack a second rule).
func countTreelineRules(t *testing.T, ipt string) int {
	t.Helper()
	out, err := exec.Command(ipt, "-t", "nat", "-L", "OUTPUT", "-n").CombinedOutput()
	if err != nil {
		t.Fatalf("listing nat OUTPUT rules: %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "git-treeline") {
			n++
		}
	}
	return n
}

// caTrustedClient builds an HTTPS client that trusts only the git-treeline
// local CA. A successful request through it proves both the redirect (443 ->
// router) AND that the router served a cert chaining to our CA.
func caTrustedClient(t *testing.T) *http.Client {
	t.Helper()
	pem, err := os.ReadFile(proxy.CACertPath())
	if err != nil {
		t.Fatalf("reading CA cert %s: %v", proxy.CACertPath(), err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatalf("CA cert %s is not valid PEM", proxy.CACertPath())
	}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
}

// waitForHealth polls url until the router answers "ok" or the deadline
// passes. Returns the trimmed body on success.
func waitForHealth(t *testing.T, client *http.Client, url string, deadline time.Duration) (string, bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastErr error
	for time.Now().Before(end) {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return strings.TrimSpace(string(body)), true
	}
	if lastErr != nil {
		t.Logf("waitForHealth(%s) last error: %v", url, lastErr)
	}
	return "", false
}

// TestIntegration_PortForward_EndToEnd is the core D3 gate: it drives the real
// InstallPortForward/CheckPortForward/ReloadPortForward/UninstallPortForward
// against a live kernel and a live TLS router, asserting that:
//
//   - install adds the REDIRECT rule and CheckPortForward sees it in the kernel;
//   - an HTTPS request to 127.0.0.1:443 is actually redirected to the router
//     and returns the health body (proves the redirect works, not just that a
//     rule exists);
//   - installing again is idempotent (exactly one marked rule remains);
//   - reload-pf re-applies the rule after it is manually flushed;
//   - uninstall removes the rule and 443 stops answering.
func TestIntegration_PortForward_EndToEnd(t *testing.T) {
	requireRoot(t)

	ipt, err := resolveIptables()
	if err != nil {
		t.Fatalf("resolveIptables: %v (install iptables or the iptables-nft shim)", err)
	}

	// Isolate all on-disk state (CA, certs, and the dev-suffixed unit/anchor
	// names) under a throwaway GTL_HOME so the test never touches a real
	// install. Note: GTL_HOME activates DevSuffix(), so the persistence unit
	// and iptables comment stay internally consistent across install/uninstall.
	gtlHome := t.TempDir()
	t.Setenv("GTL_HOME", gtlHome)

	// Real CA so the router can serve a genuinely trusted leaf cert.
	if _, err := proxy.EnsureCA("localhost"); err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	routerPort := freeLoopbackPort(t)
	reg := registry.New(t.TempDir() + "/registry.json")
	router := proxy.NewRouter(routerPort, reg).WithTLS()
	go func() { _ = router.Run() }()

	client := caTrustedClient(t)
	directURL := fmt.Sprintf("https://127.0.0.1:%d%s", routerPort, proxy.HealthEndpoint)
	if body, ok := waitForHealth(t, client, directURL, 5*time.Second); !ok || body != "ok" {
		t.Fatalf("router did not come up on its own port %d (body=%q ok=%v)", routerPort, body, ok)
	}

	// Guarantee we always tear down the kernel rule even if an assertion fails.
	t.Cleanup(func() { _ = UninstallPortForward() })

	// --- install ---
	if err := InstallPortForward(routerPort); err != nil {
		t.Fatalf("InstallPortForward(%d): %v", routerPort, err)
	}
	if !IsPortForwardConfigured() {
		t.Error("IsPortForwardConfigured() = false after install")
	}
	st := CheckPortForward(routerPort)
	if !st.KernelStateKnown {
		t.Errorf("KernelStateKnown = false as root; detail=%q", st.Detail)
	}
	if !st.LoadedInKernel {
		t.Errorf("LoadedInKernel = false after install; detail=%q", st.Detail)
	}
	if n := countTreelineRules(t, ipt); n != 1 {
		t.Errorf("expected exactly 1 git-treeline nat rule after install, got %d", n)
	}

	// --- the redirect actually carries traffic: 443 -> router ---
	redirURL := fmt.Sprintf("https://127.0.0.1:443%s", proxy.HealthEndpoint)
	if body, ok := waitForHealth(t, client, redirURL, 5*time.Second); !ok || body != "ok" {
		t.Fatalf("request to :443 did not reach the router (body=%q ok=%v) — REDIRECT not working", body, ok)
	}

	// --- idempotency: installing again must not stack a duplicate rule ---
	if err := InstallPortForward(routerPort); err != nil {
		t.Fatalf("second InstallPortForward(%d): %v", routerPort, err)
	}
	if n := countTreelineRules(t, ipt); n != 1 {
		t.Errorf("install is not idempotent: expected 1 git-treeline rule, got %d", n)
	}

	// --- reload-pf re-applies a flushed rule ---
	// Simulate a reboot/network-flush by deleting the live rule, leaving the
	// on-disk persistence unit intact. ReloadPortForward must put it back.
	for countTreelineRules(t, ipt) > 0 {
		line, derr := exec.Command(ipt, "-t", "nat", "-L", "OUTPUT", "-n", "--line-numbers").CombinedOutput()
		if derr != nil {
			t.Fatalf("listing rules before delete: %v\n%s", derr, line)
		}
		num := firstTreelineRuleNumber(string(line))
		if num == "" {
			break
		}
		if out, derr := exec.Command(ipt, "-t", "nat", "-D", "OUTPUT", num).CombinedOutput(); derr != nil {
			t.Fatalf("deleting rule %s: %v\n%s", num, derr, out)
		}
	}
	if countTreelineRules(t, ipt) != 0 {
		t.Fatal("failed to flush the rule before reload test")
	}
	if err := ReloadPortForward(); err != nil {
		t.Fatalf("ReloadPortForward: %v", err)
	}
	if n := countTreelineRules(t, ipt); n != 1 {
		t.Errorf("reload-pf did not reapply exactly one rule, got %d", n)
	}
	if body, ok := waitForHealth(t, client, redirURL, 5*time.Second); !ok || body != "ok" {
		t.Errorf("after reload-pf, :443 did not reach the router (body=%q ok=%v)", body, ok)
	}

	// --- uninstall removes the rule and 443 stops answering ---
	if err := UninstallPortForward(); err != nil {
		t.Fatalf("UninstallPortForward: %v", err)
	}
	if n := countTreelineRules(t, ipt); n != 0 {
		t.Errorf("uninstall left %d git-treeline rule(s) behind", n)
	}
	// With the redirect gone, nothing listens on :443, so the dial is refused.
	if _, err := net.DialTimeout("tcp", "127.0.0.1:443", time.Second); err == nil {
		t.Error("expected connection to :443 to be refused after uninstall")
	}
	// The router itself is still alive on its own port — proving we removed the
	// redirect, not the server.
	if body, ok := waitForHealth(t, client, directURL, 3*time.Second); !ok || body != "ok" {
		t.Errorf("router should still be reachable on its own port after uninstall (body=%q ok=%v)", body, ok)
	}
}

// firstTreelineRuleNumber returns the line number of the first marked rule in
// `iptables -L --line-numbers` output, or "" when none remain.
func firstTreelineRuleNumber(listing string) string {
	for _, line := range strings.Split(listing, "\n") {
		if strings.Contains(line, "git-treeline") {
			if fields := strings.Fields(line); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}
