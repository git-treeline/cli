// Package service manages the git-treeline router as a system service.
// Supports macOS LaunchAgents and Linux systemd user units.
//
// When GTL_HOME is set, labels and paths are suffixed with ".dev" to avoid
// colliding with the production install.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/git-treeline/cli/internal/platform"
)

// runCmd executes a command and returns its error. Overridable for tests so
// service-management code can be exercised without touching launchctl/systemctl.
var runCmd = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// runCmdOutput executes a command and returns combined stdout+stderr.
// Overridable for tests.
var runCmdOutput = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// nowFn / sleepFn are overridable so bounce-verification can be tested
// without real time passing.
var (
	nowFn   = time.Now
	sleepFn = time.Sleep
)

// DefaultBounceTimeout is the default deadline for verifying that a bounced
// router has come back up (i.e. written its version file).
const DefaultBounceTimeout = 5 * time.Second

// StableExecutablePath returns a path suitable for embedding in a service
// definition (launchd plist, systemd unit). On Homebrew installs,
// os.Executable() resolves symlinks and returns a versioned Cellar path
// (e.g. /opt/homebrew/Cellar/git-treeline/0.38.0/bin/gtl) that breaks
// after `brew upgrade`. This function detects that case and returns the
// stable symlink path instead (e.g. /opt/homebrew/bin/gtl).
func StableExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return resolveStablePath(exe), nil
}

// resolveStablePath checks if the binary lives inside a Homebrew Cellar
// or similar versioned directory, and returns the symlink in the
// corresponding bin directory if one exists pointing to the same binary
// name. Otherwise returns the original path unchanged.
func resolveStablePath(exe string) string {
	dir := filepath.Dir(exe)
	base := filepath.Base(exe)

	// Walk up looking for a "Cellar" component — indicates Homebrew.
	// Structure: <prefix>/Cellar/<formula>/<version>/bin/<binary>
	// Stable symlink: <prefix>/bin/<binary>
	parts := strings.Split(dir, string(filepath.Separator))
	for i, part := range parts {
		if part != "Cellar" {
			continue
		}
		prefix := string(filepath.Separator) + filepath.Join(parts[1:i]...)
		candidate := filepath.Join(prefix, "bin", base)
		if _, err := os.Readlink(candidate); err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		resolvedExe, err := filepath.EvalSymlinks(exe)
		if err != nil {
			continue
		}
		if resolved == resolvedExe {
			return candidate
		}
	}
	return exe
}

const baseLaunchLabel = "dev.treeline.router"
const baseSystemdUnit = "git-treeline-router"

func LaunchLabel() string { return baseLaunchLabel + platform.DevSuffix() }
func SystemdUnit() string { return baseSystemdUnit + platform.DevSuffix() + ".service" }

// Install writes a service definition and activates it.
// Returns the path to the written file.
func Install(gtlPath string, port int) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(gtlPath, port)
	case "linux":
		return installSystemd(gtlPath, port)
	default:
		return "", fmt.Errorf("unsupported platform: %s (macOS and Linux only)", runtime.GOOS)
	}
}

// Uninstall stops the service and removes the definition file.
func Uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchAgent()
	case "linux":
		return uninstallSystemd()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// IsRunning checks if the service is currently active.
func IsRunning() bool {
	switch runtime.GOOS {
	case "darwin":
		out, err := runCmdOutput("launchctl", "list", LaunchLabel())
		return err == nil && len(out) > 0
	case "linux":
		err := runCmd("systemctl", "--user", "is-active", "--quiet", SystemdUnit())
		return err == nil
	default:
		return false
	}
}

// RunningPID returns the PID of the running service process, or 0 if it
// cannot be determined. On macOS, parses `launchctl print` output for the
// authoritative PID launchd has registered for our label. On Linux, reads
// the MainPID from `systemctl --user show`.
func RunningPID() int {
	switch runtime.GOOS {
	case "darwin":
		return runningPIDDarwin()
	case "linux":
		return runningPIDLinux()
	default:
		return 0
	}
}

func runningPIDDarwin() int {
	out, err := runCmdOutput("launchctl", "print", launchTarget())
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Format: "pid = 12345"
		if !strings.HasPrefix(line, "pid") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "pid" && fields[1] == "=" {
			var pid int
			if _, err := fmt.Sscanf(fields[2], "%d", &pid); err == nil {
				return pid
			}
		}
	}
	return 0
}

func runningPIDLinux() int {
	out, err := runCmdOutput("systemctl", "--user", "show", "--property=MainPID", "--value", SystemdUnit())
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid); err == nil {
		return pid
	}
	return 0
}

