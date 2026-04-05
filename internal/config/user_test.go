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

func TestUserConfig_TunnelDefault_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.TunnelDefault() != "" {
		t.Errorf("expected empty, got %s", uc.TunnelDefault())
	}
}

func TestUserConfig_TunnelName_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.TunnelName("") != "" {
		t.Errorf("expected empty, got %s", uc.TunnelName(""))
	}
}

func TestUserConfig_TunnelName_Override(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"default":"gtl","tunnels":{"gtl":{"domain":"myteam.dev"}}}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.TunnelName("") != "gtl" {
		t.Errorf("expected gtl from default, got %s", uc.TunnelName(""))
	}
	if uc.TunnelName("other") != "other" {
		t.Errorf("expected override to win, got %s", uc.TunnelName("other"))
	}
}

func TestUserConfig_TunnelDomain_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if uc.TunnelDomain("") != "" {
		t.Errorf("expected empty, got %s", uc.TunnelDomain(""))
	}
}

func TestUserConfig_TunnelDomain_NewFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"default":"gtl","tunnels":{"gtl":{"domain":"myteam.dev"},"gtl-personal":{"domain":"personal.dev"}}}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.TunnelDomain("") != "myteam.dev" {
		t.Errorf("expected myteam.dev from default, got %s", uc.TunnelDomain(""))
	}
	if uc.TunnelDomain("gtl-personal") != "personal.dev" {
		t.Errorf("expected personal.dev from override, got %s", uc.TunnelDomain("gtl-personal"))
	}
	if uc.TunnelDomain("nonexistent") != "" {
		t.Errorf("expected empty for unknown tunnel, got %s", uc.TunnelDomain("nonexistent"))
	}
}

func TestUserConfig_TunnelConfigs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"default":"gtl","tunnels":{"gtl":{"domain":"myteam.dev"},"gtl-personal":{"domain":"personal.dev"}}}}`), 0o644)

	uc := LoadUserConfig(path)
	configs := uc.TunnelConfigs()
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}
	if configs["gtl"] != "myteam.dev" {
		t.Errorf("expected myteam.dev, got %s", configs["gtl"])
	}
	if configs["gtl-personal"] != "personal.dev" {
		t.Errorf("expected personal.dev, got %s", configs["gtl-personal"])
	}
}

func TestUserConfig_TunnelConfigs_Empty(t *testing.T) {
	uc := LoadUserConfig("/nonexistent/config.json")
	if configs := uc.TunnelConfigs(); configs != nil {
		t.Errorf("expected nil, got %v", configs)
	}
}

// --- Migration from legacy tunnel config ---

func TestUserConfig_TunnelMigration_LegacyFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"name":"gtl","domain":"myteam.dev"}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.TunnelDefault() != "gtl" {
		t.Errorf("expected migrated default 'gtl', got %q", uc.TunnelDefault())
	}
	if uc.TunnelName("") != "gtl" {
		t.Errorf("expected migrated name 'gtl', got %q", uc.TunnelName(""))
	}
	if uc.TunnelDomain("") != "myteam.dev" {
		t.Errorf("expected migrated domain 'myteam.dev', got %q", uc.TunnelDomain(""))
	}
	configs := uc.TunnelConfigs()
	if len(configs) != 1 {
		t.Fatalf("expected 1 migrated config, got %d", len(configs))
	}
}

func TestUserConfig_TunnelMigration_PersistsOnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"name":"gtl","domain":"myteam.dev"}}`), 0o644)

	uc := LoadUserConfig(path)
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadUserConfig(path)
	if reloaded.TunnelDefault() != "gtl" {
		t.Errorf("expected default preserved after save, got %q", reloaded.TunnelDefault())
	}
	if reloaded.TunnelDomain("") != "myteam.dev" {
		t.Errorf("expected domain preserved after save, got %q", reloaded.TunnelDomain(""))
	}
	// Legacy keys should be gone
	if reloaded.Get("tunnel.name") != nil {
		t.Error("expected legacy tunnel.name to be removed after save")
	}
	if reloaded.Get("tunnel.domain") != nil {
		t.Error("expected legacy tunnel.domain to be removed after save")
	}
}

func TestUserConfig_TunnelMigration_NameOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"name":"gtl"}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.TunnelDefault() != "gtl" {
		t.Errorf("expected migrated default, got %q", uc.TunnelDefault())
	}
	if uc.TunnelDomain("") != "" {
		t.Errorf("expected empty domain, got %q", uc.TunnelDomain(""))
	}
}

func TestUserConfig_TunnelMigration_NoopIfAlreadyNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"tunnel":{"default":"gtl","tunnels":{"gtl":{"domain":"myteam.dev"}}}}`), 0o644)

	uc := LoadUserConfig(path)
	if uc.TunnelDomain("") != "myteam.dev" {
		t.Errorf("expected myteam.dev, got %q", uc.TunnelDomain(""))
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
