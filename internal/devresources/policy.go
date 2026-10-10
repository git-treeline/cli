// Package devresources admits the finite, explicitly reserved managed-dev pool.
package devresources

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const increment = 10

var blocks = [...]int{42000, 42010, 42020, 42030}
var identifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var errRefusal = errors.New("managed dev resource policy unavailable")

type Policy struct{ reservations map[string]int }

func Active() bool {
	return os.Getenv("TREELINE_MANAGED") == "app" && os.Getenv("TREELINE_CHANNEL") == "dev"
}

// Check reads existing configuration and normal allocations without creating,
// repairing or locking either state directory. Call again before each effect.
func Check() (*Policy, error) {
	if !Active() {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, errRefusal
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, errRefusal
	}
	private := filepath.Join(home, ".treeline-dev", "gtl")
	if os.Getenv("GTL_HOME") != private {
		return nil, errRefusal
	}
	normal := filepath.Join(home, "Library", "Application Support", "git-treeline")
	uc, err := readObject(home, filepath.Join(normal, "config.json"), false)
	if err != nil {
		return nil, errRefusal
	}
	pc, err := readObject(home, filepath.Join(private, "config.json"), true)
	if err != nil {
		return nil, errRefusal
	}
	reg, err := readObject(home, filepath.Join(normal, "registry.json"), false)
	if err != nil {
		return nil, errRefusal
	}
	for _, key := range []string{"redis", "tunnel", "editor"} {
		if value, present := pc[key]; present {
			entries, ok := value.(map[string]any)
			if !ok || len(entries) != 0 {
				return nil, errRefusal
			}
		}
	}
	if router, present := uc["router"]; present {
		entries, ok := router.(map[string]any)
		if !ok {
			return nil, errRefusal
		}
		if value, present := entries["port"]; present {
			port, ok := portNumber(value)
			if !ok || port >= blocks[0] && port < blocks[len(blocks)-1]+increment {
				return nil, errRefusal
			}
		}
	}
	normalPorts, ok := uc["port"].(map[string]any)
	if !ok || !equalsInt(normalPorts["increment"], increment) {
		return nil, errRefusal
	}
	reservations, ok := normalPorts["reservations"].(map[string]any)
	if !ok {
		return nil, errRefusal
	}
	for i, base := range blocks {
		key := "@treeline-dev-qa/slot-0" + strconv.Itoa(i+1)
		if !equalsInt(reservations[key], base) {
			return nil, errRefusal
		}
	}
	for key, value := range reservations {
		base, ok := portNumber(value)
		if !ok || base+increment-1 > 65535 {
			return nil, errRefusal
		}
		for i, reserved := range blocks {
			if base <= reserved+increment-1 && base+increment-1 >= reserved && key != "@treeline-dev-qa/slot-0"+strconv.Itoa(i+1) {
				return nil, errRefusal
			}
		}
	}
	privatePorts, ok := pc["port"].(map[string]any)
	if !ok || !equalsInt(privatePorts["increment"], increment) {
		return nil, errRefusal
	}
	if base, present := privatePorts["base"]; present {
		if _, ok := portNumber(base); !ok {
			return nil, errRefusal
		}
	}
	router, ok := pc["router"].(map[string]any)
	if !ok || router["mode"] != "disabled" {
		return nil, errRefusal
	}
	raw, ok := privatePorts["reservations"].(map[string]any)
	if !ok || len(raw) == 0 || len(raw) > len(blocks) {
		return nil, errRefusal
	}
	policy := &Policy{reservations: make(map[string]int, len(raw))}
	seen := map[int]bool{}
	for key, value := range raw {
		project, branch, split := strings.Cut(key, "/")
		if !identifier.MatchString(project) || split && !validBranch(branch) {
			return nil, errRefusal
		}
		base, ok := portNumber(value)
		if !ok || !blockBase(base) || seen[base] {
			return nil, errRefusal
		}
		seen[base] = true
		policy.reservations[key] = base
	}
	version, ok := integer(reg["version"])
	if !ok || version < 2 || version > 4 {
		return nil, errRefusal
	}
	allocations, ok := reg["allocations"].([]any)
	if !ok {
		return nil, errRefusal
	}
	for _, raw := range allocations {
		allocation, ok := raw.(map[string]any)
		if !ok {
			return nil, errRefusal
		}
		if normalAllocationUsesPostgreSQLNamespace(allocation) || normalAllocationUsesRedisNamespace(allocation) {
			return nil, errRefusal
		}
		ports, err := allocationPorts(allocation)
		if err != nil {
			return nil, errRefusal
		}
		for _, port := range ports {
			if port >= blocks[0] && port < blocks[len(blocks)-1]+increment {
				return nil, errRefusal
			}
		}
	}
	return policy, nil
}

