package devresources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

const postgresConfig = "project: host_migration_qa\ndatabase: {adapter: postgresql, template: treeline_dev_host_migration_qa_template, name: '{template}_{worktree}', host: 127.0.0.1, port: 5432, user: qa}\n"

func TestManagedDevPostgreSQLProject(t *testing.T) {
	for name, body := range map[string]string{
		"valid":           postgresConfig,
		"other project":   strings.Replace(postgresConfig, "project: host_migration_qa", "project: production", 1),
		"other template":  strings.Replace(postgresConfig, PostgreSQLTemplate, "production", 1),
		"remote":          strings.Replace(postgresConfig, "127.0.0.1", "db.example.com", 1),
		"implicit":        strings.Replace(postgresConfig, ", user: qa", "", 1),
		"bad port":        strings.Replace(postgresConfig, "5432", "65536", 1),
		"fractional port": strings.Replace(postgresConfig, "5432", "5432.5", 1),
		"extra":           strings.Replace(postgresConfig, "user: qa", "user: qa, extra: [other]", 1),
		"provision":       postgresConfig + "provision: {database: {template: production}}\n",
		"hook":            postgresConfig + "hooks: {post_setup: 'echo nope'}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			err := ValidateProject(config.LoadProjectConfigReadOnlyQuiet(root))
			if (err == nil) != (name == "valid") {
				t.Fatalf("admission: %v", err)
			}
		})
	}
}

func TestManagedDevPostgreSQLAllocation(t *testing.T) {
	policy := &Policy{reservations: map[string]int{PostgreSQLProject + "/qa-one": 42010, PostgreSQLProject: 42000}}
	for _, main := range []bool{false, true} {
		for _, change := range []string{"none", "template", "sibling", "long", "primary", "extra", "adapter"} {
			t.Run(change+map[bool]string{true: " main", false: " linked"}[main], func(t *testing.T) {
				name := PostgreSQLTemplate + "_qa_one"
				port := 42010
				if main {
					name = PostgreSQLTemplate
					port = 42000
				}
				a := map[string]any{"project": PostgreSQLProject, "branch": "qa-one", "main_worktree": main, "worktree": "/private/qa-one", "port": port, "database_adapter": "postgresql", "database": name, "databases": []string{name}}
				switch change {
				case "template":
					a["database"], a["databases"] = "production", []string{"production"}
				case "sibling":
					a["database"], a["databases"] = PostgreSQLTemplate+"_qa_two", []string{PostgreSQLTemplate + "_qa_two"}
				case "long":
					a["worktree"] = "/private/" + strings.Repeat("x", 64)
					a["database"], a["databases"] = PostgreSQLTemplate+"_"+strings.Repeat("x", 64), []string{PostgreSQLTemplate + "_" + strings.Repeat("x", 64)}
				case "primary":
					a["database"] = "other"
				case "extra":
					a["database_extra"] = map[string]string{"other": "other"}
				case "adapter":
					a["database_adapter"] = "mysql"
				}
				// Main reservations take precedence only when no branch reservation exists.
				if main {
					a["branch"] = "main"
				}
				if err := policy.ValidateAllocation(a); (err == nil) != (change == "none") {
					t.Fatalf("admission: %v", err)
				}
			})
		}
	}
}

func TestManagedDevPostgreSQLRejectsAmbientRouting(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(postgresConfig), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"PGHOSTADDR", "PGSERVICE", "PGDATABASE", "PGOPTIONS"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "private-value")
			if err := ValidateProject(config.LoadProjectConfigReadOnlyQuiet(root)); err == nil {
				t.Fatal("ambient routing admitted")
			}
		})
	}
}

func TestManagedDevPostgreSQLCannotAllocateTemplateAsClone(t *testing.T) {
	if validPostgreSQLAllocation(PostgreSQLProject, "/private/---", PostgreSQLTemplate, false) {
		t.Fatal("template admitted as clone")
	}
}

func TestManagedDevPostgreSQLNormalAllocationsProtected(t *testing.T) {
	for _, field := range []string{"database", "databases"} {
		for _, name := range []string{PostgreSQLTemplate, PostgreSQLTemplate + "_qa_one", "production"} {
			t.Run(field+"/"+name, func(t *testing.T) {
				f := newFixture(t)
				entry := f.registry["allocations"].([]any)[0].(map[string]any)
				entry[field] = name
				if field == "databases" {
					entry[field] = []any{name}
				}
				f.write(t)
				before, err := os.ReadFile(filepath.Join(f.normal, "registry.json"))
				if err != nil {
					t.Fatal(err)
				}
				_, err = Check()
				if (err == nil) != (name == "production") {
					t.Fatalf("collision admission: %v", err)
				}
				after, err := os.ReadFile(filepath.Join(f.normal, "registry.json"))
				if err != nil || string(before) != string(after) {
					t.Fatal("normal registry changed", err)
				}
			})
		}
	}
}
