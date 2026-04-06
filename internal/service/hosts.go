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
	hostsPath       = "/etc/hosts"
	baseHostsMarker = "git-treeline"
)

func hostsMarker() string { return baseHostsMarker + platform.DevSuffix() }
func hostsBegin() string  { return "# BEGIN " + hostsMarker() }
func hostsEnd() string    { return "# END " + hostsMarker() }

// SyncHosts updates /etc/hosts with entries for the given hostnames.
// macOS Safari does not resolve *.localhost subdomains without explicit
// /etc/hosts entries. Requires sudo.
func SyncHosts(hostnames []string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if len(hostnames) == 0 {
		return CleanHosts()
	}

	block := buildHostsBlock(hostnames)

	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", hostsPath, err)
	}

	content := replaceHostsBlock(string(data), block)
	return writeHosts(content, "update /etc/hosts for Safari support")
}

// CleanHosts removes all git-treeline entries from /etc/hosts.
func CleanHosts() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return nil
	}
	content := string(data)
	if !strings.Contains(content, hostsBegin()) {
		return nil
	}
	cleaned := replaceHostsBlock(content, "")
	return writeHosts(cleaned, "remove git-treeline entries from /etc/hosts")
}

// ManagedHosts returns the hostnames currently in the managed block.
func ManagedHosts() []string {
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return nil
	}
	return parseManagedHosts(string(data))
}

// StaleHosts returns hostnames in the managed block that are not in the
// expected set. Useful for warning users after adding new routes.
func StaleHosts(expected []string) []string {
	managed := ManagedHosts()
	if len(managed) == 0 {
		if len(expected) > 0 {
			return expected
		}
		return nil
	}
	have := make(map[string]bool, len(managed))
	for _, h := range managed {
		have[h] = true
	}
	var stale []string
	for _, h := range expected {
		if !have[h] {
			stale = append(stale, h)
		}
	}
	return stale
}

// NeedsHostsSync reports whether macOS hosts file needs updating for the
// given set of expected hostnames.
func NeedsHostsSync(expected []string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	return len(StaleHosts(expected)) > 0
}

func buildHostsBlock(hostnames []string) string {
	var b strings.Builder
	b.WriteString(hostsBegin() + "\n")
	for _, h := range hostnames {
		fmt.Fprintf(&b, "127.0.0.1 %s\n", h)
	}
	b.WriteString(hostsEnd())
	return b.String()
}

func replaceHostsBlock(content, block string) string {
	begin := hostsBegin()
	end := hostsEnd()

	startIdx := strings.Index(content, begin)
	if startIdx == -1 {
		if block == "" {
			return content
		}
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + block + "\n"
	}

	endIdx := strings.Index(content[startIdx:], end)
	if endIdx == -1 {
		if block == "" {
			return content[:startIdx]
		}
		return content[:startIdx] + block + "\n"
	}
	endIdx = startIdx + endIdx + len(end)
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}

	if block == "" {
		return content[:startIdx] + content[endIdx:]
	}
	return content[:startIdx] + block + "\n" + content[endIdx:]
}

func parseManagedHosts(content string) []string {
	begin := hostsBegin()
	end := hostsEnd()

	startIdx := strings.Index(content, begin)
	if startIdx == -1 {
		return nil
	}
	endIdx := strings.Index(content[startIdx:], end)
	if endIdx == -1 {
		return nil
	}

	block := content[startIdx+len(begin) : startIdx+endIdx]
	var hosts []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			hosts = append(hosts, fields[1])
		}
	}
	return hosts
}

func writeHosts(content, prompt string) error {
	tmp, err := os.CreateTemp("", "treeline-hosts-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := fmt.Fprint(tmp, content); err != nil {
		return err
	}
	_ = tmp.Close()

	script := fmt.Sprintf("cp '%s' '%s'", tmp.Name(), hostsPath)
	cmd := exec.Command("sudo", "-p",
		fmt.Sprintf("\nEnter your password to %s: ", prompt),
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
