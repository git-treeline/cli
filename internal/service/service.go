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

	"github.com/git-treeline/git-treeline/internal/platform"
)

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
		out, err := exec.Command("launchctl", "list", LaunchLabel()).CombinedOutput()
		return err == nil && len(out) > 0
	case "linux":
		err := exec.Command("systemctl", "--user", "is-active", "--quiet", SystemdUnit()).Run()
		return err == nil
	default:
		return false
	}
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

	_ = exec.Command("launchctl", "unload", path).Run()
	if err := exec.Command("launchctl", "load", path).Run(); err != nil {
		return path, fmt.Errorf("wrote plist but failed to load: %w", err)
	}
	return path, nil
}

func uninstallLaunchAgent() error {
	path := PlistPath()
	_ = exec.Command("launchctl", "unload", path).Run()
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

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if err := exec.Command("systemctl", "--user", "enable", "--now", SystemdUnit()).Run(); err != nil {
		return path, fmt.Errorf("wrote unit but failed to enable: %w", err)
	}
	return path, nil
}

func uninstallSystemd() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", SystemdUnit()).Run()
	path := UnitPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
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
