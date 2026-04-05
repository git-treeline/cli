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

func TestUserConfig_EditorTheme_ProjectLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"editor":{"themes":{"myapp":"Monokai"}}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.EditorTheme("myapp", "main") != "Monokai" {
		t.Errorf("expected Monokai, got %s", uc.EditorTheme("myapp", "main"))
	}
	if uc.EditorTheme("other", "main") != "" {
		t.Errorf("expected empty for unknown project, got %s", uc.EditorTheme("other", "main"))
	}
}

func TestUserConfig_EditorTheme_BranchLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"editor":{"themes":{"myapp":"Monokai","myapp/staging":"GitHub Dark"}}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.EditorTheme("myapp", "staging") != "GitHub Dark" {
		t.Errorf("expected branch-level override, got %s", uc.EditorTheme("myapp", "staging"))
	}
	if uc.EditorTheme("myapp", "main") != "Monokai" {
		t.Errorf("expected project-level fallback, got %s", uc.EditorTheme("myapp", "main"))
	}
}

func TestUserConfig_EditorColor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"editor":{"colors":{"myapp":"#1a5276","myapp/staging":"#7b241c"}}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.EditorColor("myapp", "staging") != "#7b241c" {
		t.Errorf("expected branch override, got %s", uc.EditorColor("myapp", "staging"))
	}
	if uc.EditorColor("myapp", "main") != "#1a5276" {
		t.Errorf("expected project fallback, got %s", uc.EditorColor("myapp", "main"))
	}
	if uc.EditorColor("myapp", "") != "#1a5276" {
		t.Errorf("expected project-level with empty branch, got %s", uc.EditorColor("myapp", ""))
	}
}

func TestUserConfig_EditorOverrides_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.EditorTheme("any", "branch") != "" {
		t.Error("expected empty theme from default config")
	}
	if uc.EditorColor("any", "branch") != "" {
		t.Error("expected empty color from default config")
	}
}

func TestUserConfig_EditorName_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.EditorName() != "" {
		t.Errorf("expected empty editor name, got %s", uc.EditorName())
	}
}

func TestUserConfig_EditorName_SetAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	uc := LoadUserConfig(path)
	uc.SetEditorName("cursor")
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadUserConfig(path)
	if reloaded.EditorName() != "cursor" {
		t.Errorf("expected cursor, got %s", reloaded.EditorName())
	}
}

func TestUserConfig_EditorName_DoesNotClobberOtherEditorSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"editor":{"themes":{"myapp":"Monokai"}}}`), 0o644)

	uc := LoadUserConfig(path)
	uc.SetEditorName("vscode")
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadUserConfig(path)
	if reloaded.EditorName() != "vscode" {
		t.Errorf("expected vscode, got %s", reloaded.EditorName())
	}
	if reloaded.EditorTheme("myapp", "") != "Monokai" {
		t.Errorf("theme clobbered: got %s", reloaded.EditorTheme("myapp", ""))
	}
}

func TestUserConfig_PortReservations_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if r := uc.PortReservations(); r != nil {
		t.Errorf("expected nil, got %v", r)
	}
}

func TestUserConfig_PortReservations_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"port":{"base":3000,"increment":10,"reservations":{"myapp":4000,"other":5000}}}`), 0o644)

	uc := LoadUserConfig(path)
	r := uc.PortReservations()
	if r == nil {
		t.Fatal("expected non-nil reservations")
	}
	if r["myapp"] != 4000 {
		t.Errorf("expected myapp=4000, got %d", r["myapp"])
	}
	if r["other"] != 5000 {
		t.Errorf("expected other=5000, got %d", r["other"])
	}
}

func TestUserConfig_PortReservations_SkipsNonFloat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"port":{"reservations":{"a":4000,"b":"not-a-number"}}}`), 0o644)

	uc := LoadUserConfig(path)
	r := uc.PortReservations()
	if len(r) != 1 {
		t.Errorf("expected 1 entry (skipping string), got %d: %v", len(r), r)
	}
}

func TestUserConfig_ReservedPorts_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if r := uc.ReservedPorts(); r != nil {
		t.Errorf("expected nil, got %v", r)
	}
}

func TestUserConfig_ReservedPorts_CoversFullRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"port":{"increment":3,"reservations":{"a":5000}}}`), 0o644)

	uc := LoadUserConfig(path)
	r := uc.ReservedPorts()
	for i := range 3 {
		if !r[5000+i] {
			t.Errorf("expected port %d to be reserved", 5000+i)
		}
	}
	if r[5003] {
		t.Error("port 5003 should not be reserved")
	}
}

func TestUserConfig_RouterPort_Default(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.RouterPort() != 3001 {
		t.Errorf("expected 3001, got %d", uc.RouterPort())
	}
}

func TestUserConfig_RouterPort_Custom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"router":{"port":8443}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.RouterPort() != 8443 {
		t.Errorf("expected 8443, got %d", uc.RouterPort())
	}
}

func TestUserConfig_TunnelDomain_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.TunnelDomain() != "" {
		t.Errorf("expected empty, got %s", uc.TunnelDomain())
	}
}

func TestUserConfig_TunnelDomain_Set(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"domain":"myteam.dev"}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.TunnelDomain() != "myteam.dev" {
		t.Errorf("expected myteam.dev, got %s", uc.TunnelDomain())
	}
}

func TestUserConfig_TunnelName_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.TunnelName() != "" {
		t.Errorf("expected empty, got %s", uc.TunnelName())
	}
}

func TestUserConfig_TunnelName_Set(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"name":"gtl"}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.TunnelName() != "gtl" {
		t.Errorf("expected gtl, got %s", uc.TunnelName())
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
