package service

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// PfReloadDaemonLabel is the launchd label for our boot-time pf reloader.
// Lives in /Library/LaunchDaemons (system-level, runs as root). The daemon
// exists so pf rules survive a reboot AND macOS updates: Apple loads
// pf.conf at boot but does NOT enable pf, and OS updates periodically
// revert /etc/pf.conf to the stock file, silently deleting our
// rdr-anchor/load lines. The daemon runs pfEnsureScript, which re-inserts
// those lines when missing, then loads the ruleset and enables pf. It
// fires at boot (RunAtLoad) and whenever /etc/pf.conf changes (WatchPaths)
// — so an update that rewrites the file triggers its own repair.
//
// The daemon must NOT reference our gtl binary: a root daemon executing a
// user-writable Homebrew binary would be a privilege escalation. Instead
// it runs /bin/sh on a root-owned script installed by `gtl serve install`.
const PfReloadDaemonLabel = "dev.treeline.pfreload"

// pfEnsureScriptPath is where the repair script lives on disk. Installed
// root:wheel 755 by the same sudo session that installs the daemon plist,
// so root never executes anything user-writable.
const pfEnsureScriptPath = "/Library/Application Support/dev.git-treeline/pf-ensure.sh"

// pfEnsureScript is the static repair script the daemon runs. It handles
// both the prod and .dev (GTL_HOME) anchor variants so a dev install
// never breaks the shared daemon's ability to repair the prod lines.
//
// Load-bearing details:
//   - The presence check greps for the `load anchor "<name>" from` line,
//     NOT the marker comment — the prod marker is a substring of the .dev
//     marker, so a marker grep would false-positive on dev-only installs.
//   - The rewritten pf.conf is validated with `pfctl -n -f` before it
//     replaces the live file; a failed validation leaves pf.conf alone.
//   - `pfctl -e` is masked: it exits non-zero when pf is already enabled,
//     which is the common case and not an error.
const pfEnsureScript = `#!/bin/sh
# git-treeline boot-time pf loader (dev.treeline.pfreload).
#
# Repairs /etc/pf.conf when a macOS update reverts it to the stock file
# (removing the git-treeline rdr-anchor/load lines), then loads the
# ruleset and enables pf. Runs at boot and whenever /etc/pf.conf changes.
# Idempotent: when pf.conf already references the anchors it only reloads.
# Managed by 'gtl serve install' — do not edit by hand.

CONF=/etc/pf.conf

repair() {
    anchor="dev.treeline.router$1"
    anchor_file="/etc/pf.anchors/$anchor"
    marker="# git-treeline$1"
    [ -f "$anchor_file" ] || return 0
    /usr/bin/grep -qF "load anchor \"$anchor\" from" "$CONF" && return 0
    tmp=$(/usr/bin/mktemp /tmp/treeline-pfconf.XXXXXX) || return 1
    /usr/bin/awk -v rdr="rdr-anchor \"$anchor\" $marker" \
        -v load="load anchor \"$anchor\" from \"$anchor_file\" $marker" '
        { line[NR] = $0 }
        $1 == "rdr-anchor" { last = NR }
        END {
            if (last == 0) print rdr
            for (i = 1; i <= NR; i++) { print line[i]; if (i == last) print rdr }
            print load
        }' "$CONF" > "$tmp" || { /bin/rm -f "$tmp"; return 1; }
    if ! /sbin/pfctl -n -f "$tmp" 2>/dev/null; then
        /bin/rm -f "$tmp"
        return 1
    fi
    /bin/cp "$tmp" "$CONF"
    rc=$?
    /bin/rm -f "$tmp"
    return $rc
}

rc=0
repair "" || rc=1
repair ".dev" || rc=1

/sbin/pfctl -f "$CONF" 2>/dev/null || rc=1
/sbin/pfctl -e 2>/dev/null
exit $rc
`

// pfEnsureScriptBody returns the static repair script installed at
// pfEnsureScriptPath and executed by reloadPf via sudo.
func pfEnsureScriptBody() string { return pfEnsureScript }

// PfReloadDaemonPath returns the on-disk plist path. macOS-only; on
// other platforms returns "" since we don't ship this daemon there.
func PfReloadDaemonPath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return "/Library/LaunchDaemons/" + PfReloadDaemonLabel + ".plist"
}