// launchTarget returns the launchd domain target for our service,
// e.g. "gui/501/dev.treeline.router". Used for kickstart, print, bootout.
func launchTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchLabel())
}

// launchDomain returns just the user domain, e.g. "gui/501". Used for
// `launchctl bootstrap <domain> <plist>`.
func launchDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

// Bounce restarts the running router service so it picks up changes that
// don't require a new service definition (e.g. the `gtl` binary on disk has
// been upgraded by Homebrew, but the launchd-managed process is still the
// old build). On macOS this is `launchctl kickstart -k`; on Linux it is
// `systemctl --user restart`.
//
// Bounce verifies the restart by waiting for the router to write a fresh
// `router.version` file. Returns an error if no new write is observed
// within `wait`.
//
// Use Reload (not Bounce) when the service definition itself changed.
func Bounce(wait time.Duration) error {
	switch runtime.GOOS {
	case "darwin":
		return bounceLaunchAgent(wait)
	case "linux":
		return bounceSystemd(wait)
	default:
		return fmt.Errorf("bounce not supported on %s", runtime.GOOS)
	}
}

// Reload tears down the existing service registration and re-registers it
// from the on-disk service definition. Use this after writing a new plist
// or systemd unit so launchd/systemd actually pick up the changes.
//
// On macOS that's `launchctl bootout` followed by `launchctl bootstrap`.
// On Linux that's `systemctl --user daemon-reload` followed by `restart`.
func Reload(wait time.Duration) error {
	switch runtime.GOOS {
	case "darwin":
		return reloadLaunchAgent(wait)
	case "linux":
		return reloadSystemd(wait)
	default:
		return fmt.Errorf("reload not supported on %s", runtime.GOOS)
	}
}

// versionFileMtime returns the mtime of the running router's version file,
// or the zero time if the file doesn't exist.
func versionFileMtime() time.Time {
	info, err := os.Stat(RouterVersionFile())
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// waitForFreshVersion polls until the router.version file has been
// (re)written after `before`, or until `wait` elapses.
func waitForFreshVersion(before time.Time, wait time.Duration) error {
	deadline := nowFn().Add(wait)
	for {
		if mt := versionFileMtime(); !mt.IsZero() && mt.After(before) {
			return nil
		}
		if !nowFn().Before(deadline) {
			break
		}
		sleepFn(100 * time.Millisecond)
	}
	return fmt.Errorf("router did not record a new version within %s — check logs at %s", wait, logDir())
}

func bounceLaunchAgent(wait time.Duration) error {
	target := launchTarget()
	before := versionFileMtime()
	// If the agent isn't loaded yet (e.g. fresh install), kickstart will
	// fail. Fall back to bootstrap so first-time callers don't hit a wall.
	if err := runCmd("launchctl", "print", target); err != nil {
		return reloadLaunchAgent(wait)
	}
	if err := runCmd("launchctl", "kickstart", "-k", target); err != nil {
		return fmt.Errorf("launchctl kickstart: %w", err)
	}
	return waitForFreshVersion(before, wait)
}

func reloadLaunchAgent(wait time.Duration) error {
	plist := PlistPath()
	if _, err := os.Stat(plist); err != nil {
		return fmt.Errorf("plist not found at %s — run 'gtl serve install' first", plist)
	}
	target := launchTarget()
	before := versionFileMtime()
	// bootout removes the existing registration. Errors are swallowed
	// because it returns non-zero when the service isn't currently
	// loaded — that's fine, we're about to bootstrap.
	_ = runCmd("launchctl", "bootout", target)
	if err := runCmd("launchctl", "bootstrap", launchDomain(), plist); err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %w", plist, err)
	}
	return waitForFreshVersion(before, wait)
}

func bounceSystemd(wait time.Duration) error {
	before := versionFileMtime()
	if err := runCmd("systemctl", "--user", "restart", SystemdUnit()); err != nil {
		return fmt.Errorf("systemctl restart: %w", err)
	}
	return waitForFreshVersion(before, wait)
}

func reloadSystemd(wait time.Duration) error {
	before := versionFileMtime()
	if err := runCmd("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := runCmd("systemctl", "--user", "restart", SystemdUnit()); err != nil {
		return fmt.Errorf("systemctl restart: %w", err)
	}
	return waitForFreshVersion(before, wait)
}

// RouterVersionFile returns the path to the file where the running router
// records its version on startup.
func RouterVersionFile() string {
	return filepath.Join(platform.ConfigDir(), "router.version")
}

// WriteRouterVersion writes the current version to the version file.
// Called by the router on startup.
func WriteRouterVersion(version string) {
	_ = os.MkdirAll(platform.ConfigDir(), platform.DirMode)
	_ = os.WriteFile(RouterVersionFile(), []byte(version), platform.PrivateFileMode)
}

