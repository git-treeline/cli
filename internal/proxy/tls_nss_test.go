package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestNssDBPrefix(t *testing.T) {
	dir := t.TempDir()

	// No DB present.
	if got := nssDBPrefix(dir); got != "" {
		t.Errorf("empty dir should yield no prefix, got %q", got)
	}

	// Legacy cert8.db → dbm:
	legacy := filepath.Join(dir, "legacy")
	_ = os.MkdirAll(legacy, 0o755)
	_ = os.WriteFile(filepath.Join(legacy, "cert8.db"), []byte("x"), 0o644)
	if got := nssDBPrefix(legacy); got != "dbm:"+legacy {
		t.Errorf("cert8.db should yield dbm: prefix, got %q", got)
	}

	// Modern cert9.db → sql: (and wins if both present).
	modern := filepath.Join(dir, "modern")
	_ = os.MkdirAll(modern, 0o755)
	_ = os.WriteFile(filepath.Join(modern, "cert9.db"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(modern, "cert8.db"), []byte("x"), 0o644)
	if got := nssDBPrefix(modern); got != "sql:"+modern {
		t.Errorf("cert9.db should yield sql: prefix, got %q", got)
	}
}

func TestNssDBDirs_DiscoversChromiumAndFirefox(t *testing.T) {
	home := t.TempDir()

	// Chromium shared DB.
	chromium := filepath.Join(home, ".pki", "nssdb")
	_ = os.MkdirAll(chromium, 0o755)
	_ = os.WriteFile(filepath.Join(chromium, "cert9.db"), []byte("x"), 0o644)

	// Native Firefox profile (sql).
	ffNative := filepath.Join(home, ".mozilla", "firefox", "abcd.default")
	_ = os.MkdirAll(ffNative, 0o755)
	_ = os.WriteFile(filepath.Join(ffNative, "cert9.db"), []byte("x"), 0o644)

	// Snap Firefox profile (legacy dbm).
	ffSnap := filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox", "wxyz.default")
	_ = os.MkdirAll(ffSnap, 0o755)
	_ = os.WriteFile(filepath.Join(ffSnap, "cert8.db"), []byte("x"), 0o644)

	// A profile-shaped dir with no DB is ignored.
	empty := filepath.Join(home, ".mozilla", "firefox", "empty.default")
	_ = os.MkdirAll(empty, 0o755)

	got := nssDBDirs(home)
	sort.Strings(got)
	want := []string{
		"dbm:" + ffSnap,
		"sql:" + chromium,
		"sql:" + ffNative,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nssDBDirs mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestFirefoxProfileRoots(t *testing.T) {
	roots := firefoxProfileRoots("/home/u")
	want := map[string]bool{
		"/home/u/.mozilla/firefox":                              true,
		"/home/u/snap/firefox/common/.mozilla/firefox":          true,
		"/home/u/.var/app/org.mozilla.firefox/.mozilla/firefox": true,
	}
	if len(roots) != len(want) {
		t.Fatalf("expected %d roots, got %d: %v", len(want), len(roots), roots)
	}
	for _, r := range roots {
		if !want[r] {
			t.Errorf("unexpected firefox root %q", r)
		}
	}
}

func TestTrustNSS_AddsToEachDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// One existing Firefox profile DB so we exercise the add path without
	// depending on a real certutil creating the chromium store.
	ff := filepath.Join(home, ".mozilla", "firefox", "p.default")
	_ = os.MkdirAll(ff, 0o755)
	_ = os.WriteFile(filepath.Join(ff, "cert9.db"), []byte("x"), 0o644)

	// certutil must resolve on PATH for trustNSS to proceed; skip if absent.
	if _, err := exec.LookPath("certutil"); err != nil {
		t.Skip("certutil not on PATH")
	}

	var added []string
	orig := nssRunCmd
	nssRunCmd = func(name string, args ...string) error {
		// Record "-A" (add) invocations and their target DB.
		for i, a := range args {
			if a == "-A" {
				for j, aa := range args {
					if aa == "-d" && j+1 < len(args) {
						added = append(added, args[j+1])
					}
				}
			}
			_ = i
		}
		return nil
	}
	t.Cleanup(func() { nssRunCmd = orig })

	trustNSS(filepath.Join(home, "ca.pem"))

	found := false
	for _, db := range added {
		if db == "sql:"+ff {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CA added to firefox DB sql:%s, got adds: %v", ff, added)
	}
}