func (p *Policy) Ports(project, branch string, main bool, count int) ([]int, error) {
	if p == nil || !identifier.MatchString(project) || count < 1 || count > increment || branch != "" && !validBranch(branch) {
		return nil, errRefusal
	}
	base, ok := p.reservations[project+"/"+branch]
	if !ok && main {
		base, ok = p.reservations[project]
	}
	if !ok || !blockBase(base) {
		return nil, errRefusal
	}
	ports := make([]int, count)
	for i := range ports {
		ports[i] = base + i
	}
	return ports, nil
}

func (p *Policy) ValidateAllocation(a map[string]any) error {
	project, ok := a["project"].(string)
	if !ok {
		return errRefusal
	}
	branch, ok := a["branch"].(string)
	if !ok {
		return errRefusal
	}
	main := false
	if value, present := a["main_worktree"]; present {
		var ok bool
		main, ok = value.(bool)
		if !ok {
			return errRefusal
		}
	}
	ports, err := allocationPorts(a)
	if err != nil {
		return errRefusal
	}
	expected, err := p.Ports(project, branch, main, len(ports))
	if err != nil {
		return errRefusal
	}
	for i, port := range ports {
		if port != expected[i] {
			return errRefusal
		}
	}
	adapter := ""
	if value, present := a["database_adapter"]; present && value != nil {
		var ok bool
		adapter, ok = value.(string)
		if !ok || adapter != "" && adapter != "sqlite" && adapter != "postgresql" {
			return errRefusal
		}
	}
	primary := ""
	if value, present := a["database"]; present && value != nil {
		var ok bool
		primary, ok = value.(string)
		if !ok {
			return errRefusal
		}
	}
	var databases []string
	if value, present := a["databases"]; present && value != nil {
		switch v := value.(type) {
		case []any:
			for _, value := range v {
				name, ok := value.(string)
				if !ok {
					return errRefusal
				}
				databases = append(databases, name)
			}
		case []string:
			databases = v
		default:
			return errRefusal
		}
	}
	if adapter == "sqlite" {
		if len(databases) != 1 || databases[0] != primary || !validSQLiteName(primary) {
			return errRefusal
		}
	} else if adapter == "postgresql" {
		worktree, _ := a["worktree"].(string)
		if len(databases) != 1 || databases[0] != primary || !validPostgreSQLAllocation(project, worktree, primary, main) {
			return errRefusal
		}
	} else if primary != "" || len(databases) != 0 {
		return errRefusal
	}
	if value, present := a["redis_prefix"]; present && value != nil {
		worktree, _ := a["worktree"].(string)
		if text, ok := value.(string); !ok || text != "" && !validRedisPrefix(project, worktree, text) {
			return errRefusal
		}
	}
	if value, present := a["redis_db"]; present && value != nil {
		return errRefusal
	}
	if value, present := a["database_extra"]; present && value != nil {
		switch v := value.(type) {
		case map[string]any:
			if len(v) != 0 {
				return errRefusal
			}
		case map[string]string:
			if len(v) != 0 {
				return errRefusal
			}
		default:
			return errRefusal
		}
	}
	return nil
}

