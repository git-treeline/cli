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
	frag := pfReloadDaemonInstallFragment(
		"/tmp/treeline-pfreload-abc.plist", "/tmp/treeline-pf-ensure-abc.sh")

	target := "/Library/LaunchDaemons/dev.treeline.pfreload.plist"

	// The fragment must install the repair script root-owned (root must
	// never execute a user-writable file) and executable, copy the temp
	// plist into the canonical LaunchDaemons path, fix ownership/perms,
	// best-effort bootout, and then bootstrap.
	for _, want := range []string{
		"/bin/mkdir -p '/Library/Application Support/dev.git-treeline'",
		"/bin/cp '/tmp/treeline-pf-ensure-abc.sh' '" + pfEnsureScriptPath + "'",
		"/usr/sbin/chown root:wheel '" + pfEnsureScriptPath + "'",
		"/bin/chmod 755 '" + pfEnsureScriptPath + "'",
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
	body := pfReloadDaemonPlistBody()
	for _, want := range []string{
		"<key>Label</key>",
		"<string>dev.treeline.pfreload</string>",
		"<string>/bin/sh</string>",
		"<string>" + pfEnsureScriptPath + "</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		// WatchPaths is the update-proofing: a macOS update that rewrites
		// /etc/pf.conf triggers the repair without waiting for a reboot.
		"<key>WatchPaths</key>",
		"<string>/etc/pf.conf</string>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist body missing %q", want)
		}
	}

	// The daemon must never execute the gtl binary — a root daemon running
	// a user-writable Homebrew binary would be a privilege escalation.
	if strings.Contains(body, "gtl") && !strings.Contains(body, "dev.git-treeline") {
		t.Errorf("plist must not reference the gtl binary\nbody: %s", body)
	}
}

func TestPfEnsureScript(t *testing.T) {
	script := pfEnsureScriptBody()

	// The presence check must grep the load-anchor line, not the marker:
	// the prod marker ("# git-treeline") is a substring of the .dev marker
	// ("# git-treeline.dev"), so a marker grep would see a dev-only
	// install as "prod already configured" and skip the prod repair.
	if !strings.Contains(script, `load anchor \"$anchor\" from`) {
		t.Errorf("script must detect presence via the load-anchor line\nscript: %s", script)
	}

	// The rewritten pf.conf must be validated before it replaces the live
	// file — a bad rewrite must never land in /etc/pf.conf.
	if !strings.Contains(script, `/sbin/pfctl -n -f "$tmp"`) {
		t.Errorf("script must validate the rewritten pf.conf with pfctl -n before installing it\nscript: %s", script)
	}

	// Both the prod and .dev anchor variants must be repaired so a dev
	// install (GTL_HOME) never breaks the shared daemon's prod repair.
	for _, want := range []string{`repair ""`, `repair ".dev"`} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\nscript: %s", want, script)
		}
	}

	// After any repair the script must still load the ruleset and enable
	// pf — that is the original daemon's job and must be preserved.
	for _, want := range []string{`/sbin/pfctl -f "$CONF"`, "/sbin/pfctl -e"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\nscript: %s", want, script)
		}
	}
}
