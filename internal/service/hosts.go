package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/proxy"
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
	if err := validateHostsNames(hostnames); err != nil {
		return err
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

func validateHostsNames(hostnames []string) error {
	for _, host := range hostnames {
		if host == "" || len(host) > 253 {
			return fmt.Errorf("invalid hosts-file hostname %q", host)
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return fmt.Errorf("invalid hosts-file hostname %q", host)
			}
			for _, c := range label {
				if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
					return fmt.Errorf("invalid hosts-file hostname %q", host)
				}
			}
		}
	}
	return nil
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

// NeedsHostsSync reports whether the hosts file needs updating for the given
// set of expected hostnames. Enabled on macOS (Safari cannot resolve
// *.localhost) and Linux (a custom TLD requires /etc/hosts on every platform);
// callers gate the Linux case on a non-localhost domain. Other platforms
// always report false.
func NeedsHostsSync(expected []string) bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		return len(MissingHosts(expected)) > 0
	default:
		return false
	}
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
	script, err := platform.StagePrivilegedFiles([]platform.PrivilegedFile{
		{Name: "hosts", Data: []byte(content)},
	}, "/bin/cp hosts "+platform.ShellQuote(hostsPath))
	if err != nil {
		return err
	}
	return platform.RunPrivilegedScript(fmt.Sprintf("\nEnter your password to %s: ", prompt), script)
}

var ErrHostsPrivilegeRequired = errors.New("hosts privilege required")
var ErrHostsUncertain = errors.New("hosts write outcome uncertain")
var ErrHostsChanged = errors.New("hosts snapshot changed")
var ErrHostsBusy = errors.New("hosts writer busy")

const maxManagedHostsBytes = 1024 * 1024

// ManagedHostsSnapshot binds a strict read to the existing file and exact bytes.
type ManagedHostsSnapshot struct {
	path       string
	info       os.FileInfo
	data       []byte
	hosts      []string
	start, end int
}

func (s ManagedHostsSnapshot) Hostnames() []string { return append([]string(nil), s.hosts...) }

// ReadManagedHostsStrict uses /etc/hosts when path is empty. An absent file is
// distinct from unreadable or malformed managed state; it is never created.
func ReadManagedHostsStrict(path string) (ManagedHostsSnapshot, error) {
	if path == "" {
		path = hostsPath
	}
	s := ManagedHostsSnapshot{path: path, start: -1, end: -1}
	f, info, err := openManagedHostsFile(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	// Read-only cleanup does not change the validated snapshot.
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return s, ErrHostsBusy
		}
		return s, err
	}
	// Closing the descriptor also releases the lock.
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	s.info = info
	s.data, err = io.ReadAll(io.LimitReader(f, maxManagedHostsBytes+1))
	if err != nil {
		return s, err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return s, ErrHostsChanged
	}
	s.hosts, s.start, s.end, err = strictManagedHostsBlock(s.data)
	return s, err
}

func openManagedHostsFile(path string, flags int) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, ErrHostsChanged
	}
	f, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		_ = f.Close() // Preserve the identity-refusal error during cleanup.
		return nil, nil, ErrHostsChanged
	}
	return f, info, nil
}

func strictManagedHostsBlock(data []byte) ([]string, int, int, error) {
	if len(data) > maxManagedHostsBytes || bytes.IndexByte(data, 0) >= 0 {
		return nil, -1, -1, errors.New("invalid hosts content")
	}
	var hosts []string
	start, end, offset := -1, -1, 0
	for _, raw := range strings.SplitAfter(string(data), "\n") {
		line := strings.TrimSpace(raw)
		switch line {
		case hostsBegin():
			if start != -1 {
				return nil, -1, -1, errors.New("duplicate managed hosts marker")
			}
			start = offset
		case hostsEnd():
			if start == -1 || end != -1 {
				return nil, -1, -1, errors.New("unmatched managed hosts marker")
			}
			end = offset + len(raw)
		default:
			marker := strings.HasPrefix(line, "# BEGIN "+baseHostsMarker) || strings.HasPrefix(line, "# END "+baseHostsMarker)
			otherMarker := line == "# BEGIN "+baseHostsMarker || line == "# END "+baseHostsMarker || line == "# BEGIN "+baseHostsMarker+".dev" || line == "# END "+baseHostsMarker+".dev"
			if marker && (!otherMarker || start != -1 && end == -1) {
				return nil, -1, -1, errors.New("malformed managed hosts marker")
			}
			if start != -1 && end == -1 && line != "" && !strings.HasPrefix(line, "#") {
				fields := strings.Fields(line)
				if len(fields) != 2 || fields[0] != "127.0.0.1" || !validManagedHostname(fields[1]) {
					return nil, -1, -1, errors.New("invalid managed hosts entry")
				}
				hosts = append(hosts, fields[1])
			}
		}
		offset += len(raw)
	}
	if start != -1 && end == -1 {
		return nil, -1, -1, errors.New("unclosed managed hosts block")
	}
	return hosts, start, end, nil
}

func validManagedHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if proxy.ValidateAliasName(label) != nil {
			return false
		}
	}
	return true
}

// SyncManagedHostsNoninteractive never invokes sudo or replaces the inode. It
// only writes an existing, opted-in file already writable by this process.
// Flock coordinates strict writers; legacy and external editors do not share
// this guarantee. After any attempted write, failures are conservatively unknown.
func SyncManagedHostsNoninteractive(expected ManagedHostsSnapshot, hosts []string) (resultErr error) {
	if expected.info == nil || expected.start < 0 {
		return ErrHostsChanged
	}
	for _, host := range hosts {
		if !validManagedHostname(host) {
			return errors.New("invalid desired hostname")
		}
	}
	block := ""
	if len(hosts) > 0 {
		block = buildHostsBlock(hosts) + "\n"
	}
	desired := append([]byte(nil), expected.data[:expected.start]...)
	desired = append(desired, block...)
	desired = append(desired, expected.data[expected.end:]...)
	if len(desired) > maxManagedHostsBytes {
		return errors.New("hosts capacity exceeded")
	}
	f, info, err := openManagedHostsFile(expected.path, os.O_RDWR)
	if errors.Is(err, os.ErrPermission) {
		return ErrHostsPrivilegeRequired
	}
	if err != nil {
		return err
	}
	wrote := false
	defer func() {
		if err := f.Close(); err != nil && wrote {
			resultErr = ErrHostsUncertain
		}
	}()
	if !os.SameFile(expected.info, info) {
		return ErrHostsChanged
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return ErrHostsBusy
		}
		return err
	}
	// Closing the descriptor also releases the lock.
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	current, err := io.ReadAll(io.LimitReader(f, maxManagedHostsBytes+1))
	pathInfo, pathErr := os.Lstat(expected.path)
	if err != nil || pathErr != nil || !os.SameFile(info, pathInfo) || !bytes.Equal(current, expected.data) {
		return ErrHostsChanged
	}
	if bytes.Equal(current, desired) {
		return nil
	}
	wrote = true
	if n, err := f.WriteAt(desired, 0); err != nil || n != len(desired) {
		return ErrHostsUncertain
	}
	if err := f.Truncate(int64(len(desired))); err != nil {
		return ErrHostsUncertain
	}
	if err := f.Sync(); err != nil {
		return ErrHostsUncertain
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ErrHostsUncertain
	}
	actual, err := io.ReadAll(io.LimitReader(f, maxManagedHostsBytes+1))
	pathInfo, pathErr = os.Lstat(expected.path)
	if err != nil || pathErr != nil || !os.SameFile(info, pathInfo) || !bytes.Equal(actual, desired) {
		return ErrHostsUncertain
	}
	return nil
}

// CheckManagedHostsWrite checks access to the same file without changing it.
// The actual writer still revalidates bytes and identity under its own lock.
func CheckManagedHostsWrite(expected ManagedHostsSnapshot) error {
	if expected.info == nil || expected.start < 0 {
		return ErrHostsChanged
	}
	file, info, err := openManagedHostsFile(expected.path, os.O_RDWR)
	if errors.Is(err, os.ErrPermission) {
		return ErrHostsPrivilegeRequired
	}
	if err != nil {
		return err
	}
	// This access probe writes nothing; preserve its validation result.
	defer func() { _ = file.Close() }()
	if !os.SameFile(expected.info, info) {
		return ErrHostsChanged
	}
	return nil
}