// pfReloadDaemonPlist is the static plist body. No template params — the
// repair script lives at a fixed root-owned path. WatchPaths makes launchd
// re-run the script the moment anything rewrites /etc/pf.conf (e.g. a
// macOS update reverting it), closing the gap between updates and reboots.
// The script's one self-write converges: the re-trigger finds the lines
// present and only reloads.
const pfReloadDaemonPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>dev.treeline.pfreload</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/sh</string>
        <string>/Library/Application Support/dev.git-treeline/pf-ensure.sh</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>WatchPaths</key>
    <array>
        <string>/etc/pf.conf</string>
    </array>
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

// IsPfReloadDaemonCurrent reports whether the installed daemon matches
// what this version of gtl would install — both the plist and the repair
// script, byte for byte. False for pre-script installs (whose plist ran
// `pfctl -ef` and could not survive a macOS update reverting pf.conf),
// signalling that `gtl serve install` should upgrade the daemon.
// Read-only; both files are world-readable.
func IsPfReloadDaemonCurrent() bool {
	path := PfReloadDaemonPath()
	if path == "" {
		return false
	}
	plist, err := os.ReadFile(path)
	if err != nil || string(plist) != pfReloadDaemonPlist {
		return false
	}
	script, err := os.ReadFile(pfEnsureScriptPath)
	return err == nil && string(script) == pfEnsureScript
}

// pfReloadDaemonPlistBody returns the static plist body installed at
// PfReloadDaemonPath().
func pfReloadDaemonPlistBody() string { return pfReloadDaemonPlist }

// pfReloadDaemonInstallFragment returns a `sh -c` fragment that, when run
// as root, installs the repair script (root:wheel 755 — root must never
// execute a user-writable file) and the plist, then bootstraps the
// LaunchDaemon. The fragment ends with `launchctl bootstrap`, whose exit
// code becomes the fragment's exit code — so a failed bootstrap surfaces
// as a non-zero exit. The caller writes pfReloadDaemonPlist and
// pfEnsureScript to the two temp paths before invoking the script.
func pfReloadDaemonInstallFragment(tmpPlistPath, tmpScriptPath string) string {
	target := PfReloadDaemonPath()
	scriptDir := "/Library/Application Support/dev.git-treeline"
	return fmt.Sprintf(
		"/bin/mkdir -p '%s' && "+
			"/bin/cp '%s' '%s' && "+
			"/usr/sbin/chown root:wheel '%s' && "+
			"/bin/chmod 755 '%s' && "+
			"/bin/cp '%s' '%s' && "+
			"/usr/sbin/chown root:wheel '%s' && "+
			"/bin/chmod 644 '%s' && "+
			"(/bin/launchctl bootout system/%s 2>/dev/null; true) && "+
			"/bin/launchctl bootstrap system '%s'",
		scriptDir,
		tmpScriptPath, pfEnsureScriptPath,
		pfEnsureScriptPath,
		pfEnsureScriptPath,
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

	tmpPlist, tmpScript, cleanup, err := writePfDaemonTempFiles()
	if err != nil {
		return err
	}
	defer cleanup()

	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to install the boot-time pf reloader: ",
		"sh", "-c", pfReloadDaemonInstallFragment(tmpPlist, tmpScript))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pf-reload daemon install failed: %w", err)
	}
	return nil
}

// writePfDaemonTempFiles renders the daemon plist and repair script to
// temp files for a root install fragment to copy into place. The caller
// must invoke cleanup (safe even on partial failure).
func writePfDaemonTempFiles() (tmpPlistPath, tmpScriptPath string, cleanup func(), err error) {
	var paths []string
	cleanup = func() {
		for _, p := range paths {
			_ = os.Remove(p)
		}
	}
	write := func(pattern, content string) (string, error) {
		f, err := os.CreateTemp("", pattern)
		if err != nil {
			return "", err
		}
		paths = append(paths, f.Name())
		if _, err := f.WriteString(content); err != nil {
			_ = f.Close()
			return "", err
		}
		return f.Name(), f.Close()
	}
	if tmpPlistPath, err = write("treeline-pfreload-*.plist", pfReloadDaemonPlist); err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("creating temp plist: %w", err)
	}
	if tmpScriptPath, err = write("treeline-pf-ensure-*.sh", pfEnsureScript); err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("creating temp pf-ensure script: %w", err)
	}
	return tmpPlistPath, tmpScriptPath, cleanup, nil
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
			"/bin/rm -f '%s' '%s'",
		PfReloadDaemonLabel,
		target,
		pfEnsureScriptPath,
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
