package service

import (
	"runtime"
	"strings"
	"testing"
)

func TestPfReloadDaemonInstallFragment(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("pf reload daemon paths only resolve on darwin")
	}
	frag := pfReloadDaemonInstallFragment("/tmp/treeline-pfreload-abc.plist")

	target := "/Library/LaunchDaemons/dev.treeline.pfreload.plist"

	// The fragment must copy the temp plist into the canonical LaunchDaemons
	// path, fix ownership/perms, best-effort bootout, and then bootstrap.
	for _, want := range []string{
		"/bin/cp '/tmp/treeline-pfreload-abc.plist' '" + target + "'",
		"/usr/sbin/chown root:wheel '" + target + "'",
		"/bin/chmod 644 '" + target + "'",
		"/bin/launchctl bootout system/dev.treeline.pfreload",
		"/bin/launchctl bootstrap system '" + target + "'",
	} {
		if !strings.Contains(frag, want) {
			t.Errorf("fragment missing %q\nfragment: %s", want, frag)
		}
	}

	// bootstrap is the gate for overall success — it must be the final
	// statement so a failed bootstrap surfaces a non-zero exit code.
	if !strings.HasSuffix(strings.TrimSpace(frag), "/bin/launchctl bootstrap system '"+target+"'") {
		t.Errorf("fragment must end with bootstrap so its exit code drives the script's exit code\nfragment: %s", frag)
	}

	// bootout failures must be swallowed so they don't break the chain on a
	// fresh install where the label isn't already loaded.
	if !strings.Contains(frag, "bootout system/dev.treeline.pfreload 2>/dev/null; true") {
		t.Errorf("fragment must swallow bootout failures\nfragment: %s", frag)
	}
}

func TestPfReloadDaemonPlistBody(t *testing.T) {
	body := PfReloadDaemonPlistBody()
	for _, want := range []string{
		"<key>Label</key>",
		"<string>dev.treeline.pfreload</string>",
		"<string>/sbin/pfctl</string>",
		"<string>-ef</string>",
		"<string>/etc/pf.conf</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist body missing %q", want)
		}
	}
}
