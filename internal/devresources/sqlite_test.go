package devresources

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

func TestValidateSQLiteProject(t *testing.T) {
	for name, database := range map[string]string{
		"valid":                 "adapter: sqlite, template: seed/template.db, name: '{template}_{worktree}'",
		"literal":               "adapter: sqlite, template: template.db, name: local.db",
		"project token":         "adapter: sqlite, template: template.db, name: '{project}_{worktree}'",
		"missing template":      "adapter: sqlite",
		"absolute template":     "adapter: sqlite, template: /outside.db",
		"parent template":       "adapter: sqlite, template: ../outside.db",
		"git template":          "adapter: sqlite, template: .Git/config",
		"noncanonical template": "adapter: sqlite, template: data/../template.db",
		"control template":      "adapter: sqlite, template: \"template\\t.db\"",
		"unknown token":         "adapter: sqlite, template: template.db, name: '{unknown}'",
		"absolute name":         "adapter: sqlite, template: template.db, name: /outside.db",
		"parent name":           "adapter: sqlite, template: template.db, name: ../outside.db",
		"git name":              "adapter: sqlite, template: template.db, name: .git/config",
		"list name":             "adapter: sqlite, template: template.db, name: [one, two]",
		"extras":                "adapter: sqlite, template: template.db, extra: []",
		"connection":            "adapter: sqlite, template: template.db, host: localhost",
		"sync":                  "adapter: sqlite, template: template.db, sync_on_create: false",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: fixture\ndatabase: {"+database+"}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			err := ValidateProject(config.LoadProjectConfigReadOnlyQuiet(root))
			want := name == "valid" || name == "literal" || name == "project token"
			if (err == nil) != want {
				t.Fatalf("admitted=%t, want %t: %v", err == nil, want, err)
			}
		})
	}
}

func TestValidateSQLiteAllocation(t *testing.T) {
	policy := &Policy{reservations: map[string]int{"fixture/qa-one": 42010}}
	for name, mutate := range map[string]func(map[string]any){
		"valid":             func(map[string]any) {},
		"strings":           func(a map[string]any) { a["databases"] = []string{"fixture_qa_one"} },
		"missing primary":   func(a map[string]any) { delete(a, "database") },
		"missing list":      func(a map[string]any) { delete(a, "databases") },
		"different primary": func(a map[string]any) { a["database"] = "other" },
		"many":              func(a map[string]any) { a["databases"] = []any{"fixture_qa_one", "other"} },
		"nonstring":         func(a map[string]any) { a["databases"] = []any{1} },
		"absolute":          func(a map[string]any) { a["database"], a["databases"] = "/outside", []any{"/outside"} },
		"git":               func(a map[string]any) { a["database"], a["databases"] = ".git/config", []any{".git/config"} },
		"parent":            func(a map[string]any) { a["database"], a["databases"] = "../outside", []any{"../outside"} },
		"control":           func(a map[string]any) { a["database"], a["databases"] = "bad\tname", []any{"bad\tname"} },
		"postgresql":        func(a map[string]any) { a["database_adapter"] = "postgresql" },
		"empty postgres": func(a map[string]any) {
			a["database_adapter"], a["database"], a["databases"] = "postgresql", "", []any{}
		},
		"unknown adapter": func(a map[string]any) { a["database_adapter"] = "unknown" },
		"extras":          func(a map[string]any) { a["database_extra"] = map[string]any{"extra": "other"} },
		"redis":           func(a map[string]any) { a["redis_db"] = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			a := fixtureAllocation()
			a["database_adapter"], a["database"], a["databases"] = "sqlite", "fixture_qa_one", []any{"fixture_qa_one"}
			mutate(a)
			err := policy.ValidateAllocation(a)
			want := name == "valid" || name == "strings"
			if (err == nil) != want {
				t.Fatalf("admitted=%t, want %t: %v", err == nil, want, err)
			}
		})
	}
}

func sqliteFileFixture(t *testing.T) (string, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".treeline-dev", "workspaces", "fixture")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "outside.db")
	if err := os.WriteFile(outside, []byte("outside sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(outside)
		if err != nil || string(data) != "outside sentinel" {
			t.Fatalf("outside sentinel changed: %q %v", data, err)
		}
	})
	return root, outside
}

