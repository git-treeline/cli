package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadProvision(t *testing.T, yml string) ProvisionConfig {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadProjectConfig(dir).Provision()
}

func TestProvision_AbsentSection_NotPresent(t *testing.T) {
	cfg := loadProvision(t, "project: test\n")
	if cfg.Present {
		t.Error("expected Present=false when no provision: section")
	}
	if len(cfg.Apt) != 0 || len(cfg.Services) != 0 {
		t.Error("expected empty apt/services")
	}
}

func TestProvision_FullSection(t *testing.T) {
	cfg := loadProvision(t, `
project: salt
database:
  template: salt_development
provision:
  apt: [libvips, imagemagick]
  services: [redis-server]
  database:
    source: production
    hydrate: "bin/rails db:schema:load db:seed"
`)
	if !cfg.Present {
		t.Fatal("expected Present=true")
	}
	if got, want := cfg.Apt, []string{"libvips", "imagemagick"}; !equalStrs(got, want) {
		t.Errorf("apt = %v, want %v", got, want)
	}
	if got, want := cfg.Services, []string{"redis-server"}; !equalStrs(got, want) {
		t.Errorf("services = %v, want %v", got, want)
	}
	// Template falls back to top-level database.template.
	if cfg.Database.Template != "salt_development" {
		t.Errorf("template = %q, want salt_development", cfg.Database.Template)
	}
	if cfg.Database.Source != "production" {
		t.Errorf("source = %q, want production", cfg.Database.Source)
	}
	if cfg.Database.Hydrate != "bin/rails db:schema:load db:seed" {
		t.Errorf("hydrate = %q", cfg.Database.Hydrate)
	}
}

func TestProvision_TemplateOverride(t *testing.T) {
	cfg := loadProvision(t, `
database:
  template: top_level_tmpl
provision:
  database:
    template: override_tmpl
`)
	if cfg.Database.Template != "override_tmpl" {
		t.Errorf("template = %q, want override_tmpl (provision block wins)", cfg.Database.Template)
	}
}

func TestProvision_PresentButEmpty(t *testing.T) {
	cfg := loadProvision(t, `
provision:
  apt: [libvips]
`)
	if !cfg.Present {
		t.Fatal("expected Present=true")
	}
	if cfg.Database.Template != "" {
		t.Errorf("template = %q, want empty (no top-level template)", cfg.Database.Template)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
