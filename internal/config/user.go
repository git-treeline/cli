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

func (uc *UserConfig) load() map[string]any {
	raw, err := os.ReadFile(uc.Path)
	if err != nil {
		return copyMap(UserDefaults)
	}

	var userData map[string]any
	if err := json.Unmarshal(raw, &userData); err != nil {
		return copyMap(UserDefaults)
	}

	return DeepMerge(UserDefaults, userData)
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