func TestValidateSQLiteFile(t *testing.T) {
	for _, kind := range []string{"missing", "existing", "missing parent", "missing root", "absolute", "parent", "dot", "noncanonical", "control", "git", "outside root", "root alias", "workspace alias", "parent symlink", "file symlink", "dangling symlink", "hardlink", "directory", "fifo", "writable file", "writable parent"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := sqliteFileFixture(t)
			name := "database.db"
			path := filepath.Join(root, name)
			var err error
			switch kind {
			case "existing":
				err = os.WriteFile(path, []byte("fixture"), 0600)
			case "missing parent":
				name = "data/nested/database.db"
			case "missing root":
				root += "-missing"
			case "absolute":
				name = outside
			case "parent":
				name = "../outside.db"
			case "dot":
				name = "."
			case "noncanonical":
				name = "data/../database.db"
			case "control":
				name = "database\t.db"
			case "git":
				name = ".GiT/config"
			case "outside root":
				root = filepath.Dir(outside)
			case "root alias":
				err = os.Symlink(root, root+"-alias")
				root += "-alias"
			case "workspace alias":
				workspace := filepath.Dir(root)
				if err = os.Rename(workspace, workspace+"-real"); err == nil {
					err = os.Symlink(workspace+"-real", workspace)
				}
			case "parent symlink":
				err = os.Symlink(filepath.Dir(outside), filepath.Join(root, "link"))
				name = "link/outside.db"
			case "file symlink":
				err = os.Symlink(outside, path)
			case "dangling symlink":
				err = os.Symlink(outside+"-missing", path)
			case "hardlink":
				err = os.Link(outside, path)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			case "writable file":
				if err = os.WriteFile(path, []byte("fixture"), 0600); err == nil {
					err = os.Chmod(path, 0666)
				}
			case "writable parent":
				err = os.Chmod(root, 0777)
			}
			if err != nil {
				t.Fatal(err)
			}
			want := kind == "missing" || kind == "existing" || kind == "missing parent" || kind == "missing root"
			if err := ValidateSQLiteFile(root, name); (err == nil) != want {
				t.Fatalf("admitted=%t, want %t: %v", err == nil, want, err)
			}
		})
	}
}

func TestValidateSQLiteCompanions(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		for _, kind := range []string{"regular", "symlink", "dangling", "hardlink", "directory", "fifo"} {
			t.Run(suffix+"/"+kind, func(t *testing.T) {
				root, outside := sqliteFileFixture(t)
				path := filepath.Join(root, "database.db"+suffix)
				var err error
				switch kind {
				case "regular":
					err = os.WriteFile(path, []byte("fixture"), 0600)
				case "symlink":
					err = os.Symlink(outside, path)
				case "dangling":
					err = os.Symlink(outside+"-missing", path)
				case "hardlink":
					err = os.Link(outside, path)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "fifo":
					err = syscall.Mkfifo(path, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidateSQLiteFile(root, "database.db"); (err == nil) != (kind == "regular") {
					t.Fatalf("unexpected companion admission: %v", err)
				}
			})
		}
	}
}

func TestValidateProjectRejectsDerivedRedisURL(t *testing.T) {
	for _, adapter := range []string{"http", "sqlite"} {
		for _, pattern := range []string{"{port}", "{database}", "{database.name}", "redis://localhost:6379", "redis_url", "${REDIS_URL}", "{redis_url}", "prefix-{redis_url}/1"} {
			t.Run(adapter+"/"+pattern, func(t *testing.T) {
				root := t.TempDir()
				body := "project: fixture\n"
				if adapter == "sqlite" {
					body += "database: {adapter: sqlite, template: template.db}\n"
				}
				if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				pc := config.LoadProjectConfigReadOnlyQuiet(root)
				env := map[string]any{"VALUE": pattern}
				pc.Data["env"] = env
				err := ValidateProject(pc)
				if strings.Contains(pattern, "{redis_url}") {
					if err == nil || !strings.Contains(err.Error(), "cannot derive {redis_url}") {
						t.Fatalf("derived Redis URL lacked explicit refusal: %v", err)
					}
				} else if err != nil {
					t.Fatalf("supported token or literal refused: %v", err)
				}
				if !reflect.DeepEqual(pc.Data["env"], map[string]any{"VALUE": pattern}) {
					t.Fatal("validation changed the user's environment template")
				}
			})
		}
	}
}
