package config

import (
	"bytes"
	"os"
	"testing"
)

func TestDeepMerge_OverrideWins(t *testing.T) {
	base := map[string]any{"a": "1", "b": "2"}
	over := map[string]any{"a": "X"}
	result := DeepMerge(base, over)
	if result["a"] != "X" {
		t.Errorf("expected override value X, got %v", result["a"])
	}
	if result["b"] != "2" {
		t.Errorf("expected base value 2, got %v", result["b"])
	}
}

func TestDeepMerge_NestedMaps(t *testing.T) {
	base := map[string]any{
		"port": map[string]any{"base": 3000, "increment": 10},
	}
	over := map[string]any{
		"port": map[string]any{"base": 4000},
	}
	result := DeepMerge(base, over)
	port := result["port"].(map[string]any)
	if port["base"] != 4000 {
		t.Errorf("expected 4000, got %v", port["base"])
	}
	if port["increment"] != 10 {
		t.Errorf("expected 10 from base, got %v", port["increment"])
	}
}

func TestDeepMerge_OverrideReplacesNonMap(t *testing.T) {
	base := map[string]any{"key": "string_value"}
	over := map[string]any{"key": map[string]any{"nested": true}}
	result := DeepMerge(base, over)
	if _, ok := result["key"].(map[string]any); !ok {
		t.Errorf("expected map, got %T", result["key"])
	}
}

func TestDeepMerge_DoesNotAliasBaseMaps(t *testing.T) {
	base := map[string]any{
		"router": map[string]any{"port": 3001},
	}
	result := DeepMerge(base, map[string]any{})

	router := result["router"].(map[string]any)
	router["mode"] = "disabled"

	baseRouter := base["router"].(map[string]any)
	if _, ok := baseRouter["mode"]; ok {
		t.Fatal("expected base router map to remain unchanged")
	}
}

func TestDig(t *testing.T) {
	m := map[string]any{
		"port": map[string]any{
			"base": 3000,
		},
	}
	if v := Dig(m, "port", "base"); v != 3000 {
		t.Errorf("expected 3000, got %v", v)
	}
	if v := Dig(m, "port", "missing"); v != nil {
		t.Errorf("expected nil, got %v", v)
	}
	if v := Dig(m, "nope"); v != nil {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestWarnUnknownKeys_NoWarning(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	known := map[string]bool{"port": true, "redis": true}
	WarnUnknownKeys(map[string]any{"port": 1}, known, "config.json")

	_ = w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if buf.Len() > 0 {
		t.Errorf("expected no warnings, got: %s", buf.String())
	}
}

func TestWarnUnknownKeys_UnknownKey(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	known := map[string]bool{"port": true, "redis": true}
	WarnUnknownKeys(map[string]any{"prot": 1, "redis": "x"}, known, "config.json")

	_ = w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte(`"prot"`)) {
		t.Errorf("expected warning about 'prot', got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte(`"port"`)) {
		t.Errorf("expected suggestion 'port', got: %s", output)
	}
}

func TestWarnUnknownKeys_NoSuggestionForDistantKey(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	known := map[string]bool{"port": true}
	WarnUnknownKeys(map[string]any{"zzzzz": 1}, known, "test.yml")

	_ = w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte(`"zzzzz"`)) {
		t.Errorf("expected warning about 'zzzzz', got: %s", output)
	}
	if bytes.Contains([]byte(output), []byte("did you mean")) {
		t.Errorf("should not suggest for distant keys, got: %s", output)
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"port", "port", 0},
		{"port", "prot", 2},
		{"port", "ports", 1},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, tt := range tests {
		if got := levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
