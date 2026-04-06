package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/git-treeline/git-treeline/internal/platform"
)

var UserDefaults = map[string]any{
	"port": map[string]any{
		"base":      float64(3000),
		"increment": float64(10),
	},
	"redis": map[string]any{
		"strategy": "prefixed",
		"url":      "redis://localhost:6379",
	},
	"router": map[string]any{
		"port": float64(3001),
	},
	"tunnel": map[string]any{},
}

type UserConfig struct {
	Path string
	Data map[string]any
}

func LoadUserConfig(path string) *UserConfig {
	if path == "" {
		path = platform.ConfigFile()
	}

	uc := &UserConfig{Path: path}
	uc.Data = uc.load()
	return uc
}

func (uc *UserConfig) PortBase() int {
	v := Dig(uc.Data, "port", "base")
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 3000
}

func (uc *UserConfig) PortIncrement() int {
	v := Dig(uc.Data, "port", "increment")
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 10
}

func (uc *UserConfig) PortReservations() map[string]int {
	raw, ok := Dig(uc.Data, "port", "reservations").(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]int, len(raw))
	for project, v := range raw {
		if f, ok := v.(float64); ok {
			result[project] = int(f)
		}
	}
	return result
}

// ReservedPorts returns all ports covered by reservations. Each reservation
// blocks a full increment-sized range so multi-port projects are protected.
func (uc *UserConfig) ReservedPorts() map[int]bool {
	reservations := uc.PortReservations()
	if len(reservations) == 0 {
		return nil
	}
	inc := uc.PortIncrement()
	set := make(map[int]bool, len(reservations)*inc)
	for _, base := range reservations {
		for i := range inc {
			set[base+i] = true
		}
	}
	return set
}

func (uc *UserConfig) RedisStrategy() string {
	v := Dig(uc.Data, "redis", "strategy")
	if s, ok := v.(string); ok {
		return s
	}
	return "prefixed"
}

func (uc *UserConfig) RedisURL() string {
	v := Dig(uc.Data, "redis", "url")
	if s, ok := v.(string); ok {
		return s
	}
	return "redis://localhost:6379"
}

// RouterPort returns the port the subdomain router listens on. Default 3001.
// Kept off 3000 so gtl proxy can still forward OAuth/webhook callbacks on :3000.
func (uc *UserConfig) RouterPort() int {
	v := Dig(uc.Data, "router", "port")
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 3001
}

// RouterDomain returns the base domain for local routing. Default: "localhost".
func (uc *UserConfig) RouterDomain() string {
	if v, ok := Dig(uc.Data, "router", "domain").(string); ok && v != "" {
		return v
	}
	return "localhost"
}

// RouterAliases returns static alias routes from the user config (e.g.
// {"grafana": 3100}). These are per-machine routes for personal services.
func (uc *UserConfig) RouterAliases() map[string]int {
	raw, ok := Dig(uc.Data, "router", "aliases").(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]int, len(raw))
	for name, v := range raw {
		if f, ok := v.(float64); ok {
			result[name] = int(f)
		}
	}
	return result
}

// TunnelDefault returns the name of the default tunnel config, or "".
func (uc *UserConfig) TunnelDefault() string {
	if v, ok := Dig(uc.Data, "tunnel", "default").(string); ok {
		return v
	}
	return ""
}

// TunnelName resolves a tunnel name: returns override if non-empty, else the
// configured default. Used by cmd/tunnel and cmd/share to support --tunnel.
func (uc *UserConfig) TunnelName(override string) string {
	if override != "" {
		return override
	}
	return uc.TunnelDefault()
}

// TunnelDomain returns the domain for a tunnel. If override is non-empty it
// selects that tunnel config; otherwise the default is used.
func (uc *UserConfig) TunnelDomain(override string) string {
	name := uc.TunnelName(override)
	if name == "" {
		return ""
	}
	if v, ok := Dig(uc.Data, "tunnel", "tunnels", name, "domain").(string); ok {
		return v
	}
	return ""
}

// TunnelConfigs returns all configured tunnels as a map of name → domain.
func (uc *UserConfig) TunnelConfigs() map[string]string {
	raw, ok := Dig(uc.Data, "tunnel", "tunnels").(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(raw))
	for name, v := range raw {
		if entry, ok := v.(map[string]any); ok {
			domain, _ := entry["domain"].(string)
			result[name] = domain
		}
	}
	return result
}

// DeleteTunnel removes a named tunnel from config. Returns the new default
// name (may be empty if no tunnels remain). Does not call Save().
func (uc *UserConfig) DeleteTunnel(name string) string {
	tunnels, ok := Dig(uc.Data, "tunnel", "tunnels").(map[string]any)
	if !ok {
		return uc.TunnelDefault()
	}
	delete(tunnels, name)

	if uc.TunnelDefault() != name {
		return uc.TunnelDefault()
	}
	newDefault := ""
	for remaining := range tunnels {
		newDefault = remaining
		break
	}
	uc.Set("tunnel.default", newDefault)
	return newDefault
}

