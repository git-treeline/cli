package service

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/git-treeline/cli/internal/platform"
)

const (
	basePfAnchorName = "dev.treeline.router"
	pfConfPath       = "/etc/pf.conf"
	pfBackupPath     = "/etc/pf.conf.bak.treeline"
	basePfMarker     = "# git-treeline"
)

func pfAnchorName() string { return basePfAnchorName + platform.DevSuffix() }
func pfAnchorPath() string { return "/etc/pf.anchors/" + pfAnchorName() }
func pfMarker() string     { return basePfMarker + platform.DevSuffix() }

// InstallPortForward sets up an OS-level redirect from port 443 to the
// router port so users can access worktrees at https://{branch}.localhost
// without typing a port number. Requires sudo.
func InstallPortForward(routerPort int) error {
	switch runtime.GOOS {
	case "darwin":
		return installDarwinPortForward(routerPort)
	case "linux":
		return installLinuxPortForward(routerPort)
	default:
		return fmt.Errorf("port forwarding not supported on %s", runtime.GOOS)
	}
}

// UninstallPortForward removes the OS-level port 443 redirect.
func UninstallPortForward() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallDarwinPortForward()
	case "linux":
		return uninstallLinuxPortForward()
	default:
		return nil
	}
}

// IsPortForwardConfigured checks whether the port 443 redirect is in place
// on disk (pf.conf or iptables rules saved). Note: this can return true when
// the rules are not actually loaded into the kernel — see PortForwardActive
// for an authoritative check.
func IsPortForwardConfigured() bool {
	switch runtime.GOOS {
	case "darwin":
		data, err := os.ReadFile(pfConfPath)
		if err != nil {
			return false
		}
		return strings.Contains(string(data), pfMarker())
	case "linux":
		return isLinuxPortForwardConfigured()
	default:
		return false
	}
}

// PortForwardStatus reports whether the port-forwarding rules are not just
// configured on disk but actually loaded in the kernel right now. This is
// what determines whether traffic to :443 will reach the router.
type PortForwardStatus struct {
	// ConfiguredOnDisk is true when pf.conf (macOS) or saved iptables rules
	// (Linux) reference our redirect.
	ConfiguredOnDisk bool
	// LoadedInKernel is true when the running ruleset includes our redirect.
	// On macOS this requires both pf to be enabled AND our rdr rule to be
	// in the active anchor.
	LoadedInKernel bool
	// PfEnabled (macOS only) reports the running pf status. Meaningful only
	// when PfStateKnown is true. Meaningless on Linux.
	PfEnabled bool
	// PfStateKnown (macOS only) is true when `pfctl -s info` succeeded —
	// without it, callers must not interpret PfEnabled=false as proof that
	// pf is actually disabled. On modern macOS, `pfctl -s info` may
	// require sudo; without it, returns an error.
	PfStateKnown bool
	// KernelStateKnown is true when we successfully queried the kernel rule
	// list. On macOS, `pfctl -s nat` (and per-anchor variants) typically
	// require sudo — if our calls fail with permission errors, this is
	// false, and LoadedInKernel should not be trusted as authoritative.
	KernelStateKnown bool
	// Detail is a one-line human-readable summary, or "" when everything is
	// healthy.
	Detail string
}

// CheckPortForward queries the running kernel state of our port-forwarding
// rules. Requires sudo on macOS for full accuracy; without sudo, falls back
// to the on-disk check and returns LoadedInKernel = false (unknown).
//
// routerPort is the port the rdr should forward to; used to detect a rule
// that exists but points to the wrong port.
func CheckPortForward(routerPort int) PortForwardStatus {
	switch runtime.GOOS {
	case "darwin":
		return checkPortForwardDarwin(routerPort)
	case "linux":
		return checkPortForwardLinux(routerPort)
	default:
		return PortForwardStatus{}
	}
}

