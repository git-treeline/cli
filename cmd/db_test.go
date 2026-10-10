package cmd

import (
	"path/filepath"
	"strings"
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

func TestResolvePostgresEndpoint(t *testing.T) {
	env := map[string]string{
		"PGHOST": "env-db.internal",
		"PGPORT": "5433",
		"PGUSER": "env-user",
	}
	getenv := func(name string) string { return env[name] }

	got := resolvePostgresEndpoint("club_feat", []string{"-h", "config-db.internal", "-p", "6432", "-U", "config-user"}, getenv, "os-user")
	if got.Host != "config-db.internal" || got.Port != "6432" || got.User != "config-user" || got.Database != "club_feat" {
		t.Fatalf("config endpoint = %+v", got)
	}

	got = resolvePostgresEndpoint("club_feat", nil, getenv, "os-user")
	if got.Host != "env-db.internal" || got.Port != "5433" || got.User != "env-user" {
		t.Fatalf("environment endpoint = %+v", got)
	}

	got = resolvePostgresEndpoint("club_feat", nil, func(string) string { return "" }, "os-user")
	if got.Host != "libpq default local socket" || got.Port != "5432 (libpq default)" || got.User != "os-user" {
		t.Fatalf("default endpoint = %+v", got)
	}

	env["PGHOSTADDR"] = "192.0.2.10"
	got = resolvePostgresEndpoint("club_feat", []string{"-h", "db.example.com"}, getenv, "os-user")
	if got.Host != "db.example.com" || got.HostAddress != "192.0.2.10" {
		t.Fatalf("host/hostaddr endpoint = %+v", got)
	}
	delete(env, "PGHOST")
	got = resolvePostgresEndpoint("club_feat", nil, getenv, "os-user")
	if got.Host != "unset (hostaddr selects network endpoint)" || got.HostAddress != "192.0.2.10" {
		t.Fatalf("hostaddr-only endpoint = %+v", got)
	}

	env["PGSERVICE"] = "production"
	env["PGSERVICEFILE"] = "/private/pg_service.conf"
	got = resolvePostgresEndpoint("club_feat", nil, getenv, "os-user")
	if got.Service != "production" || got.ServiceFile != "/private/pg_service.conf" ||
		!strings.Contains(got.Host, `libpq service "production"`) || !strings.Contains(got.Port, `libpq service "production"`) {
		t.Fatalf("service-derived endpoint was not disclosed: %+v", got)
	}
}

func TestDestructiveDBTargetShowsEndpointWithoutSecrets(t *testing.T) {
	info := &dbInfo{
		adapterName: "postgresql",
		target:      "club_feat",
		endpoint: databaseEndpoint{
			Host: "db.internal", Port: "6432", User: "deploy", Database: "club_feat",
		},
	}
	got := destructiveDBTarget(info)
	for _, want := range []string{`host="db.internal"`, `port="6432"`, `user="deploy"`, `database="club_feat"`} {
		if !strings.Contains(got, want) {
			t.Errorf("target %q missing %q", got, want)
		}
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

func TestValidateResetSource(t *testing.T) {
	cases := []struct {
		name, target, source string
		wantErr              bool
	}{
		{"worktree_clone_from_template", "myapp_dev_feat", "myapp_development", false},
		{"main_target_is_template", "myapp_development", "myapp_development", true},
		{"main_with_from_other", "myapp_development", "staging_snapshot", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateResetSource(c.target, c.source)
			if c.wantErr && err == nil {
				t.Error("expected refusal when source == target, got nil")
			}
			if !c.wantErr && err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		})
	}
}