// WorktreePathTemplate returns the configured worktree path template, or "".
// The template supports {project} and {branch} interpolation and is resolved
// relative to the main repo root. Example: ".worktrees/{branch}".
func (uc *UserConfig) WorktreePathTemplate() string {
	if v, ok := Dig(uc.Data, "worktree", "path").(string); ok {
		return v
	}
	return ""
}

// ResolveWorktreePath applies the worktree path template. Returns "" if no
// template is configured (caller should fall back to default behavior).
func (uc *UserConfig) ResolveWorktreePath(mainRepo, project, branch string) string {
	tmpl := uc.WorktreePathTemplate()
	if tmpl == "" {
		return ""
	}
	resolved := strings.NewReplacer(
		"{project}", project,
		"{branch}", branch,
	).Replace(tmpl)

	if filepath.IsAbs(resolved) {
		return resolved
	}
	return filepath.Join(mainRepo, resolved)
}

// EditorName returns the stored editor name (e.g. "cursor", "vscode"), or empty.
func (uc *UserConfig) EditorName() string {
	if v, ok := Dig(uc.Data, "editor", "name").(string); ok {
		return v
	}
	return ""
}

// SetEditorName stores the editor name in config. Call Save() to persist.
func (uc *UserConfig) SetEditorName(name string) {
	uc.Set("editor.name", name)
}

// EditorTheme returns a theme override for the given project or project/branch.
func (uc *UserConfig) EditorTheme(project, branch string) string {
	return uc.resolveEditorOverride("themes", project, branch)
}

// EditorColor returns a color override for the given project or project/branch.
func (uc *UserConfig) EditorColor(project, branch string) string {
	return uc.resolveEditorOverride("colors", project, branch)
}

// resolveEditorOverride looks up a value in editor.<key> by project/branch
// then project-only, matching the same precedence as port reservations.
func (uc *UserConfig) resolveEditorOverride(key, project, branch string) string {
	raw, ok := Dig(uc.Data, "editor", key).(map[string]any)
	if !ok {
		return ""
	}
	if branch != "" {
		if v, ok := raw[project+"/"+branch].(string); ok {
			return v
		}
	}
	if v, ok := raw[project].(string); ok {
		return v
	}
	return ""
}

func (uc *UserConfig) Get(dottedKey string) any {
	keys := splitDotted(dottedKey)
	return Dig(uc.Data, keys...)
}

func (uc *UserConfig) Set(dottedKey string, value any) {
	keys := splitDotted(dottedKey)
	m := uc.Data
	for _, k := range keys[:len(keys)-1] {
		child, ok := m[k].(map[string]any)
		if !ok {
			child = make(map[string]any)
			m[k] = child
		}
		m = child
	}
	m[keys[len(keys)-1]] = value
}

func (uc *UserConfig) Save() error {
	if err := os.MkdirAll(filepath.Dir(uc.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(uc.Data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(uc.Path, append(data, '\n'), 0o644)
}

func (uc *UserConfig) Exists() bool {
	_, err := os.Stat(uc.Path)
	return err == nil
}

func (uc *UserConfig) Init() error {
	if err := os.MkdirAll(filepath.Dir(uc.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(UserDefaults, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(uc.Path, append(data, '\n'), 0o644)
}

var userKnownKeys = map[string]bool{
	"port": true, "redis": true, "router": true, "tunnel": true,
	"worktree": true, "editor": true,
}

func (uc *UserConfig) load() map[string]any {
	raw, err := os.ReadFile(uc.Path)
	if err != nil {
		return copyMap(UserDefaults)
	}

	var userData map[string]any
	if err := json.Unmarshal(raw, &userData); err != nil {
		return copyMap(UserDefaults)
	}

	WarnUnknownKeys(userData, userKnownKeys, filepath.Base(uc.Path))

	merged := DeepMerge(UserDefaults, userData)
	migrateTunnelConfig(merged)
	return merged
}

// migrateTunnelConfig converts the legacy flat tunnel config
// (tunnel.name + tunnel.domain) to the multi-tunnel format
// (tunnel.default + tunnel.tunnels map). No-op if already migrated.
func migrateTunnelConfig(data map[string]any) {
	tunnelData, ok := data["tunnel"].(map[string]any)
	if !ok {
		return
	}
	if _, hasTunnels := tunnelData["tunnels"]; hasTunnels {
		return
	}
	name, hasName := tunnelData["name"].(string)
	if !hasName || name == "" {
		return
	}
	domain, _ := tunnelData["domain"].(string)

	entry := map[string]any{}
	if domain != "" {
		entry["domain"] = domain
	}
	tunnelData["tunnels"] = map[string]any{name: entry}
	tunnelData["default"] = name
	delete(tunnelData, "name")
	delete(tunnelData, "domain")
}

func splitDotted(key string) []string {
	return strings.Split(key, ".")
}

// copyMap creates a deep copy of a map[string]any via JSON round-trip.
// Errors are ignored because Marshal/Unmarshal of map[string]any with
// primitive values (strings, floats, nested maps) cannot fail.
func copyMap(m map[string]any) map[string]any {
	data, _ := json.Marshal(m)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}
