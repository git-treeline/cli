package cmd

import (
	"path/filepath"
	"testing"
)

func TestResolveDBPaths_PostgreSQL(t *testing.T) {
	target, tmpl := resolveDBPaths("postgresql", "/work/myapp-feat", "/work/myapp", "myapp_feat", "myapp_dev")
	if target != "myapp_feat" {
		t.Errorf("target = %q, want %q", target, "myapp_feat")
	}
	if tmpl != "myapp_dev" {
		t.Errorf("template = %q, want %q", tmpl, "myapp_dev")
	}
}

func TestResolveDBPaths_SQLite(t *testing.T) {
	target, tmpl := resolveDBPaths("sqlite", "/work/myapp-feat", "/work/myapp", "dev.db", "seed.db")
	wantTarget := filepath.Join("/work/myapp-feat", "dev.db")
	wantTmpl := filepath.Join("/work/myapp", "seed.db")
	if target != wantTarget {
		t.Errorf("target = %q, want %q", target, wantTarget)
	}
	if tmpl != wantTmpl {
		t.Errorf("template = %q, want %q", tmpl, wantTmpl)
	}
}

func TestResolveDBPaths_SQLite_EmptyTemplate(t *testing.T) {
	target, tmpl := resolveDBPaths("sqlite", "/work/myapp-feat", "/work/myapp", "dev.db", "")
	wantTarget := filepath.Join("/work/myapp-feat", "dev.db")
	if target != wantTarget {
		t.Errorf("target = %q, want %q", target, wantTarget)
	}
	if tmpl != "" {
		t.Errorf("template = %q, want empty", tmpl)
	}
}

func TestResolveDBPaths_PostgreSQL_EmptyTemplate(t *testing.T) {
	target, tmpl := resolveDBPaths("postgresql", "/work/app", "/work/main", "app_branch", "")
	if target != "app_branch" {
		t.Errorf("target = %q, want %q", target, "app_branch")
	}
	if tmpl != "" {
		t.Errorf("template = %q, want empty", tmpl)
	}
}
