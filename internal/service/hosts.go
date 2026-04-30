package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/git-treeline/cli/internal/platform"
)

const (
	hostsPath       = "/etc/hosts"
	baseHostsMarker = "git-treeline"
)

func hostsMarker() string { return baseHostsMarker + platform.DevSuffix() }
func hostsBegin() string  { return "# BEGIN " + hostsMarker() }
func hostsEnd() string    { return "# END " + hostsMarker() }

// SyncHosts updates /etc/hosts with entries for the given hostnames.
// On macOS this fixes Safari's failure to resolve *.localhost. For custom
// TLDs (non-.localhost), this is required on all platforms. Requires sudo.
func SyncHosts(hostnames []string) error {
	if len(hostnames) == 0 {
		return CleanHosts()
	}

	block := buildHostsBlock(hostnames)

	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", hostsPath, err)
	}

	content, err := replaceHostsBlock(string(data), block)
	if err != nil {
		return err
	}
	return writeHosts(content, "update /etc/hosts for Safari support")
}

// CleanHosts removes all git-treeline entries from /etc/hosts.
func CleanHosts() error {
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("could not read %s: %w", hostsPath, err)
	}
	content := string(data)
	if !strings.Contains(content, hostsBegin()) {
		return nil
	}
	cleaned, err := replaceHostsBlock(content, "")
	if err != nil {
		return err
	}
	return writeHosts(cleaned, "remove git-treeline entries from /etc/hosts")
}

// ManagedHosts returns the hostnames currently in the managed block.
func ManagedHosts() ([]string, error) {
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read %s: %w", hostsPath, err)
	}
	return parseManagedHosts(string(data)), nil
}

// MissingHosts returns hostnames from expected that are not yet present in the
// managed /etc/hosts block. If the hosts file cannot be read, all expected
// hostnames are returned (conservative: assume sync is needed).
func MissingHosts(expected []string) []string {
	managed, err := ManagedHosts()
	if err != nil || len(managed) == 0 {
		if len(expected) > 0 {
			return expected
		}
		return nil
	}
	have := make(map[string]bool, len(managed))
	for _, h := range managed {
		have[h] = true
	}
	var missing []string
	for _, h := range expected {
		if !have[h] {
			missing = append(missing, h)
		}
	}
	return missing
}

// NeedsHostsSync reports whether macOS hosts file needs updating for the
// given set of expected hostnames.
func NeedsHostsSync(expected []string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	return len(MissingHosts(expected)) > 0
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

func replaceHostsBlock(content, block string) (string, error) {
	begin := hostsBegin()
	end := hostsEnd()

	startIdx := strings.Index(content, begin)
	if startIdx == -1 {
		if block == "" {
			return content, nil
		}
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + block + "\n", nil
	}

	endIdx := strings.Index(content[startIdx:], end)
	if endIdx == -1 {
		return "", fmt.Errorf("malformed %s: found %q without matching %q — refusing to modify", hostsPath, begin, end)
	}
	endIdx = startIdx + endIdx + len(end)
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}

	if block == "" {
		return content[:startIdx] + content[endIdx:], nil
	}
	return content[:startIdx] + block + "\n" + content[endIdx:], nil
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
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("flushing temp hosts file: %w", err)
	}

	script := fmt.Sprintf("/bin/cp '%s' '%s'", tmp.Name(), hostsPath)
	cmd := exec.Command("sudo", "-p",
		fmt.Sprintf("\nEnter your password to %s: ", prompt),
		"sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