func checkPortForwardDarwin(routerPort int) PortForwardStatus {
	st := PortForwardStatus{ConfiguredOnDisk: IsPortForwardConfigured()}

	// pfctl -s info reports the daemon's enabled/disabled state. This call
	// MAY require sudo on modern macOS — when it fails we record
	// PfStateKnown=false so callers don't misread the missing answer as
	// "pf is disabled."
	if out, err := runCmdOutput("/sbin/pfctl", "-s", "info"); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Status: Enabled") {
				st.PfEnabled = true
				st.PfStateKnown = true
				break
			}
			if strings.HasPrefix(trimmed, "Status: Disabled") {
				st.PfEnabled = false
				st.PfStateKnown = true
				break
			}
		}
	}

	// pfctl -a treeline -s nat lists the rules in our anchor. Both this and
	// the main `pfctl -s nat` typically require sudo on modern macOS, so we
	// track whether either query actually succeeded — if neither does we
	// can't prove or disprove "loaded in kernel."
	wantSubstr := fmt.Sprintf("port %d", routerPort)
	if out, err := runCmdOutput("/sbin/pfctl", "-a", pfAnchorName(), "-s", "nat"); err == nil {
		st.KernelStateKnown = true
		if strings.Contains(string(out), wantSubstr) {
			st.LoadedInKernel = true
		}
	}
	if !st.LoadedInKernel {
		if out, err := runCmdOutput("/sbin/pfctl", "-s", "nat"); err == nil {
			st.KernelStateKnown = true
			if strings.Contains(string(out), wantSubstr) {
				st.LoadedInKernel = true
			}
		}
	}

	switch {
	case !st.ConfiguredOnDisk:
		st.Detail = "not configured (run 'gtl serve install')"
	case st.PfStateKnown && !st.PfEnabled:
		st.Detail = "pf disabled — rules will not apply (run 'gtl serve reload-pf')"
	case !st.PfStateKnown && !st.KernelStateKnown:
		st.Detail = "pf state not readable without sudo — verify with 'sudo pfctl -s info'"
	case !st.KernelStateKnown:
		st.Detail = "kernel ruleset not readable without sudo — verify with 'sudo pfctl -s nat'"
	case !st.LoadedInKernel:
		st.Detail = fmt.Sprintf("rule not loaded in kernel — pf.conf has it but `pfctl -s nat` doesn't show port %d (run 'gtl serve reload-pf')", routerPort)
	}
	return st
}

func checkPortForwardLinux(routerPort int) PortForwardStatus {
	st := PortForwardStatus{ConfiguredOnDisk: IsPortForwardConfigured()}
	out, err := runCmdOutput("/sbin/iptables", "-t", "nat", "-L", "OUTPUT", "-n")
	if err != nil {
		if !st.ConfiguredOnDisk {
			st.Detail = "not configured"
		} else {
			st.Detail = "could not read iptables (need root or CAP_NET_ADMIN)"
		}
		return st
	}
	st.KernelStateKnown = true
	body := string(out)
	if strings.Contains(body, "git-treeline") &&
		strings.Contains(body, fmt.Sprintf("ports %d", routerPort)) {
		st.LoadedInKernel = true
	}
	if !st.LoadedInKernel && st.ConfiguredOnDisk {
		st.Detail = "iptables rule missing in current ruleset (run 'gtl serve reload-pf')"
	}
	return st
}

// ReloadPortForward re-applies the port-forwarding rules from disk into the
// running kernel ruleset. Useful after a reboot/network change wiped them
// without changing pf.conf or iptables-save state.
//
// macOS requires sudo. Linux uses the same mechanism as install (currently
// re-running iptables rules); we delegate to the same install path because
// it is idempotent.
func ReloadPortForward() error {
	switch runtime.GOOS {
	case "darwin":
		return reloadPf()
	case "linux":
		// Linux installs iptables rules directly; reapplying them is the
		// only "reload" available. The install function already short-
		// circuits when rules are present, but we want it to reapply
		// unconditionally — the linux install path is idempotent so we
		// just call it again.
		// We don't have the routerPort here, so the caller should use
		// InstallPortForward(routerPort) instead. ReloadPortForward is
		// macOS-focused; on Linux we return a hint.
		return fmt.Errorf("on Linux, run 'gtl serve install' to reapply iptables rules")
	default:
		return fmt.Errorf("port forwarding not supported on %s", runtime.GOOS)
	}
}

func isLinuxPortForwardConfigured() bool {
	out, err := exec.Command("/sbin/iptables", "-t", "nat", "-L", "OUTPUT", "-n",
		"--line-numbers").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "git-treeline")
}

// --- macOS (pf) ---

