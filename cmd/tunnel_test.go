package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestTunnelConfigNames(t *testing.T) {
	configs := map[string]string{
		"gtl":          "example.dev",
		"gtl-personal": "personal.dev",
	}
	names := tunnelConfigNames(configs)
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "gtl" || names[1] != "gtl-personal" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestTunnelConfigNames_Empty(t *testing.T) {
	names := tunnelConfigNames(map[string]string{})
	if len(names) != 0 {
		t.Errorf("expected empty, got %v", names)
	}
}

func TestSortedKeys(t *testing.T) {
	got := sortedKeys(map[string]string{"c": "", "a": "", "b": ""})
	want := []string{"a", "b", "c"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("sortedKeys = %v, want %v", got, want)
	}
}

// --- routeDNSWithReauth tests ---

// recordingRouter captures the calls routeDNSWithReauth makes and replays
// scripted responses, so each test can describe a flow as a small program.
type recordingRouter struct {
	routeResponses []error
	loginErr       error
	promptAnswer   bool

	routeCalls  []routeCall
	loginCalls  []string
	promptCalls []string
}

type routeCall struct {
	tunnelName string
	host       string
	certPath   string
}

func (r *recordingRouter) deps() dnsRouter {
	return dnsRouter{
		route: func(tunnelName, host, certPath string) error {
			r.routeCalls = append(r.routeCalls, routeCall{tunnelName, host, certPath})
			if len(r.routeCalls) > len(r.routeResponses) {
				return fmt.Errorf("unscripted route call #%d", len(r.routeCalls))
			}
			return r.routeResponses[len(r.routeCalls)-1]
		},
		login: func(domain string) error {
			r.loginCalls = append(r.loginCalls, domain)
			return r.loginErr
		},
		prompt: func(msg string) bool {
			r.promptCalls = append(r.promptCalls, msg)
			return r.promptAnswer
		},
	}
}

func TestRouteDNSWithReauth_SuccessFirstTry(t *testing.T) {
	rec := &recordingRouter{routeResponses: []error{nil}}
	certPath := ""

	var err error
	captureStdout(t, func() {
		err = routeDNSWithReauth(rec.deps(), "gtl", "devtunl.com", "*.devtunl.com", &certPath)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.routeCalls) != 1 {
		t.Errorf("expected 1 route call, got %d", len(rec.routeCalls))
	}
	if len(rec.promptCalls) != 0 {
		t.Errorf("did not expect a prompt; got %d: %v", len(rec.promptCalls), rec.promptCalls)
	}
	if len(rec.loginCalls) != 0 {
		t.Errorf("did not expect a login; got %d", len(rec.loginCalls))
	}
	if certPath != "" {
		t.Errorf("certPath should not change on first-try success; got %q", certPath)
	}
}

func TestRouteDNSWithReauth_DefaultCertFails_ReauthSucceeds(t *testing.T) {
	rec := &recordingRouter{
		routeResponses: []error{errors.New("403 Forbidden"), nil},
		promptAnswer:   true,
	}
	certPath := ""

	var err error
	captureStdout(t, func() {
		err = routeDNSWithReauth(rec.deps(), "gtl", "devtunl.com", "*.devtunl.com", &certPath)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.routeCalls) != 2 {
		t.Fatalf("expected 2 route calls, got %d", len(rec.routeCalls))
	}
	if rec.routeCalls[0].certPath != "" {
		t.Errorf("first call should use default cert (empty path); got %q", rec.routeCalls[0].certPath)
	}
	if rec.routeCalls[1].certPath == "" {
		t.Error("second call should use domain-specific cert path")
	}
	if len(rec.loginCalls) != 1 || rec.loginCalls[0] != "devtunl.com" {
		t.Errorf("expected one login for devtunl.com; got %v", rec.loginCalls)
	}
	if certPath == "" {
		t.Error("expected certPath to be updated to domain-specific path after relogin")
	}
}

func TestRouteDNSWithReauth_DefaultCertFails_UserDeclinesReauth(t *testing.T) {
	rec := &recordingRouter{
		routeResponses: []error{errors.New("403 Forbidden")},
		promptAnswer:   false,
	}
	certPath := ""

	var err error
	captureStdout(t, func() {
		err = routeDNSWithReauth(rec.deps(), "gtl", "devtunl.com", "*.devtunl.com", &certPath)
	})
	if err == nil {
		t.Fatal("expected error when user declines re-auth")
	}
	var ce *CliError
	if !errors.As(err, &ce) {
		t.Errorf("expected *CliError, got %T: %v", err, err)
	}
	if len(rec.loginCalls) != 0 {
		t.Errorf("login should not run when prompt is declined; got %v", rec.loginCalls)
	}
	if len(rec.routeCalls) != 1 {
		t.Errorf("expected only the initial route call; got %d", len(rec.routeCalls))
	}
}

func TestRouteDNSWithReauth_DefaultCertFails_RetryAlsoFails(t *testing.T) {
	rec := &recordingRouter{
		routeResponses: []error{errors.New("403"), errors.New("403 again")},
		promptAnswer:   true,
	}
	certPath := ""

	var err error
	captureStdout(t, func() {
		err = routeDNSWithReauth(rec.deps(), "gtl", "devtunl.com", "*.devtunl.com", &certPath)
	})
	if err == nil {
		t.Fatal("expected error when retry also fails")
	}
	var ce *CliError
	if !errors.As(err, &ce) {
		t.Errorf("expected *CliError, got %T", err)
	}
	if len(rec.loginCalls) != 1 {
		t.Errorf("expected one login attempt; got %v", rec.loginCalls)
	}
	if len(rec.routeCalls) != 2 {
		t.Errorf("expected two route calls; got %d", len(rec.routeCalls))
	}
}

// When a domain-specific cert was already on disk and the route still fails,
// re-login won't help (cert is already domain-scoped). The function must
// surface the error without prompting or logging in.
func TestRouteDNSWithReauth_DomainCertFails_NoReauthOffered(t *testing.T) {
	rec := &recordingRouter{
		routeResponses: []error{errors.New("some other failure")},
	}
	certPath := "/home/u/.cloudflared/cert-devtunl.com.pem"

	var err error
	captureStdout(t, func() {
		err = routeDNSWithReauth(rec.deps(), "gtl", "devtunl.com", "*.devtunl.com", &certPath)
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(rec.promptCalls) != 0 {
		t.Errorf("must not prompt when domain cert was used; got %v", rec.promptCalls)
	}
	if len(rec.loginCalls) != 0 {
		t.Errorf("must not re-login when domain cert was used; got %v", rec.loginCalls)
	}
	if certPath != "/home/u/.cloudflared/cert-devtunl.com.pem" {
		t.Errorf("certPath should not be mutated; got %q", certPath)
	}
}

// Regression guard for the removed misleading message. printDNSManualInstructions
// should never claim "credentials are for a different Cloudflare zone" — that
// was a guess with no evidence. The actual zone-mismatch path runs only when
// RouteDNS itself errors with a 403, which the caller already surfaces.
func TestPrintDNSManualInstructions_NoBogusZoneClaim(t *testing.T) {
	stdout := captureStdout(t, func() {
		_ = printDNSManualInstructions("gtl", "devtunl.com", errors.New("403"))
	})
	bad := "credentials are for a different Cloudflare zone"
	if strings.Contains(stdout, bad) {
		t.Errorf("output should not contain %q (it was a guess, not evidence):\n%s", bad, stdout)
	}
}

func TestFileExistsForReset(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if fileExistsForReset(file) {
		t.Error("expected false for non-existent")
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileExistsForReset(file) {
		t.Error("expected true after write")
	}
	if fileExistsForReset(dir) {
		t.Error("expected false for directory")
	}
}
