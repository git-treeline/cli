package service

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/git-treeline/git-treeline/internal/platform"
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

// IsPortForwardConfigured checks whether the port 443 redirect is in place.
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

func isLinuxPortForwardConfigured() bool {
	out, err := exec.Command("iptables", "-t", "nat", "-L", "OUTPUT", "-n",
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

	if strings.Contains(string(pfConf), pfMarker()) {
		fmt.Println("  Port forwarding already configured (443 → router).")
		return reloadPf()
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

	script := fmt.Sprintf(
		"cp '%s' '%s' && mkdir -p /etc/pf.anchors && cp '%s' '%s' && cp '%s' '%s' && pfctl -ef '%s' 2>/dev/null; true",
		pfConfPath, pfBackupPath,
		tmpAnchor.Name(), pfAnchorPath(),
		tmpPfConf.Name(), pfConfPath,
		pfConfPath,
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

// reloadPf ensures the kernel's pf rules match /etc/pf.conf. The config
// file can have rules that the kernel doesn't — e.g. after a failed
// uninstall or if pfctl was never invoked after writing.
func reloadPf() error {
	cmd := exec.Command("sudo", "-p",
		"\nEnter your password to reload port forwarding: ",
		"pfctl", "-ef", pfConfPath)
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
		"cp '%s' '%s' && rm -f '%s' && pfctl -f '%s' 2>/dev/null; true",
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
		"iptables", "-t", "nat", "-A", "OUTPUT",
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
		out, err := exec.Command("iptables", "-t", "nat", "-L", "OUTPUT", "-n",
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
				"iptables", "-t", "nat", "-D", "OUTPUT", fields[0]).Run()
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