func installDarwinPortForward(routerPort int) error {
	pfConf, err := os.ReadFile(pfConfPath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", pfConfPath, err)
	}

	// Check both pf.conf AND the anchor file exist — if anchor is missing,
	// we need to recreate it even if pf.conf has the marker.
	anchorExists := true
	if _, err := os.Stat(pfAnchorPath()); os.IsNotExist(err) {
		anchorExists = false
	}
	pfConfigured := strings.Contains(string(pfConf), pfMarker()) && anchorExists
	daemonInstalled := IsPfReloadDaemonInstalled()

	if pfConfigured && daemonInstalled {
		fmt.Println("  Port forwarding already configured (443 → router).")
		return reloadPf()
	}

	if pfConfigured && !daemonInstalled {
		// Rules are on disk but the boot-time reloader isn't — typical for
		// installs that predate the daemon, or where a previous install
		// silently lost the second sudo prompt. Reload kernel state and
		// drop the daemon in one sudo session.
		fmt.Println("  Port forwarding already configured (443 → router); installing boot-time reloader.")
		return reloadPfAndInstallDaemon()
	}

	anchorContent := fmt.Sprintf(
		"rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 443 -> 127.0.0.1 port %d\n",
		routerPort,
	)

	modifiedPfConf := insertPfRules(string(pfConf))

	tmpAnchor, err := os.CreateTemp("", "treeline-anchor-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpAnchor.Name()) }()
	if _, err := fmt.Fprint(tmpAnchor, anchorContent); err != nil {
		return err
	}
	_ = tmpAnchor.Close()

	tmpPfConf, err := os.CreateTemp("", "treeline-pfconf-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPfConf.Name()) }()
	if _, err := fmt.Fprint(tmpPfConf, modifiedPfConf); err != nil {
		return err
	}
	_ = tmpPfConf.Close()

	tmpPlist, err := os.CreateTemp("", "treeline-pfreload-*.plist")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPlist.Name()) }()
	if _, err := tmpPlist.WriteString(pfReloadDaemonPlistBody()); err != nil {
		return err
	}
	_ = tmpPlist.Close()

	// One sudo session for everything that requires root: validate the new
	// pf.conf, swap it in, apply the rules, and install the boot-time
	// reloader. Bundling these together means a single password prompt and
	// guarantees the daemon either lands on disk or the install fails
	// loudly — no silent half-installs.
	script := fmt.Sprintf(
		"/bin/mkdir -p /etc/pf.anchors && /bin/cp '%s' '%s' && /sbin/pfctl -n -f '%s' 2>&1 || exit 1; "+
			"/bin/cp '%s' '%s' && /bin/cp '%s' '%s' && (/sbin/pfctl -ef '%s' 2>/dev/null; true) && %s",
		tmpAnchor.Name(), pfAnchorPath(),
		tmpPfConf.Name(),
		pfConfPath, pfBackupPath,
		tmpPfConf.Name(), pfConfPath,
		pfConfPath,
		pfReloadDaemonInstallFragment(tmpPlist.Name()),
	)

	cmd := exec.Command("sudo", "-p",
		"\nEnter your password (2 of 2): ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("port forwarding setup failed: %w", err)
	}

	fmt.Printf("  Port forwarding configured (443 → %d).\n", routerPort)
	return nil
}

// reloadPfAndInstallDaemon re-applies pf rules from /etc/pf.conf and
// installs the boot-time reloader in a single sudo invocation. Used when
// the user is already configured for port forwarding but the LaunchDaemon
// is missing — e.g. upgrading from a pre-daemon version, or recovering
// from a previous install where the daemon's separate sudo prompt
// silently failed.
func reloadPfAndInstallDaemon() error {
	tmpPlist, err := os.CreateTemp("", "treeline-pfreload-*.plist")
	if err != nil {
		return fmt.Errorf("creating temp plist: %w", err)
	}
	defer func() { _ = os.Remove(tmpPlist.Name()) }()
	if _, err := tmpPlist.WriteString(pfReloadDaemonPlistBody()); err != nil {
		return err
	}
	if err := tmpPlist.Close(); err != nil {
		return err
	}

	// pfctl -e returns non-zero when pf is already enabled, so we mask its
	// exit code via `; true`. The daemon install fragment is the gate for
	// the overall exit code — if bootstrap fails, the script exits non-zero
	// and the caller surfaces it as an error.
	script := fmt.Sprintf(
		"(/sbin/pfctl -f '%s' 2>/dev/null && /sbin/pfctl -e 2>/dev/null; true) && %s",
		pfConfPath,
		pfReloadDaemonInstallFragment(tmpPlist.Name()),
	)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to reload port forwarding and install the boot-time reloader: ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pf reload + daemon install failed: %w", err)
	}
	return nil
}

