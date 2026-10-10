package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

func TestConfigSetRouterDomainRejectsUnsafeSuffix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GTL_HOME", home)
	if err := configSetCmd.RunE(configSetCmd, []string{"router.domain", "com"}); err == nil {
		t.Fatal("expected public suffix to be rejected")
	}
	if _, err := os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid domain should not be persisted, stat error = %v", err)
	}
}

func TestConfigSetRouterDomainCanonicalizesValue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GTL_HOME", home)
	if err := configSetCmd.RunE(configSetCmd, []string{"router.domain", "PRT.DEV"}); err != nil {
		t.Fatal(err)
	}
	uc := config.LoadUserConfig("")
	if got := uc.RouterDomain(); got != "prt.dev" {
		t.Errorf("router domain = %q, want prt.dev", got)
	}
}

func TestRunRouterRejectsUnsafeDomainFromManualConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GTL_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{\"router\":{\"domain\":\"*.com\"}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRouter(); err == nil {
		t.Fatal("expected manually configured wildcard domain to be rejected")
	}
}
