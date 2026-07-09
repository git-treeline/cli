//go:build linux && linux_integration

// D3 VERIFICATION GATE — Linux CA trust parity (system store + NSS).
//
// These tests exercise the REAL Linux trust plumbing: the system OpenSSL store
// (TrustCA/UntrustCA -> update-ca-certificates) and the per-user NSS databases
// that Chrome/Chromium/Brave/Firefox actually consult (trustNSS/untrustNSS via
// a real `certutil`). They cannot run on macOS. The authority that these paths
// work is the `linux-integration` GitHub Actions job (ubuntu, root), which
// installs `libnss3-tools` so `certutil` is present and runs:
//
//	sudo -E go test -tags linux_integration -run Integration ./internal/proxy/...
package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestIntegration_NSSTrust_AddRemove installs the real CA into a real NSS
// database using the system `certutil`, then removes it — asserting the
// nickname is present after trustNSS and gone after untrustNSS. This is the
// browser-trust path that the system OpenSSL store does not cover.
func TestIntegration_NSSTrust_AddRemove(t *testing.T) {
	certutil, err := exec.LookPath("certutil")
	if err != nil {
		t.Fatalf("certutil not found — CI must install libnss3-tools: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	// GTL_HOME isolates the CA under a throwaway dir (and sets DevSuffix, so the
	// NSS nickname is dev-suffixed but consistent between add and remove).
	t.Setenv("GTL_HOME", t.TempDir())

	caPath, err := EnsureCA("localhost")
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	// A real Firefox-style NSS database (cert9.db/sql:) so trustNSS discovers a
	// DB to add into without depending on it creating the chromium store.
	ffProfile := filepath.Join(home, ".mozilla", "firefox", "test.default")
	if err := os.MkdirAll(ffProfile, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(certutil, "-N", "--empty-password", "-d", "sql:"+ffProfile).CombinedOutput(); err != nil {
		t.Fatalf("creating NSS db: %v\n%s", err, out)
	}

	trustNSS(caPath)

	name := nssCertName()
	if !nssHasCert(t, certutil, "sql:"+ffProfile, name) {
		t.Errorf("CA nickname %q not found in NSS db after trustNSS", name)
	}

	untrustNSS()
	if nssHasCert(t, certutil, "sql:"+ffProfile, name) {
		t.Errorf("CA nickname %q still present after untrustNSS", name)
	}
}

// nssHasCert reports whether the named cert exists in the given NSS db.
func nssHasCert(t *testing.T, certutil, db, name string) bool {
	t.Helper()
	err := exec.Command(certutil, "-d", db, "-L", "-n", name).Run()
	return err == nil
}

// TestIntegration_SystemTrust_AddRemove drives the real exported TrustCA /
// UntrustCA against the system OpenSSL store. On Debian/Ubuntu this copies the
// CA into /usr/local/share/ca-certificates and runs update-ca-certificates.
// Requires root. certutil-less hosts still pass: trustNSS degrades gracefully.
func TestIntegration_SystemTrust_AddRemove(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("system trust store test must run as root (sudo -E go test -tags linux_integration ...)")
	}
	if _, err := exec.LookPath("update-ca-certificates"); err != nil {
		t.Skipf("update-ca-certificates not present (non-Debian host): %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GTL_HOME", t.TempDir())

	caPath, err := EnsureCA("localhost")
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	cfg := linuxTrustConfigs[detectLinuxDistro()]
	installed := filepath.Join(cfg.certDir, "git-treeline.crt")
	t.Cleanup(func() { _ = UntrustCA() })

	if err := TrustCA(caPath); err != nil {
		t.Fatalf("TrustCA: %v", err)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Errorf("CA not copied into system store at %s: %v", installed, err)
	}

	if err := UntrustCA(); err != nil {
		t.Fatalf("UntrustCA: %v", err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("CA still present in system store at %s after UntrustCA (err=%v)", installed, err)
	}
}