func blockBase(port int) bool {
	for _, base := range blocks {
		if port == base {
			return true
		}
	}
	return false
}
func validBranch(branch string) bool {
	return branch != "" && !strings.ContainsAny(branch, "\x00\r\n") && strings.TrimSpace(branch) == branch
}
func equalsInt(value any, want int) bool { got, ok := integer(value); return ok && got == want }
func integer(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < 0 || v > 65535 {
			return 0, false
		}
		return int(v), true
	case json.Number:
		n, err := strconv.Atoi(string(v))
		return n, err == nil
	default:
		return 0, false
	}
}
func portNumber(value any) (int, bool) { n, ok := integer(value); return n, ok && n >= 1 && n <= 65535 }

func allocationPorts(a map[string]any) ([]int, error) {
	var ports []int
	if value, present := a["ports"]; present {
		switch list := value.(type) {
		case []any:
			for _, value := range list {
				n, ok := portNumber(value)
				if !ok {
					return nil, errRefusal
				}
				ports = append(ports, n)
			}
		case []int:
			for _, value := range list {
				n, ok := portNumber(value)
				if !ok {
					return nil, errRefusal
				}
				ports = append(ports, n)
			}
		default:
			return nil, errRefusal
		}
		if len(ports) == 0 {
			return nil, errRefusal
		}
	}
	if value, present := a["port"]; present {
		n, ok := portNumber(value)
		if !ok {
			return nil, errRefusal
		}
		if len(ports) == 0 {
			ports = []int{n}
		} else if ports[0] != n {
			return nil, errRefusal
		}
	}
	if len(ports) == 0 {
		return nil, errRefusal
	}
	seen := map[int]bool{}
	for _, port := range ports {
		if seen[port] {
			return nil, errRefusal
		}
		seen[port] = true
	}
	for key, value := range a {
		if !strings.HasPrefix(key, "port_") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(key, "port_"))
		if err != nil || index < 1 || index > len(ports) || key != "port_"+strconv.Itoa(index) || !equalsInt(value, ports[index-1]) {
			return nil, errRefusal
		}
	}
	return ports, nil
}

func checkedPath(home, path string, private bool) (os.FileInfo, error) {
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errRefusal
	}
	current := home
	components := append([]string{""}, strings.Split(relative, string(filepath.Separator))...)
	var info os.FileInfo
	for index, component := range components {
		if component != "" {
			current = filepath.Join(current, component)
		}
		info, err = os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return nil, errRefusal
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) || info.Mode().IsRegular() && owner.Nlink != 1 {
			return nil, errRefusal
		}
		if index != len(components)-1 && !info.IsDir() || index == len(components)-1 && !info.Mode().IsRegular() {
			return nil, errRefusal
		}
		if private && (current == filepath.Dir(path) || current == path) && info.Mode().Perm()&0077 != 0 {
			return nil, errRefusal
		}
	}
	return info, nil
}

func readObject(home, path string, private bool) (map[string]any, error) {
	before, err := checkedPath(home, path, private)
	if err != nil {
		return nil, errRefusal
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errRefusal
	}
	file := os.NewFile(uintptr(fd), path)
	// This descriptor is read-only; the read result determines success.
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() || opened.Size() > 16<<20 {
		return nil, errRefusal
	}
	owner, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || owner.Nlink != 1 || owner.Uid != uint32(os.Getuid()) {
		return nil, errRefusal
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		return nil, errRefusal
	}
	after, err := checkedPath(home, path, private)
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, errRefusal
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, errRefusal
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errRefusal
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errRefusal
	}
	return object, nil
}

// decodeValue rejects duplicate keys instead of accepting JSON's last value.
func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, errRefusal
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return nil, errRefusal
			}
			key, ok := token.(string)
			if !ok {
				return nil, errRefusal
			}
			if _, duplicate := object[key]; duplicate {
				return nil, errRefusal
			}
			value, err := decodeValue(decoder)
			if err != nil {
				return nil, errRefusal
			}
			object[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errRefusal
		}
		return object, nil
	case '[':
		array := []any{}
		for decoder.More() {
			value, err := decodeValue(decoder)
			if err != nil {
				return nil, errRefusal
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errRefusal
		}
		return array, nil
	default:
		return nil, errRefusal
	}
}
