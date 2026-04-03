package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserConfig_Defaults(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.PortBase() != 3000 {
		t.Errorf("expected 3000, got %d", uc.PortBase())
	}
	if uc.PortIncrement() != 10 {
		t.Errorf("expected 10, got %d", uc.PortIncrement())
	}
	if uc.RedisStrategy() != "prefixed" {
		t.Errorf("expected prefixed, got %s", uc.RedisStrategy())
	}
	if uc.RedisURL() != "redis://localhost:6379" {
		t.Errorf("expected redis://localhost:6379, got %s", uc.RedisURL())
	}
}

func TestUserConfig_CustomValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"port":{"base":4000,"increment":20}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.PortBase() != 4000 {
		t.Errorf("expected 4000, got %d", uc.PortBase())
	}
	if uc.PortIncrement() != 20 {
		t.Errorf("expected 20, got %d", uc.PortIncrement())
	}
	if uc.RedisStrategy() != "prefixed" {
		t.Errorf("expected prefixed default, got %s", uc.RedisStrategy())
	}
}

func TestUserConfig_Init(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.json")
	uc := LoadUserConfig(path)

	if uc.Exists() {
		t.Error("expected Exists() to be false before init")
	}
	if err := uc.Init(); err != nil {
		t.Fatal(err)
	}
	if !uc.Exists() {
		t.Error("expected Exists() to be true after init")
	}
}

func TestUserConfig_Get_TopLevel(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	val := uc.Get("port")
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", val)
	}
	if m["base"] != float64(3000) {
		t.Errorf("expected 3000, got %v", m["base"])
	}
}

func TestUserConfig_Get_Nested(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	val := uc.Get("port.base")
	if val != float64(3000) {
		t.Errorf("expected 3000, got %v", val)
	}
}

func TestUserConfig_Get_Missing(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.Get("nonexistent.key") != nil {
		t.Error("expected nil for missing key")
	}
}

func TestUserConfig_Set_Existing(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	uc.Set("port.base", float64(5000))
	if uc.PortBase() != 5000 {
		t.Errorf("expected 5000, got %d", uc.PortBase())
	}
}

func TestUserConfig_Set_NewNestedKey(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	uc.Set("custom.nested.value", "hello")
	val := uc.Get("custom.nested.value")
	if val != "hello" {
		t.Errorf("expected hello, got %v", val)
	}
}

func TestUserConfig_Save_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	uc := LoadUserConfig(path)
	uc.Set("port.base", float64(4000))
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadUserConfig(path)
	if reloaded.PortBase() != 4000 {
		t.Errorf("expected 4000 after reload, got %d", reloaded.PortBase())
	}
	if reloaded.PortIncrement() != 10 {
		t.Errorf("expected default increment 10 preserved, got %d", reloaded.PortIncrement())
	}
}
