package service

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// PfReloadDaemonLabel is the launchd label for our boot-time pf reloader.
// Lives in /Library/LaunchDaemons (system-level, runs as root). The daemon
// exists solely so pf rules survive a reboot — macOS loads pf.conf at boot
// via Apple's own LaunchDaemon but does NOT enable pf. Our daemon re-runs
// `pfctl -ef /etc/pf.conf` to enable pf and load our rdr rule.
//
// The daemon invokes Apple's signed /sbin/pfctl directly — it does NOT
// reference our gtl binary, so no notarization or code-signing concerns
// apply. The plist itself is a static config file.
const PfReloadDaemonLabel = "dev.treeline.pfreload"

// PfReloadDaemonPath returns the on-disk plist path. macOS-only; on
// other platforms returns "" since we don't ship this daemon there.
func PfReloadDaemonPath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return "/Library/LaunchDaemons/" + PfReloadDaemonLabel + ".plist"
}

// pfReloadDaemonPlist is the static plist body. No template params —
// /sbin/pfctl is part of the OS, /etc/pf.conf is where our rdr rule
// already lives.
const pfReloadDaemonPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>dev.treeline.pfreload</string>
    <key>ProgramArguments</key>
    <array>
        <string>/sbin/pfctl</string>
        <string>-ef</string>
        <string>/etc/pf.conf</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/var/log/dev.treeline.pfreload.err</string>
    <key>StandardOutPath</key>
    <string>/var/log/dev.treeline.pfreload.out</string>
</dict>
</plist>
`

// IsPfReloadDaemonInstalled reports whether our LaunchDaemon plist exists
// on disk. Read-only; no sudo needed.
func IsPfReloadDaemonInstalled() bool {
	path := PfReloadDaemonPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// pfReloadDaemonPlistBody returns the static plist body installed at
// PfReloadDaemonPath(). Used by installDarwinPortForward to write the
// same plist as part of a combined sudo session, eliminating the second
// password prompt that previously gated daemon installation.
func pfReloadDaemonPlistBody() string { return pfReloadDaemonPlist }

// pfReloadDaemonInstallFragment returns a `sh -c` fragment that, when run
// as root, copies a pre-rendered plist into /Library/LaunchDaemons, fixes
// ownership/perms, and bootstraps the LaunchDaemon. The fragment ends with
// `launchctl bootstrap`, whose exit code becomes the fragment's exit code
// — so a failed bootstrap surfaces as a non-zero exit. The caller writes
// pfReloadDaemonPlist to tmpPlistPath before invoking the script.
func pfReloadDaemonInstallFragment(tmpPlistPath string) string {
	target := PfReloadDaemonPath()
	return fmt.Sprintf(
		"/bin/cp '%s' '%s' && "+
			"/usr/sbin/chown root:wheel '%s' && "+
			"/bin/chmod 644 '%s' && "+
			"(/bin/launchctl bootout system/%s 2>/dev/null; true) && "+
			"/bin/launchctl bootstrap system '%s'",
		tmpPlistPath, target,
		target,
		target,
		PfReloadDaemonLabel,
		target,
	)
}

// InstallPfReloadDaemon writes the LaunchDaemon plist to
// /Library/LaunchDaemons and bootstraps it so it runs at every boot.
// Requires sudo. Idempotent — safe to call when already installed.
//
// macOS-only. On other platforms returns nil (no-op) since iptables on
// Linux distros generally persist their own rules via netfilter-persistent
// or iptables-save and don't need a separate boot service.
//
// Note: gtl serve install bundles this work into the same sudo session as
// the pf rules install (see installDarwinPortForward) so users only ever
// hit a single password prompt and the two stay atomic. This function is
// retained for callers that need to (re-)install the daemon on its own —
// e.g. doctor-style repair flows.
func InstallPfReloadDaemon() error {
	if runtime.GOOS != "darwin" {
		return nil
	}

	tmp, err := os.CreateTemp("", "treeline-pfreload-*.plist")
	if err != nil {
		return fmt.Errorf("creating temp plist: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(pfReloadDaemonPlist); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to install the boot-time pf reloader: ",
		"sh", "-c", pfReloadDaemonInstallFragment(tmp.Name()))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pf-reload daemon install failed: %w", err)
	}
	return nil
}

// UninstallPfReloadDaemon removes the LaunchDaemon. Symmetric with install
// — requires sudo. Idempotent.
func UninstallPfReloadDaemon() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if !IsPfReloadDaemonInstalled() {
		return nil
	}
	target := PfReloadDaemonPath()
	script := fmt.Sprintf(
		"/bin/launchctl bootout system/%s 2>/dev/null; "+
			"/bin/rm -f '%s'",
		PfReloadDaemonLabel,
		target,
	)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to remove the boot-time pf reloader: ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pf-reload daemon uninstall failed: %w", err)
	}
	return nil
}
