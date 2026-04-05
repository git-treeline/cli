package platform

import (
	"strings"
	"testing"
)

func TestConfigDir_NotEmpty(t *testing.T) {
	dir := ConfigDir()
	if dir == "" {
		t.Error("ConfigDir returned empty string")
	}
	if !strings.HasSuffix(dir, "git-treeline") {
		t.Errorf("expected path ending in git-treeline, got %s", dir)
	}
}

func TestConfigDir_GTLHome(t *testing.T) {
	t.Setenv("GTL_HOME", "/tmp/gtl-dev-test")
	dir := ConfigDir()
	if dir != "/tmp/gtl-dev-test" {
		t.Errorf("expected /tmp/gtl-dev-test, got %s", dir)
	}
}

func TestIsDevMode(t *testing.T) {
	t.Setenv("GTL_HOME", "")
	if IsDevMode() {
		t.Error("expected IsDevMode=false when GTL_HOME is empty")
	}
	t.Setenv("GTL_HOME", "/tmp/gtl-dev")
	if !IsDevMode() {
		t.Error("expected IsDevMode=true when GTL_HOME is set")
	}
}

func TestDevSuffix(t *testing.T) {
	t.Setenv("GTL_HOME", "")
	if s := DevSuffix(); s != "" {
		t.Errorf("expected empty suffix, got %q", s)
	}
	t.Setenv("GTL_HOME", "/tmp/gtl-dev")
	if s := DevSuffix(); s != ".dev" {
		t.Errorf("expected .dev, got %q", s)
	}
}

func TestConfigFile_EndsWithJSON(t *testing.T) {
	f := ConfigFile()
	if !strings.HasSuffix(f, "config.json") {
		t.Errorf("expected config.json, got %s", f)
	}
}

func TestRegistryFile_EndsWithJSON(t *testing.T) {
	f := RegistryFile()
	if !strings.HasSuffix(f, "registry.json") {
		t.Errorf("expected registry.json, got %s", f)
	}
}