// reloadPf ensures the kernel's pf rules match /etc/pf.conf. The reload
// uses -f (load rules) separately from -e (enable pf) because pfctl
// returns exit 1 from -e when pf is already running.
func reloadPf() error {
	script := fmt.Sprintf(
		"/sbin/pfctl -f '%s' 2>/dev/null && { /sbin/pfctl -e 2>/dev/null; true; }",
		pfConfPath,
	)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to reload port forwarding: ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pfctl reload failed: %w", err)
	}
	return nil
}

func uninstallDarwinPortForward() error {
	data, err := os.ReadFile(pfConfPath)
	if err != nil || !strings.Contains(string(data), pfMarker()) {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.Contains(line, pfMarker()) {
			filtered = append(filtered, line)
		}
	}
	cleaned := strings.Join(filtered, "\n")

	tmpPfConf, err := os.CreateTemp("", "treeline-pfconf-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPfConf.Name()) }()
	if _, err := fmt.Fprint(tmpPfConf, cleaned); err != nil {
		return err
	}
	_ = tmpPfConf.Close()

	script := fmt.Sprintf(
		"/bin/cp '%s' '%s' && /bin/rm -f '%s' && /sbin/pfctl -f '%s' 2>/dev/null; true",
		tmpPfConf.Name(), pfConfPath,
		pfAnchorPath(),
		pfConfPath,
	)

	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to remove port forwarding: ",
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// insertPfRules adds the git-treeline rdr-anchor and load anchor lines
// to pf.conf content, placing them in the correct order relative to
// existing rules.
func insertPfRules(pfConf string) string {
	lines := strings.Split(pfConf, "\n")
	rdrLine := fmt.Sprintf(`rdr-anchor "%s" %s`, pfAnchorName(), pfMarker())
	loadLine := fmt.Sprintf(`load anchor "%s" from "%s" %s`, pfAnchorName(), pfAnchorPath(), pfMarker())

	lastRdrAnchor := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "rdr-anchor") {
			lastRdrAnchor = i
		}
	}

	var result []string
	if lastRdrAnchor >= 0 {
		for i, line := range lines {
			result = append(result, line)
			if i == lastRdrAnchor {
				result = append(result, rdrLine)
			}
		}
	} else {
		result = append([]string{rdrLine}, lines...)
	}

	result = append(result, loadLine)
	out := strings.Join(result, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// --- Linux (iptables) ---

func installLinuxPortForward(routerPort int) error {
	portStr := fmt.Sprintf("%d", routerPort)
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password (2 of 2): ",
		"/sbin/iptables", "-t", "nat", "-A", "OUTPUT",
		"-p", "tcp", "-d", "127.0.0.1", "--dport", "443",
		"-j", "REDIRECT", "--to-port", portStr,
		"-m", "comment", "--comment", "git-treeline")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("port forwarding setup failed: %w", err)
	}

	fmt.Printf("  Port forwarding configured (443 → %d).\n", routerPort)
	return nil
}

func uninstallLinuxPortForward() error {
	for {
		out, err := exec.Command("/sbin/iptables", "-t", "nat", "-L", "OUTPUT", "-n",
			"--line-numbers").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "git-treeline") {
			break
		}
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.Contains(line, "git-treeline") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			_ = exec.Command("sudo", "-p",
				"\nEnter your password to remove port forwarding: ",
				"/sbin/iptables", "-t", "nat", "-D", "OUTPUT", fields[0]).Run()
			break
		}
	}
	return nil
}

// GeneratePfAnchor returns the pf anchor content for testing.
func GeneratePfAnchor(routerPort int) string {
	return fmt.Sprintf(
		"rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 443 -> 127.0.0.1 port %d\n",
		routerPort,
	)
}
