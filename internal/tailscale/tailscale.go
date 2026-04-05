// Package tailscale provides helpers for detecting and interacting with the
// Tailscale CLI. Used by gtl share --tailscale to expose ports on the tailnet.
package tailscale

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Overridable for tests.
var (
	lookPath = exec.LookPath
	statFile = os.Stat
	runCmd   = defaultRunCmd
	cmdOut   = defaultCmdOut
)

func defaultRunCmd(name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func defaultCmdOut(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

const macAppPath = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

// ResolveTailscale returns the path to the tailscale CLI binary, checking PATH
// first, then the macOS app bundle location.
func ResolveTailscale() (string, error) {
	if path, err := lookPath("tailscale"); err == nil {
		return path, nil
	}
	if _, err := statFile(macAppPath); err == nil {
		return macAppPath, nil
	}
	return "", fmt.Errorf("tailscale not found\n  Install: brew install --cask tailscale")
}

type statusResponse struct {
	Self struct {
		DNSName string `json:"DNSName"`
		Online  bool   `json:"Online"`
	} `json:"Self"`
}

// ParseStatus unmarshals tailscale status --json output. Exported for testing.
func ParseStatus(data []byte) (dnsName string, online bool, err error) {
	var status statusResponse
	if err := json.Unmarshal(data, &status); err != nil {
		return "", false, fmt.Errorf("failed to parse tailscale status: %w", err)
	}
	return strings.TrimSuffix(status.Self.DNSName, "."), status.Self.Online, nil
}

// Preflight checks all prerequisites for tailscale serve and returns an
// actionable error if anything is missing. On success returns the DNS name.
func Preflight() (dnsName string, err error) {
	tsPath, err := ResolveTailscale()
	if err != nil {
		return "", err
	}

	out, cmdErr := cmdOut(tsPath, "status", "--json")
	if cmdErr != nil {
		return "", fmt.Errorf("tailscale daemon is not running\n  Open the Tailscale app and log in, then retry")
	}

	name, online, parseErr := ParseStatus(out)
	if parseErr != nil {
		return "", parseErr
	}
	if !online {
		return "", fmt.Errorf("tailscale is installed but not connected\n  Open the Tailscale menu bar icon and toggle it on")
	}
	if name == "" {
		return "", fmt.Errorf("tailscale returned an empty hostname\n  Log out and log back in: tailscale logout && tailscale login")
	}

	return name, nil
}

// IsRunning checks whether the Tailscale daemon is up and connected.
func IsRunning() bool {
	_, err := Preflight()
	return err == nil
}

// GetDNSName returns the machine's MagicDNS hostname (e.g. "macbook.tail1234.ts.net").
func GetDNSName() (string, error) {
	return Preflight()
}

// Serve starts serving a local port over HTTPS on the tailnet in background
// mode. Returns the combined stdout on success. Detects known errors (serve
// not enabled, not logged in) and returns actionable messages.
func Serve(port int) error {
	tsPath, err := ResolveTailscale()
	if err != nil {
		return err
	}
	target := fmt.Sprintf("https+insecure://localhost:%d", port)
	stdout, stderr, runErr := runCmd(tsPath, "serve", "--bg", target)

	combined := string(stdout) + string(stderr)

	if runErr != nil {
		if strings.Contains(combined, "Serve is not enabled") {
			url := extractEnableURL(combined)
			msg := "Tailscale Serve is not enabled on your tailnet"
			if url != "" {
				msg += "\n  Enable it: " + url
			} else {
				msg += "\n  Enable it in the Tailscale admin console under DNS > HTTPS Certificates"
			}
			return fmt.Errorf("%s", msg)
		}
		if strings.Contains(combined, "not logged in") {
			return fmt.Errorf("tailscale is not logged in\n  Run: tailscale login")
		}
		return fmt.Errorf("tailscale serve failed: %s", strings.TrimSpace(combined))
	}

	if len(stdout) > 0 {
		fmt.Print(string(stdout))
	}
	return nil
}

// ServeOff removes the tailscale serve configuration.
func ServeOff() error {
	tsPath, err := ResolveTailscale()
	if err != nil {
		return err
	}
	_, _, runErr := runCmd(tsPath, "serve", "off")
	return runErr
}

func extractEnableURL(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "https://login.tailscale.com/") {
			return trimmed
		}
	}
	return ""
}