// RunningRouterVersion reads the version recorded by the running router.
// Returns "" if the file doesn't exist or can't be read.
func RunningRouterVersion() string {
	data, err := os.ReadFile(RouterVersionFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// --- macOS LaunchAgent ---

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{ .Label }}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{ .GtlPath }}</string>
		<string>serve</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{ .LogDir }}/router.log</string>
	<key>StandardErrorPath</key>
	<string>{{ .LogDir }}/router.err</string>
</dict>
</plist>
`))

func PlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", LaunchLabel()+".plist")
}

func logDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "git-treeline")
}

func installLaunchAgent(gtlPath string, _ int) (string, error) {
	path := PlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(logDir(), 0o755); err != nil {
		return "", err
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}

	err = plistTemplate.Execute(f, struct {
		Label   string
		GtlPath string
		LogDir  string
	}{
		Label:   LaunchLabel(),
		GtlPath: gtlPath,
		LogDir:  logDir(),
	})
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}

	if err := Reload(DefaultBounceTimeout); err != nil {
		return path, fmt.Errorf("wrote plist but service did not come up: %w", err)
	}
	return path, nil
}

func uninstallLaunchAgent() error {
	path := PlistPath()
	// Best-effort bootout — non-zero exit when not loaded is fine.
	_ = runCmd("launchctl", "bootout", launchTarget())
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InstalledBinaryPath reads the LaunchAgent plist (or systemd unit) and
// returns the binary path embedded in the service definition. Returns ""
// if the service file doesn't exist or can't be parsed.
func InstalledBinaryPath() string {
	switch runtime.GOOS {
	case "darwin":
		return installedBinaryFromPlist()
	case "linux":
		return installedBinaryFromUnit()
	default:
		return ""
	}
}

func installedBinaryFromPlist() string {
	data, err := os.ReadFile(PlistPath())
	if err != nil {
		return ""
	}
	content := string(data)
	const startTag = "<key>ProgramArguments</key>"
	idx := strings.Index(content, startTag)
	if idx < 0 {
		return ""
	}
	rest := content[idx:]
	const strStart = "<string>"
	const strEnd = "</string>"
	si := strings.Index(rest, strStart)
	if si < 0 {
		return ""
	}
	rest = rest[si+len(strStart):]
	ei := strings.Index(rest, strEnd)
	if ei < 0 {
		return ""
	}
	return rest[:ei]
}

func installedBinaryFromUnit() string {
	data, err := os.ReadFile(UnitPath())
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			parts := strings.Fields(strings.TrimPrefix(line, "ExecStart="))
			if len(parts) > 0 {
				return parts[0]
			}
		}
	}
	return ""
}

// --- Linux systemd user unit ---

var unitTemplate = template.Must(template.New("unit").Parse(`[Unit]
Description=git-treeline subdomain router

[Service]
ExecStart={{ .GtlPath }} serve run
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
`))

func UnitPath() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "systemd", "user", SystemdUnit())
}

func installSystemd(gtlPath string, _ int) (string, error) {
	path := UnitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}

	err = unitTemplate.Execute(f, struct{ GtlPath string }{GtlPath: gtlPath})
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}

	if err := runCmd("systemctl", "--user", "daemon-reload"); err != nil {
		return path, fmt.Errorf("wrote unit but daemon-reload failed: %w", err)
	}
	if err := runCmd("systemctl", "--user", "enable", "--now", SystemdUnit()); err != nil {
		return path, fmt.Errorf("wrote unit but failed to enable: %w", err)
	}
	if err := Reload(DefaultBounceTimeout); err != nil {
		return path, fmt.Errorf("wrote unit but service did not come up: %w", err)
	}
	return path, nil
}

func uninstallSystemd() error {
	_ = runCmd("systemctl", "--user", "disable", "--now", SystemdUnit())
	path := UnitPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = runCmd("systemctl", "--user", "daemon-reload")
	return nil
}

// GeneratePlist returns the plist XML content as a string (for testing).
func GeneratePlist(gtlPath string) (string, error) {
	var b strings.Builder
	err := plistTemplate.Execute(&b, struct {
		Label   string
		GtlPath string
		LogDir  string
	}{
		Label:   LaunchLabel(),
		GtlPath: gtlPath,
		LogDir:  logDir(),
	})
	return b.String(), err
}

// GenerateUnit returns the systemd unit content as a string (for testing).
func GenerateUnit(gtlPath string) (string, error) {
	var b strings.Builder
	err := unitTemplate.Execute(&b, struct{ GtlPath string }{GtlPath: gtlPath})
	return b.String(), err
}
